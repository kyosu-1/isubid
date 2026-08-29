package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
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
		step.AddScore(ScoreGETFeed)
		if err := ValidateFeedPage(feed, since); err != nil {
			addErr(ctx, step, ErrCritical, fmt.Errorf("auction %d: %w", auctionID, err))
			return
		}
		for _, b := range feed {
			if b.ID == bidID {
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
	list, err := c.GetAuctions(ctx)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(ScoreGETList)
	if len(list) == 0 {
		addErr(ctx, step, ErrCritical, fmt.Errorf("GET /auctions: 開催中オークションが0件"))
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
					fmt.Errorf("POST /auctions/%d/bids: 応答内容が不一致 (got user=%d amount=%d, want user=%d amount=%d)",
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
				fmt.Errorf("POST /auctions/%d/bids: 予期しない status %d", targetID, code))
			return
		}
	}
	// 5連敗は人気オークションなら起こりうる。エラーにしない。
}

// watcherIteration は「一覧→ランダム詳細+不変条件チェック」の回遊。
func (s *Scenario) watcherIteration(ctx context.Context, step *isucandar.BenchmarkStep) {
	c, err := NewClient(s.Target)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	list, err := c.GetAuctions(ctx)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(ScoreGETList)
	for _, a := range list {
		if a.Status != "live" {
			addErr(ctx, step, ErrCritical,
				fmt.Errorf("GET /auctions: live以外が混入 (id=%d status=%q)", a.ID, a.Status))
			return
		}
	}
	if len(list) == 0 {
		return
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

var sellerTitles = []string{
	"ラピッドチェア", "オークリーフ・スツール", "ミニマルワークシート",
	"ベルベット・オットマン", "スカンジ・ダイニング",
}

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
	created, err := c.PostAuction(ctx, title, "ベンチが出品した椅子",
		int64(1+rand.Intn(3)), startingPrice, duration)
	if err != nil {
		// 応答を受け取れなかっただけで、サーバー側では既にコミットされている可能性がある
		// (in-flight commit)。この場合ベンチ側はauction IDを知り得ずListingを作れないため、
		// Validationが「想定外のauction」と誤検知(false-FAIL)しうる。bidのIntent/Pending
		// (C1)と対称な仕組みは作れない(先行して仮IDを発番できない)ため、件数だけを記録し
		// Validation側で許容判定の材料にする。
		s.Ledger.RecordUnknownListing()
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(ScorePOSTAuction)
	if created.Status != "live" || created.StartingPrice != startingPrice {
		addErr(ctx, step, ErrCritical,
			fmt.Errorf("POST /auctions: 応答が不一致 (status=%q starting_price=%d, 期待: live/%d)",
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
			fmt.Errorf("GET /stats/me: 出品直後なのに listed_count=%d live_count=%d",
				stats.ListedCount, stats.LiveCount))
	}
}
