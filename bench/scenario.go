package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/isucon/isucandar"
	"github.com/isucon/isucandar/failure"
	"github.com/isucon/isucandar/pubsub"
	"github.com/isucon/isucandar/worker"
)

// Scenario はISUBIDベンチのシナリオ。
type Scenario struct {
	Target      string
	PrepareOnly bool
	Bidders     int
	Watchers    int
	Notifiers   int
	Sellers     int
	Listings    *pubsub.PubSub
	Board       *listingBoard
	Ledger      *Ledger
	Snapshot    *Snapshot
}

// newListingPubSub は出品配信用の PubSub を作る。
// pubsub.Publish は購読チャネルが満杯だとブロックし、その状態で購読側が
// ctx キャンセルで閉じようとすると相互にロック待ちになりうる。
// 購読ハンドラは即座に返る実装だが、念のため十分な容量を確保しておく。
func newListingPubSub() *pubsub.PubSub {
	ps := pubsub.NewPubSub()
	ps.Capacity = 1000
	return ps
}

func randomName(prefix string) string {
	b := make([]byte, 4)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// Load は入札者(bidderIteration)・ウォッチャー(watcherIteration)・
// 通知閲覧者(notifierIteration)・出品者(sellerIteration)の4種の worker を
// 無限ループで並行実行し、ctx(WithLoadTimeout)がキャンセルされるまで走らせる。
// (isucandarのLoadは削除するとParallel実行系の前提が崩れるため、no-opでも定義必須)
func (s *Scenario) Load(ctx context.Context, step *isucandar.BenchmarkStep) error {
	if s.PrepareOnly {
		return nil
	}
	// 購読は worker 起動前に張る。ハンドラはスライス追記だけで即座に返るため
	// Publish 側がブロックしない。Capacity にも十分な余裕を持たせておく。
	s.Board = &listingBoard{}
	s.Listings.Subscribe(ctx, func(v interface{}) {
		if id, ok := v.(int64); ok {
			s.Board.add(id)
		}
	})
	bidder, err := worker.NewWorker(func(ctx context.Context, _ int) {
		s.bidderIteration(ctx, step)
	}, worker.WithInfinityLoop(), worker.WithMaxParallelism(int32(s.Bidders)))
	if err != nil {
		return err
	}
	watcher, err := worker.NewWorker(func(ctx context.Context, _ int) {
		s.watcherIteration(ctx, step)
	}, worker.WithInfinityLoop(), worker.WithMaxParallelism(int32(s.Watchers)))
	if err != nil {
		return err
	}
	notifier, err := worker.NewWorker(func(ctx context.Context, _ int) {
		s.notifierIteration(ctx, step)
	}, worker.WithInfinityLoop(), worker.WithMaxParallelism(int32(s.Notifiers)))
	if err != nil {
		return err
	}
	seller, err := worker.NewWorker(func(ctx context.Context, _ int) {
		s.sellerIteration(ctx, step)
	}, worker.WithInfinityLoop(), worker.WithMaxParallelism(int32(s.Sellers)))
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); bidder.Process(ctx) }()
	go func() { defer wg.Done(); watcher.Process(ctx) }()
	go func() { defer wg.Done(); notifier.Process(ctx) }()
	go func() { defer wg.Done(); seller.Process(ctx) }()
	wg.Wait()
	return nil
}

