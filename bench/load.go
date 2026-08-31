package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/isucon/isucandar"
	"github.com/isucon/isucandar/failure"
)

// seedUserName はシードユーザー(1..20)のログイン名を返す。パスワードは全員 'password'。
func seedUserName() string {
	return fmt.Sprintf("seed_user_%02d", rand.Intn(20)+1)
}

// addErr は step.AddError の一元化ヘルパー。ctx キャンセル/タイムアウトそのものが原因の
// エラー(Load終了時に飛んでくる context.Canceled / context.DeadlineExceeded)だけをノイズとして
// 記録しない。完全な応答から判定した本物の整合性違反(critical)は、ctx がその後キャンセルされて
// いても揉み消してはならないため、cause で判別する(I1: 旧実装は ctx.Err() != nil のときエラー種別を
// 問わず全て握り潰しており、キャンセル直前に成立した本物の違反まで消えてしまっていた)。
func addErr(ctx context.Context, step *isucandar.BenchmarkStep, code failure.StringCode, err error) {
	if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && ctx.Err() != nil {
		return
	}
	step.AddError(failure.NewError(code, err))
}

const (
	// feedReflectDeadline は自分の入札がフィードに現れるまでの許容時間。
	// レギュレーションのリアルタイム性要件(ポーリング間引きによるズルの防止)。
	feedReflectDeadline = 2 * time.Second
	// feedPollInterval はフィードのポーリング間隔。
	feedPollInterval = 100 * time.Millisecond
)

// awaitFeedReflection は自分の入札 bidID が feedReflectDeadline 以内に
// フィードへ現れることを検証する。違反は step に直接記録する。
//
// ctx がキャンセルされた場合(Load終了)は違反として扱わない。走行終了間際の
// ポーリング打ち切りを critical にすると false-FAIL になるため。
func (s *Scenario) awaitFeedReflection(ctx context.Context, step *isucandar.BenchmarkStep,
	c *Client, auctionID, since, bidID int64) {
	deadline := time.Now().Add(feedReflectDeadline)
	for {
		feed, err := c.GetBidFeed(ctx, auctionID, since)
		if err != nil {
			addErr(ctx, step, ErrApplication, err)
			return
		}
		// 検証は毎ポーリング実行する(フィードの不変条件はポーリング回数に関わらず常に成立すべき)。
		if err := ValidateFeedPage(feed, since); err != nil {
			addErr(ctx, step, ErrCritical, fmt.Errorf("auction %d: %w", auctionID, err))
			return
		}
		for _, b := range feed {
			if b.ID == bidID {
				// I2: 加点は「反映を確認できた」ことに対して1回だけ行う。ポーリングのたびに
				// 加点すると、フィード反映が遅い(=デッドラインぎりぎりまでポーリングを重ねる)
				// 実装ほど GETFeed 点が積み上がり、2秒デッドラインが罰するはずの鮮度劣化を
				// 逆に加点してしまう(ポーリング間引きへのインセンティブを生む)。
				step.AddScore(ScoreGETFeed)
				return // 反映を確認できた
			}
		}
		if time.Now().After(deadline) {
			if ctx.Err() != nil {
				return // Load終了に伴う打ち切り。違反ではない
			}
			addErr(ctx, step, ErrCritical,
				fmt.Errorf("auction %d: 入札 id=%d が %v 以内にフィードへ反映されない",
					auctionID, bidID, feedReflectDeadline))
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(feedPollInterval):
		}
	}
}

