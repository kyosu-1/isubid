package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
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
	target := list[rand.Intn(len(list))]

	// 競り負け(400 too-low)たら現在価格を取り直して上乗せ。最大5回。
	for attempt := 0; attempt < 5; attempt++ {
		d, err := c.GetAuction(ctx, target.ID)
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
		intentID := s.Ledger.Intent(target.ID, user.ID, amount)
		bid, code, err := c.PostBid(ctx, target.ID, amount)
		if err != nil {
			// 結果不明(転送エラー/5xx/タイムアウト): pendingのまま残す。
			addErr(ctx, step, ErrApplication, err)
			return
		}
		switch code {
		case 201:
			// 201は確定的なコミット。台帳へ昇格させる(応答内容が期待とズレていても
			// 実際にコミットされた値で記録し、その上で内容不一致を別途criticalにする)。
			s.Ledger.Confirm(intentID, AcceptedBid{BidID: bid.ID, AuctionID: target.ID, UserID: bid.UserID, Amount: bid.Amount})
			if bid.UserID != user.ID || bid.Amount != amount {
				addErr(ctx, step, ErrCritical,
					fmt.Errorf("POST /auctions/%d/bids: 応答内容が不一致 (got user=%d amount=%d, want user=%d amount=%d)",
						target.ID, bid.UserID, bid.Amount, user.ID, amount))
				return
			}
			step.AddScore(ScorePOSTBid)
			s.awaitFeedReflection(ctx, step, c, target.ID, sinceID, bid.ID)
			return
		case 400:
			// 競り負け: 確定的に未コミット。取り直して再入札。
			s.Ledger.Reject(intentID)
			continue
		default:
			// その他の4xx(401/403/404等)も確定的に未コミットと判断してpendingを解消する。
			s.Ledger.Reject(intentID)
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