// Validation はLoad終了後に台帳と実データを突合する。
// ベンチ以外に入札者はいないため、各オークションの期待状態は理論上
// 「走行開始前から存在する入札(preexistingMaxBidID以下) ∪ 台帳が確定受理した入札 ∪
// 結果不明のまま残ったpending」で決まる(reconcileAuction参照)。201の応答を受け取れない
// ままctxキャンセル/転送エラーになった入札は、サーバー側では既にコミットされている
// 可能性があるため、pendingとして突き合わせに使うことでfalse-FAILを避ける(C1)。
//
// 想定していないauctionへの記録は、通常は即critical。ただし出品(POST /auctions)の応答が
// 受け取れず結果不明の出品(unknownListings > 0)が1件でもある走行に限り、その出品が
// Listingを作れず「想定外」と誤検知されている可能性があるため、この走行全体で
// application へ格下げする(詳細は unknownListings を使っている箇所のコメント参照)。
// 検査対象は id<=10 の初期シードauction全件(Loadでベンチが一度も触れなかったauctionでも、
// 既存入札が消えていないかは検証したいため)に加え、ベンチが Load 中に出品した
// listing(s.Ledger.Listings())全件、さらに(生成データ搭載時は)台帳が実際に入札を
// 持っている生成auctionも同様に突合する。生成auctionは一覧の大半を占めるため、
// スナップショットのauction一覧をknownに含めないと入札のたびに「想定外のauction」
// criticalになってしまう。
func (s *Scenario) Validation(ctx context.Context, step *isucandar.BenchmarkStep) error {
	if s.PrepareOnly {
		return nil
	}
	c, err := NewClient(s.Target)
	if err != nil {
		return err
	}

	acceptedByAuction := s.Ledger.ByAuction()
	pendingByAuction := s.Ledger.PendingByAuction()
	listings := s.Ledger.Listings()

	// ベンチが知っているオークション = 初期データ(シード ∪ 生成) ∪ ベンチが出品したもの
	known := map[int64]bool{}
	for id := range expectedInitialAuctions {
		known[id] = true
	}
	if s.Snapshot != nil {
		for _, a := range s.Snapshot.Auctions {
			known[a.ID] = true
		}
	}
	for _, li := range listings {
		known[li.AuctionID] = true
	}
	// unknownListings > 0 の場合、POST /auctions の応答を受け取れなかった出品が存在する。
	// サーバー側では既にコミットされている可能性があり(in-flight commit)、その出品は
	// Listing を作れず known に含められない。そのため「想定外のauction」検知が
	// false-FAIL になりうるので、この走行に限り critical から減点(application)へ
	// 落とす。件数だけの簡易な突合(個体特定はしない)であるトレードオフとして、
	// このケースの走行は既にPOST /auctionsのapplicationエラーを1件以上抱えているため、
	// オペレーターは両方のシグナルを見ることになる。
	unknownListings := s.Ledger.UnknownListings()
	phantomAuctionCode := ErrCritical
	if unknownListings > 0 {
		phantomAuctionCode = ErrApplication
	}
	for auctionID := range acceptedByAuction {
		if !known[auctionID] {
			msg := fmt.Errorf("想定外のauctionに入札が受理された (auction %d)", auctionID)
			if unknownListings > 0 {
				msg = fmt.Errorf("%w (結果不明の出品が%d件あるため減点扱い)", msg, unknownListings)
			}
			step.AddError(failure.NewError(phantomAuctionCode, msg))
		}
	}
	for auctionID := range pendingByAuction {
		if !known[auctionID] {
			msg := fmt.Errorf("想定外のauctionに未確定入札(pending)が存在 (auction %d)", auctionID)
			if unknownListings > 0 {
				msg = fmt.Errorf("%w (結果不明の出品が%d件あるため減点扱い)", msg, unknownListings)
			}
			step.AddError(failure.NewError(phantomAuctionCode, msg))
		}
	}

	winners := map[int64]int64{} // auctionID -> winnerID
	for auctionID, want := range expectedInitialAuctions {
		// M1: GetAuctionは一過性エラーの影響を減らすため軽いbackoff付きで最大3回試行する。
		d, err := c.GetAuctionRetry(ctx, auctionID, 3, 100*time.Millisecond)
		if err != nil {
			step.AddError(failure.NewError(ErrCritical, fmt.Errorf("auction %d: %w", auctionID, err)))
			continue
		}
		for _, e := range reconcileAuction(auctionID, d, want.BidCount, want.CurrentPrice, seedMaxBidID,
			acceptedByAuction[auctionID], pendingByAuction[auctionID]) {
			step.AddError(failure.NewError(ErrCritical, e))
		}
		for _, e := range reconcileClosedAuction(auctionID, d) {
			step.AddError(failure.NewError(ErrCritical, e))
		}
		if err := ValidateAuctionClosedIfDue(d, time.Now().UTC(), closeGrace); err != nil {
			step.AddError(failure.NewError(ErrCritical, err))
		}
		if d.Status == "closed" && d.WinnerID != nil {
			winners[auctionID] = *d.WinnerID
		}
	}
	for _, li := range listings {
		d, err := c.GetAuctionRetry(ctx, li.AuctionID, 3, 100*time.Millisecond)
		if err != nil {
			step.AddError(failure.NewError(ErrCritical, fmt.Errorf("auction %d: %w", li.AuctionID, err)))
			continue
		}
		for _, e := range reconcileAuction(li.AuctionID, d, 0, li.StartingPrice, seedMaxBidID,
			acceptedByAuction[li.AuctionID], pendingByAuction[li.AuctionID]) {
			step.AddError(failure.NewError(ErrCritical, e))
		}
		for _, e := range reconcileClosedAuction(li.AuctionID, d) {
			step.AddError(failure.NewError(ErrCritical, e))
		}
		// listingCloseGrace を使う(closeGraceではない): 理由は同定数のコメント参照。
		if err := ValidateAuctionClosedIfDue(d, time.Now().UTC(), listingCloseGrace); err != nil {
			step.AddError(failure.NewError(ErrCritical, err))
		}
		if d.Status == "closed" && d.WinnerID != nil {
			winners[li.AuctionID] = *d.WinnerID
		}
	}
	// 生成auction(id>=13)のうち、台帳が実際に入札(accepted/pending)を持つものだけを
	// 突合する。触れていない生成auctionまで全件フェッチすると(小規模でも数十〜数百件)
	// シグナルの無いHTTPが増えるだけなので、対象は台帳に載っているidに限定する。
	// ベースラインはスナップショットのBidCount/CurrentPrice(走行開始前の状態)。これは
	// expectedInitialAuctionsがシードauctionに、Listing.StartingPriceがベンチ出品に
	// 対して果たすのと同じ役割。
	//
	// bid idは全auction共通の連番なので、走行開始前から存在する入札の境界はauction単位
	// ではなく、生成bid件数(snapshot.Counts.Bids)から一意に決まる: 生成bidは
	// SeedMaxBidID+1から連番で採番されるため、境界は seedMaxBidID + Counts.Bids
	// (initial-data/generate.go 参照)。
	if s.Snapshot != nil {
		preexistingMaxBidID := seedMaxBidID + s.Snapshot.Counts.Bids
		touchedGenerated := map[int64]bool{}
		for auctionID := range acceptedByAuction {
			touchedGenerated[auctionID] = true
		}
		for auctionID := range pendingByAuction {
			touchedGenerated[auctionID] = true
		}
		for id := range expectedInitialAuctions {
			delete(touchedGenerated, id)
		}
		for _, li := range listings {
			delete(touchedGenerated, li.AuctionID)
		}
		for auctionID := range touchedGenerated {
			sa, ok := s.Snapshot.ByID(auctionID)
			if !ok {
				// known(スナップショットのauction一覧)にも無いidはここまで来ず、
				// 上の「想定外のauction」検知が既に拾っている。
				continue
			}
			d, err := c.GetAuctionRetry(ctx, auctionID, 3, 100*time.Millisecond)
			if err != nil {
				step.AddError(failure.NewError(ErrCritical, fmt.Errorf("auction %d: %w", auctionID, err)))
				continue
			}
			for _, e := range reconcileAuction(auctionID, d, sa.BidCount, sa.CurrentPrice, preexistingMaxBidID,
				acceptedByAuction[auctionID], pendingByAuction[auctionID]) {
				step.AddError(failure.NewError(ErrCritical, e))
			}
		}
	}
	s.validateNotifications(ctx, step, acceptedByAuction, winners)
	return nil
}