// bidderIteration は「ログイン→一覧→詳細→入札(競り負けたら再挑戦)」の1セッション。
func (s *Scenario) bidderIteration(ctx context.Context, step *isucandar.BenchmarkStep) {
	c, err := NewClient(s.Target)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	user, err := c.Login(ctx, seedUserName(), "password")
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	// 入札対象は「終了が最も近い20件」= 1ページ目から選ぶ。実サイトの挙動である
	// と同時に、live 件数が増えても入札が分散しないようにする狙いがある
	// (docs/phase4-notes.md 持ち越し1/9: 分散すると FOR UPDATE 検出器が発火しない)。
	l, err := c.GetAuctions(ctx, AuctionListParams{Page: 1})
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(ScoreGETList)
	if err := ValidatePagedListShape(1, l); err != nil {
		addErr(ctx, step, ErrCritical, err)
		return
	}
	list := l.Auctions
	if len(list) == 0 {
		addErr(ctx, step, ErrCritical, fmt.Errorf("GET /api/auctions: 開催中オークションが0件"))
		return
	}
	targetID := list[rand.Intn(len(list))].ID
	// 新規出品には pubsub 経由で人が集まる(終了間際に競りが起きる挙動の再現)。
	// 既に closed になっていた場合は詳細取得後の status チェックで抜ける。
	if id, ok := s.Board.random(); ok && rand.Intn(2) == 0 {
		targetID = id
	}

	// 競り負け(400 too-low)たら現在価格を取り直して上乗せ。最大5回。
	for attempt := 0; attempt < 5; attempt++ {
		d, err := c.GetAuction(ctx, targetID)
		if err != nil {
			addErr(ctx, step, ErrApplication, err)
			return
		}
		step.AddScore(ScoreGETDetail)
		// 走行中に終了処理バッチが closed にした可能性がある。
		// closed への入札は 400 が正しい応答なので、エラーにせず次のイテレーションへ譲る。
		if d.Status != "live" {
			return
		}
		// フィードのカーソル起点。詳細は created_at DESC, id DESC なので先頭が最大 id。
		var sinceID int64
		if len(d.Bids) > 0 {
			sinceID = d.Bids[0].ID
		}
		amount := d.CurrentPrice + 100 + rand.Int63n(400)

		// C1: POST送信前にintentとして記録する。応答が届く前にctxキャンセル/転送エラーが
		// 起きても、サーバー側では既にコミットされている可能性がある(in-flight commit)。
		// 201を受け取れなかった場合にAcceptedBidを作れないだけで「入札されなかった」とは
		// 断定できないため、pendingとして残しValidationで許容判定させる。
		intentID := s.Ledger.Intent(targetID, user.ID, amount)
		bid, code, err := c.PostBid(ctx, targetID, amount)
		if err != nil {
			// 結果不明(転送エラー/5xx/タイムアウト): pendingのまま残す。
			addErr(ctx, step, ErrApplication, err)
			return
		}
		switch code {
		case 201:
			// 201は確定的なコミット。台帳へ昇格させる(応答内容が期待とズレていても
			// 実際にコミットされた値で記録し、その上で内容不一致を別途criticalにする)。
			s.Ledger.Confirm(intentID, AcceptedBid{BidID: bid.ID, AuctionID: targetID, UserID: bid.UserID, Amount: bid.Amount})
			if bid.UserID != user.ID || bid.Amount != amount {
				addErr(ctx, step, ErrCritical,
					fmt.Errorf("POST /api/auctions/%d/bids: 応答内容が不一致 (got user=%d amount=%d, want user=%d amount=%d)",
						targetID, bid.UserID, bid.Amount, user.ID, amount))
				return
			}
			step.AddScore(ScorePOSTBid)
			s.awaitFeedReflection(ctx, step, c, targetID, sinceID, bid.ID)
			return
		case 400:
			// 競り負け: 確定的に未コミット。取り直して再入札。
			s.Ledger.Reject(intentID)
			continue
		default:
			// その他の4xx(401/403/404等)も確定的に未コミットと判断してpendingを解消する。
			s.Ledger.Reject(intentID)
			addErr(ctx, step, ErrApplication,
				fmt.Errorf("POST /api/auctions/%d/bids: 予期しない status %d", targetID, code))
			return
		}
	}
	// 5連敗は人気オークションなら起こりうる。エラーにしない。
}

