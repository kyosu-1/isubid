package main

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/isucon/isucandar"
	"github.com/isucon/isucandar/failure"
)

// seedUserName はシードユーザー(1..20)のログイン名を返す。パスワードは全員 'password'。
func seedUserName() string {
	return fmt.Sprintf("seed_user_%02d", rand.Intn(20)+1)
}

// addErr は step.AddError の一元化ヘルパー。ctx が既にキャンセルされている場合は
// (Load終了/タイムアウトによる)ノイズなのでエラーとして記録しない。
func addErr(ctx context.Context, step *isucandar.BenchmarkStep, code failure.StringCode, err error) {
	if ctx.Err() != nil {
		return
	}
	step.AddError(failure.NewError(code, err))
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
	target := list[rand.Intn(len(list))]

	// 競り負け(400 too-low)たら現在価格を取り直して上乗せ。最大5回。
	for attempt := 0; attempt < 5; attempt++ {
		d, err := c.GetAuction(ctx, target.ID)
		if err != nil {
			addErr(ctx, step, ErrApplication, err)
			return
		}
		step.AddScore(ScoreGETDetail)
		amount := d.CurrentPrice + 100 + rand.Int63n(400)
		bid, code, err := c.PostBid(ctx, target.ID, amount)
		if err != nil {
			addErr(ctx, step, ErrApplication, err)
			return
		}
		switch code {
		case 201:
			if bid.UserID != user.ID || bid.Amount != amount {
				addErr(ctx, step, ErrCritical,
					fmt.Errorf("POST /auctions/%d/bids: 応答内容が不一致 (got user=%d amount=%d, want user=%d amount=%d)",
						target.ID, bid.UserID, bid.Amount, user.ID, amount))
				return
			}
			s.Ledger.Record(AcceptedBid{BidID: bid.ID, AuctionID: target.ID, UserID: user.ID, Amount: amount})
			step.AddScore(ScorePOSTBid)
			return
		case 400:
			continue // 競り負け。取り直して再入札
		default:
			addErr(ctx, step, ErrApplication,
				fmt.Errorf("POST /auctions/%d/bids: 予期しない status %d", target.ID, code))
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
	if err := ValidateBidsOrdered(d.Bids); err != nil {
		addErr(ctx, step, ErrCritical, fmt.Errorf("auction %d: %w", d.ID, err))
		return
	}
	if int64(len(d.Bids)) != d.BidCount {
		addErr(ctx, step, ErrCritical,
			fmt.Errorf("auction %d: bid_count %d と bids件数 %d が不一致", d.ID, d.BidCount, len(d.Bids)))
	}
}