// notifyExpectation は1ユーザーぶんの通知期待値。
type notifyExpectation struct {
	MinOutbid   int64
	WonAuctions []int64
}

// validateNotifications は通知の欠落を照合する。
//   - 各入札ユーザーの outbid 通知数が台帳から導いた下限を下回らないこと
//   - 落札者に該当オークションの won 通知が届いていること
//   - 一度も入札していない新規ユーザーの通知が0件であること(他人宛の混入検出)
//
// ベンチの入札者はシードユーザーのみなので、user id から seed_user_%02d でログイン名を
// 逆引きできる(Global Constraints 参照)。
func (s *Scenario) validateNotifications(ctx context.Context, step *isucandar.BenchmarkStep,
	acceptedByAuction map[int64][]AcceptedBid, winners map[int64]int64) {

	want := map[int64]*notifyExpectation{}
	for uid, n := range ExpectedOutbidCounts(acceptedByAuction) {
		want[uid] = &notifyExpectation{MinOutbid: n}
	}
	for auctionID, winnerID := range winners {
		e, ok := want[winnerID]
		if !ok {
			e = &notifyExpectation{}
			want[winnerID] = e
		}
		e.WonAuctions = append(e.WonAuctions, auctionID)
	}

	for uid, e := range want {
		if uid < 1 || uid > 20 {
			// シードユーザー以外はログイン名を逆引きできないため検証対象外
			continue
		}
		uc, err := NewClient(s.Target)
		if err != nil {
			step.AddError(failure.NewError(ErrApplication, err))
			continue
		}
		if _, err := uc.Login(ctx, fmt.Sprintf("seed_user_%02d", uid), "password"); err != nil {
			step.AddError(failure.NewError(ErrApplication, err))
			continue
		}
		ns, err := uc.GetNotifications(ctx)
		if err != nil {
			step.AddError(failure.NewError(ErrApplication, err))
			continue
		}
		if err := ValidateNotificationsOrdered(ns); err != nil {
			step.AddError(failure.NewError(ErrCritical, err))
		}
		if got := CountByType(ns, "outbid"); got < e.MinOutbid {
			step.AddError(failure.NewError(ErrCritical,
				fmt.Errorf("user %d: outbid通知が %d件 (期待: %d件以上、欠落の疑い)", uid, got, e.MinOutbid)))
		}
		for _, auctionID := range e.WonAuctions {
			if !HasWonNotification(ns, auctionID) {
				step.AddError(failure.NewError(ErrCritical,
					fmt.Errorf("user %d: auction %d を落札したのに won通知が無い", uid, auctionID)))
			}
		}
	}

	// 一度も入札していない新規ユーザーの通知は0件でなければならない。
	// (user_id で絞らず全件返す実装を検出する)
	fresh, err := NewClient(s.Target)
	if err != nil {
		step.AddError(failure.NewError(ErrApplication, err))
		return
	}
	if _, err := fresh.Register(ctx, randomName("bench_notify_"), "benchpassword"); err != nil {
		step.AddError(failure.NewError(ErrApplication, err))
		return
	}
	ns, err := fresh.GetNotifications(ctx)
	if err != nil {
		step.AddError(failure.NewError(ErrApplication, err))
		return
	}
	if len(ns) != 0 {
		step.AddError(failure.NewError(ErrCritical,
			fmt.Errorf("入札していない新規ユーザーに通知が %d件 (期待: 0件、他人宛の混入)", len(ns))))
	}
}

