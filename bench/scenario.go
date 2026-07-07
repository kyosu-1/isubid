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
	"github.com/isucon/isucandar/worker"
)

// Scenario はISUBIDベンチのシナリオ。
type Scenario struct {
	Target      string
	PrepareOnly bool
	Bidders     int
	Watchers    int
	Ledger      *Ledger
}

func randomName(prefix string) string {
	b := make([]byte, 4)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// Load は入札者(bidderIteration)とウォッチャー(watcherIteration)の2種の
// worker を無限ループで並行実行し、ctx(WithLoadTimeout)がキャンセルされるまで走らせる。
// (isucandarのLoadは削除するとParallel実行系の前提が崩れるため、no-opでも定義必須)
func (s *Scenario) Load(ctx context.Context, step *isucandar.BenchmarkStep) error {
	if s.PrepareOnly {
		return nil
	}
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
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); bidder.Process(ctx) }()
	go func() { defer wg.Done(); watcher.Process(ctx) }()
	wg.Wait()
	return nil
}

// Validation はLoad終了後に台帳と実データを突合する。
// ベンチ以外に入札者はいないため、各オークションの期待状態は理論上
// 「シード入札(id<=8) ∪ 台帳が確定受理した入札 ∪ 結果不明のまま残ったpending」で決まる
// (reconcileAuction参照)。201の応答を受け取れないままctxキャンセル/転送エラーになった
// 入札は、サーバー側では既にコミットされている可能性があるため、pendingとして
// 突き合わせに使うことでfalse-FAILを避ける(C1)。
//
// 想定していないauctionへの記録(バグでもない限り起こらない)は即critical。
// 各auctionはid<=10全件を検査する(Loadでベンチが一度も触れなかったauctionでも、
// シード入札が消えていないかは検証したいため)。
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

	for auctionID := range acceptedByAuction {
		if _, ok := expectedInitialAuctions[auctionID]; !ok {
			step.AddError(failure.NewError(ErrCritical,
				fmt.Errorf("想定外のauctionに入札が受理された (auction %d)", auctionID)))
		}
	}
	for auctionID := range pendingByAuction {
		if _, ok := expectedInitialAuctions[auctionID]; !ok {
			step.AddError(failure.NewError(ErrCritical,
				fmt.Errorf("想定外のauctionに未確定入札(pending)が存在 (auction %d)", auctionID)))
		}
	}

	for auctionID, want := range expectedInitialAuctions {
		// M1: GetAuctionは一過性エラーの影響を減らすため軽いbackoff付きで最大3回試行する。
		d, err := c.GetAuctionRetry(ctx, auctionID, 3, 100*time.Millisecond)
		if err != nil {
			step.AddError(failure.NewError(ErrCritical, fmt.Errorf("auction %d: %w", auctionID, err)))
			continue
		}
		for _, e := range reconcileAuction(auctionID, d, want.BidCount, want.CurrentPrice,
			acceptedByAuction[auctionID], pendingByAuction[auctionID]) {
			step.AddError(failure.NewError(ErrCritical, e))
		}
	}
	return nil
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
	if lang == "" {
		return fmt.Errorf("POST /initialize: lang が空")
	}

	// 2. 初期データの検証
	list, err := c.GetAuctions(ctx)
	if err != nil {
		return err
	}
	if err := ValidateInitialAuctionList(list); err != nil {
		return err
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