// watcherIteration は「一覧(ページ回遊・検索あり)→ランダム詳細+不変条件チェック」の回遊。
func (s *Scenario) watcherIteration(ctx context.Context, step *isucandar.BenchmarkStep) {
	c, err := NewClient(s.Target)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	page := 1 + rand.Intn(3)
	p := AuctionListParams{Page: page}
	tag := ScoreGETList
	// switch の3分岐で 1/3 ずつ、q 絞り込み / category 絞り込み / 絞り込み無し を選ぶ
	// (case 0 と case 1 の両方が絞り込みを付けるので、絞り込みを付ける確率自体は 2/3)。
	// q は title 専用プローブに限る —— 一覧レスポンスに description が無いため、
	// description で一致した行を走行中に検証する術がなく、正しい実装を誤判定してしまう。
	switch rand.Intn(3) {
	case 0:
		p.Q = probeTitleOnly
		// q に一致する live は初期データ由来の数件のみで、ベンチが走行中に出品する
		// オークションは(sellerTitles・seller description がプローブ語を含まない
		// 設計のため)絶対に一致しない。したがって一致集合は走行が進むにつれ単調に
		// 減っていき、2ページ目以降はほぼ確実に0件になる(0件では述語検査が
		// 一度も走らず検出力が無い)。q 分岐のときだけ page を1に固定して、
		// 述語検査(q 一致)が実際に働くようにする。category 分岐・絞り込み無し
		// 分岐は、走行中に増えるオークションも母集合に加わり0件に収束しないため、
		// 従来どおり page を1〜3のランダムのままにする。
		p.Page = 1
		page = 1
		tag = ScoreGETSearch
	case 1:
		p.Category = int64(1 + rand.Intn(3))
		tag = ScoreGETSearch
	}
	l, err := c.GetAuctions(ctx, p)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(tag)
	if err := ValidatePagedListShape(page, l); err != nil {
		addErr(ctx, step, ErrCritical, err)
		return
	}
	list := l.Auctions
	// 絞り込み結果は述語に合致していなければならない。これは一方向の検査である:
	// 「期待集合にあるのに返ってこない」のは走行中なら正常(closed になった、
	// 別ページへ移った)だが、「述語に合致しない行が返る」のは常に異常。
	// 「返さなすぎ」は静穏期の Prepare が見る。
	for _, a := range list {
		if p.Q != "" && !strings.Contains(a.Title, p.Q) {
			addErr(ctx, step, ErrCritical,
				fmt.Errorf("GET /api/auctions?q=%s: title が一致しない行が返った (id=%d title=%q)", p.Q, a.ID, a.Title))
			return
		}
		if p.Category != 0 && a.CategoryID != p.Category {
			addErr(ctx, step, ErrCritical,
				fmt.Errorf("GET /api/auctions?category=%d: category_id=%d の行が返った (id=%d)", p.Category, a.CategoryID, a.ID))
			return
		}
	}
	if len(list) == 0 {
		return
	}
	// ブラウザと同じように、一覧に出た出品者のアイコンを取得する。
	// ベンチはイテレーションごとに新しいクライアントを作る(= 毎回キャッシュが空の
	// 新規訪問者)ので、アイコンは必ず再取得される。これが「リクエストのたびに
	// LONGBLOB を DB から読む」仕込みを持続的な負荷にしている。
	//
	// アイコンには採点タグを作らない。アイコンはコストであって報酬ではなく、
	// 速く返せるようになった見返りは「1周が速くなって一覧と詳細の回数が増える」
	// という形で既存の採点に現れる。
	seenSeller := make(map[int64]bool, len(list))
	for _, a := range list {
		if seenSeller[a.Seller.ID] {
			continue
		}
		seenSeller[a.Seller.ID] = true
		code, ct, body, err := c.GetUserIcon(ctx, a.Seller.ID)
		if err != nil {
			addErr(ctx, step, ErrApplication, err)
			return
		}
		if s.Snapshot == nil {
			// 生成データ非搭載モード。全ユーザーがアイコンを持たず正解値も無いので、
			// 取得して負荷はかけるが照合はしない。
			continue
		}
		su, _ := s.Snapshot.UserByID(a.Seller.ID)
		// ステータス起因(一過性の5xx等でありうる)と内容不一致(実装の誤り)を
		// 区別する。前者を即FAILにすると、コネクションプール枯渇のような
		// ありがちな失敗が(1周あたり最大20回呼ばれる)アイコン経路経由で
		// 走行全体を即死させてしまい、他の採点タグ(500でもエラー予算100件の
		// 減点で済む)に比べて著しく不公平になる。
		statusErr, contentErr := ValidateUserIcon(a.Seller.ID, code, ct, body, su)
		if statusErr != nil {
			addErr(ctx, step, ErrApplication, statusErr)
			return
		}
		if contentErr != nil {
			addErr(ctx, step, ErrCritical, contentErr)
			return
		}
	}
	d, err := c.GetAuction(ctx, list[rand.Intn(len(list))].ID)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(ScoreGETDetail)
	if err := ValidateBidsInvariant(d.Bids); err != nil {
		addErr(ctx, step, ErrCritical, fmt.Errorf("auction %d: %w", d.ID, err))
		return
	}
	if int64(len(d.Bids)) != d.BidCount {
		addErr(ctx, step, ErrCritical,
			fmt.Errorf("auction %d: bid_count %d と bids件数 %d が不一致", d.ID, d.BidCount, len(d.Bids)))
		return
	}
	// I3: current_price == max(bids) (入札があれば) / starting_price (なければ)。
	// GET /auctions/:id は1トランザクション(REPEATABLE READスナップショット)で読むよう
	// 参照実装側を直してあるため(docs/phase2-notes.md参照)、詳細レスポンス内では
	// レースなく厳密に成立するはずの不変条件。
	if len(d.Bids) > 0 {
		max := d.Bids[0].Amount
		for _, b := range d.Bids[1:] {
			if b.Amount > max {
				max = b.Amount
			}
		}
		if d.CurrentPrice != max {
			addErr(ctx, step, ErrCritical,
				fmt.Errorf("auction %d: current_price %d が bids最大額 %d と不一致", d.ID, d.CurrentPrice, max))
		}
	} else if d.CurrentPrice != d.StartingPrice {
		addErr(ctx, step, ErrCritical,
			fmt.Errorf("auction %d: 入札0件なのに current_price %d が starting_price %d と不一致", d.ID, d.CurrentPrice, d.StartingPrice))
	}
}