func (s *Scenario) Prepare(ctx context.Context, step *isucandar.BenchmarkStep) error {
	c, err := NewClient(s.Target)
	if err != nil {
		return err
	}

	// 1. initialize
	lang, err := c.Initialize(ctx)
	if err != nil {
		return err
	}
	// アプリが基準時刻を採ったのは応答を受け取る直前。ここを base とし、
	// 初期化処理の所要時間ぶんのずれは endsAtTolerance が吸収する。
	base := time.Now().UTC()
	if lang == "" {
		return fmt.Errorf("POST /initialize: lang が空")
	}

	// 2. 初期データの検証
	list, err := c.GetAuctions(ctx)
	if err != nil {
		return err
	}
	if s.Snapshot != nil {
		if err := ValidateAuctionListWithSnapshot(list, s.Snapshot, base); err != nil {
			return err
		}
		// 代表サンプルの詳細を照合する(全件は Prepare の時間予算に収まらない)
		for _, id := range s.Snapshot.SampleAuctionIDs {
			sa, ok := s.Snapshot.ByID(id)
			if !ok {
				return fmt.Errorf("スナップショットの sample_auction_ids に載っている %d が auctions に無い", id)
			}
			d, err := c.GetAuction(ctx, id)
			if err != nil {
				return err
			}
			if err := ValidateSnapshotAuctionDetail(d, sa, base); err != nil {
				return err
			}
		}
	} else {
		if err := ValidateInitialAuctionList(list, base); err != nil {
			return err
		}
	}

	// 2b. シード詳細の検証(入札で汚す前に照合する)
	initialDetail, err := c.GetAuction(ctx, 1)
	if err != nil {
		return err
	}
	if err := ValidateInitialAuctionDetail(initialDetail); err != nil {
		return err
	}

	// 2c. closed / upcoming の詳細検証
	closedDetail, err := c.GetAuction(ctx, 11)
	if err != nil {
		return err
	}
	if closedDetail.Status != "closed" || closedDetail.CurrentPrice != 12000 || len(closedDetail.Bids) != 2 {
		return fmt.Errorf("auction 11: closed詳細が不正 (status=%q current_price=%d bids=%d, 期待: closed/12000/2)",
			closedDetail.Status, closedDetail.CurrentPrice, len(closedDetail.Bids))
	}
	if err := ValidateBidsInvariant(closedDetail.Bids); err != nil {
		return err
	}
	upcomingDetail, err := c.GetAuction(ctx, 12)
	if err != nil {
		return err
	}
	if upcomingDetail.Status != "upcoming" || upcomingDetail.CurrentPrice != 8000 || len(upcomingDetail.Bids) != 0 {
		return fmt.Errorf("auction 12: upcoming詳細が不正 (status=%q current_price=%d bids=%d, 期待: upcoming/8000/0)",
			upcomingDetail.Status, upcomingDetail.CurrentPrice, len(upcomingDetail.Bids))
	}

	// 3. 新規ユーザー登録と、シードユーザーのログイン
	name := randomName("bench_")
	if _, err := c.Register(ctx, name, "benchpassword"); err != nil {
		return err
	}
	seedClient, err := NewClient(s.Target)
	if err != nil {
		return err
	}
	seedUser, err := seedClient.Login(ctx, "seed_user_05", "password")
	if err != nil {
		return err
	}

	// 4. 入札の検証: 低すぎる入札は400、正しい入札は201で詳細に反映される
	const auctionID = 1
	detail, err := seedClient.GetAuction(ctx, auctionID)
	if err != nil {
		return err
	}
	lowCode, lowBody, err := seedClient.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/auctions/%d/bids", auctionID), map[string]int64{"amount": detail.CurrentPrice})
	if err != nil {
		return err
	}
	if lowCode != 400 {
		return fmt.Errorf("POST /auctions/%d/bids: 現在価格以下の入札が status %d (期待: 400)", auctionID, lowCode)
	}
	var rejection struct {
		Error        string `json:"error"`
		CurrentPrice int64  `json:"current_price"`
	}
	if err := json.Unmarshal(lowBody, &rejection); err != nil {
		return fmt.Errorf("POST /auctions/%d/bids: too-low応答のJSONが不正: %w", auctionID, err)
	}
	if rejection.Error == "" || rejection.CurrentPrice != detail.CurrentPrice {
		return fmt.Errorf("POST /auctions/%d/bids: too-low応答bodyが不正 (%+v, 期待: current_price=%d)",
			auctionID, rejection, detail.CurrentPrice)
	}
	bid, code, err := seedClient.PostBid(ctx, auctionID, detail.CurrentPrice+100)
	if err != nil {
		return err
	}
	if code != 201 {
		return fmt.Errorf("POST /auctions/%d/bids: status %d (期待: 201)", auctionID, code)
	}
	if bid.UserID != seedUser.ID {
		return fmt.Errorf("POST /auctions/%d/bids: user_id が %d (期待: %d)", auctionID, bid.UserID, seedUser.ID)
	}
	after, err := seedClient.GetAuction(ctx, auctionID)
	if err != nil {
		return err
	}
	if err := ValidateBidReflected(after, bid); err != nil {
		return err
	}
	if err := ValidateBidsInvariant(after.Bids); err != nil {
		return err
	}

	// 5. 未ログインの入札は401
	anon, err := NewClient(s.Target)
	if err != nil {
		return err
	}
	if _, code, err := anon.PostBid(ctx, auctionID, 999999); err != nil {
		return err
	} else if code != 401 {
		return fmt.Errorf("POST /auctions/%d/bids: 未ログイン入札が status %d (期待: 401)", auctionID, code)
	}

	// 6. not-live オークションへの入札は400
	for _, id := range []int64{11, 12} {
		if _, code, err := seedClient.PostBid(ctx, id, 999999); err != nil {
			return err
		} else if code != 400 {
			return fmt.Errorf("POST /auctions/%d/bids: not-liveへの入札が status %d (期待: 400)", id, code)
		}
	}

	// 後続の負荷走行に備えてデータを初期状態に戻す
	if _, err := c.Initialize(ctx); err != nil {
		return err
	}
	return nil
}