// notifierIteration は「ログイン→通知一覧」の回遊。Load中のスコア源であり、
// 一覧の順序(id DESC)を検証する。欠落そのものは Validation フェーズで照合する。
func (s *Scenario) notifierIteration(ctx context.Context, step *isucandar.BenchmarkStep) {
	c, err := NewClient(s.Target)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	if _, err := c.Login(ctx, seedUserName(), "password"); err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	ns, err := c.GetNotifications(ctx)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(ScoreGETNotifications)
	if err := ValidateNotificationsOrdered(ns); err != nil {
		addErr(ctx, step, ErrCritical, err)
	}
}

// listingBoard は pubsub 経由で配信された新規出品IDを保持する。
//
// isucandar の pubsub.Publish は購読チャネルが満杯だとブロックするため、
// 購読ハンドラは必ず即座に返らなければならない(ここではスライスへの追記のみ)。
type listingBoard struct {
	mu  sync.Mutex
	ids []int64
}

func (b *listingBoard) add(id int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ids = append(b.ids, id)
}

// random は配信済みの出品からランダムに1件返す。1件も無ければ ok=false。
func (b *listingBoard) random() (int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.ids) == 0 {
		return 0, false
	}
	return b.ids[rand.Intn(len(b.ids))], true
}

// sellerTitles と sellerDescription: Load 中に watcherIteration が使う q 検査
// (probeTitleOnly による title 一致のみの判定)は、ここに書く title/description が
// probeTitleOnly を含まないことに依存する安全条件である。一覧レスポンスの
// AuctionSummary には description が無いため、走行中は「title が一致しない行が
// 返ったら異常」としか検証できない。もしここの description に probeTitleOnly が
// 混入すると、正しい実装(title・description の両方を検索対象にする)が
// description 側の一致でその行を返しても、Load 側は title 不一致だけを見て
// critical FAIL にしてしまう(false-FAIL)。TestSearchProbesAreClassified
// (bench/validate_test.go)がこの結合を固定しているので、変更時はそちらも通ること。
var sellerTitles = []string{
	"ラピッドチェア", "オークリーフ・スツール", "ミニマルワークシート",
	"ベルベット・オットマン", "スカンジ・ダイニング",
}

const sellerDescription = "ベンチが出品した椅子"

// sellerIteration は「ログイン→出品→売上確認」の1セッション。
// 出品したオークションは pubsub で入札者シナリオへ配信され、入札が集まる。
// duration は20〜40秒なので、走行中に closed へ遷移して落札 Validation の対象になる。
func (s *Scenario) sellerIteration(ctx context.Context, step *isucandar.BenchmarkStep) {
	c, err := NewClient(s.Target)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	user, err := c.Login(ctx, seedUserName(), "password")
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	title := sellerTitles[rand.Intn(len(sellerTitles))]
	startingPrice := int64(1000 + rand.Intn(9)*500)
	duration := int64(20 + rand.Intn(21)) // 20〜40秒
	created, code, err := c.PostAuction(ctx, title, sellerDescription,
		int64(1+rand.Intn(3)), startingPrice, duration)
	if err != nil {
		// 結果不明(転送エラー/5xx/ctxキャンセル): 応答を受け取れなかっただけで、サーバー側では
		// 既にコミットされている可能性がある(in-flight commit)。この場合ベンチ側はauction ID
		// を知り得ずListingを作れないため、Validationが「想定外のauction」と誤検知(false-FAIL)
		// しうる。bidのIntent/Pending(C1)と対称な仕組みは作れない(先行して仮IDを発番できない)
		// ため、件数だけを記録しValidation側で許容判定の材料にする。
		s.Ledger.RecordUnknownListing()
		addErr(ctx, step, ErrApplication, err)
		return
	}
	if code != 201 {
		// 確定的な4xx(400/401等): 未コミットが確定している(bidのRejectに相当)。
		// RecordUnknownListingを増やすと、この走行全体でValidationの「想定外のauction」検知が
		// criticalからapplicationへ不必要に格下げされてしまうため、増やさない。
		addErr(ctx, step, ErrApplication,
			fmt.Errorf("POST /api/auctions: 予期しない status %d", code))
		return
	}
	step.AddScore(ScorePOSTAuction)
	if created.Status != "live" || created.StartingPrice != startingPrice {
		addErr(ctx, step, ErrCritical,
			fmt.Errorf("POST /api/auctions: 応答が不一致 (status=%q starting_price=%d, 期待: live/%d)",
				created.Status, created.StartingPrice, startingPrice))
		return
	}
	s.Ledger.RecordListing(Listing{
		AuctionID: created.ID, SellerID: user.ID, StartingPrice: created.StartingPrice,
	})
	s.Listings.Publish(created.ID)

	stats, err := c.GetStatsMe(ctx)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	// 出品直後なので、出品数も live 数も最低1件はあるはず。
	if stats.ListedCount < 1 || stats.LiveCount < 1 {
		addErr(ctx, step, ErrCritical,
			fmt.Errorf("GET /api/stats/me: 出品直後なのに listed_count=%d live_count=%d",
				stats.ListedCount, stats.LiveCount))
	}
}

// visitorIteration は「サイトを訪れた閲覧者」を1人ぶん演じる。
// ページロード(HTML と全アセットの取得・照合)を1回行い、そのあとログインせずに
// 一覧と詳細を1つずつ見る。
//
// ログインしないのは意図的である。GET /api/auctions は未ログインでも見られるので、
// この worker が bcrypt(コスト12)を毎回踏むと、測っているものが静的配信ではなく
// ログイン処理になってしまう。
//
// イテレーションごとに新しい Client を作る = 毎回キャッシュが空の新規訪問者である。
// 参加者がキャッシュヘッダを付けても初回訪問は必ず実配信になるので、
// 静的配信の負荷が走行から消えることはない。
func (s *Scenario) visitorIteration(ctx context.Context, step *isucandar.BenchmarkStep) {
	c, err := NewClient(s.Target)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	pl, err := c.GetPage(ctx)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	// VerifyAssets の失敗は ErrApplication として扱う(当初設計の ErrCritical からの
	// 意図的な変更。docs/phase4-notes.md の 4-D 節を参照)。
	// dev/nginx.conf は "/" を含む全パスを app へ proxy
	// しており、静的アセットの配信も app プロセスと運命を共にする。過負荷時の
	// 一過性の5xxが index.html や JS/CSS に出ても不思議はなく、これを
	// ErrCritical にすると一過性の1発が走行全体を即死させる(規約上の事故2と
	// 同型)。ScoreGETPage には liveness floor が課されている(bench/liveness.go)ため、
	// 恒常的に壊れたビルドはこの1箇所を甘くしても「ページロードが1回も
	// floorに届かない」形で LIVENESS: FAIL が別途捕まえる。
	if err := VerifyAssets(s.Assets, pl); err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	// ページロード1回につき1点。アセット1本ごとには加点しない(score.go のコメント参照)。
	step.AddScore(ScoreGETPage)

	// 入札者と同じく「終了が最も近い20件」= 1ページ目を見る。
	l, err := c.GetAuctions(ctx, AuctionListParams{Page: 1})
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(ScoreGETList)
	if err := ValidatePagedListShape(1, l); err != nil {
		addErr(ctx, step, ErrCritical, err)
		return
	}
	list := l.Auctions
	if len(list) == 0 {
		addErr(ctx, step, ErrCritical, fmt.Errorf("GET /api/auctions: 開催中オークションが0件"))
		return
	}
	if _, err := c.GetAuction(ctx, list[rand.Intn(len(list))].ID); err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(ScoreGETDetail)
}
