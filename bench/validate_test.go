package main

import (
	"strings"
	"testing"
	"time"
)

func seedList() []AuctionSummary {
	base := AuctionSummary{
		CategoryID: 1,
		StartsAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndsAt:     time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		Status:     "live",
	}
	mk := func(id int64, title string, price, count int64, seller int64) AuctionSummary {
		a := base
		a.ID = id
		a.Title = title
		a.CurrentPrice = price
		a.BidCount = count
		a.Seller = User{ID: seller, Name: "seed_user_" + pad2(seller)}
		return a
	}
	return []AuctionSummary{
		mk(1, "ヘリテージ・ウィングチェア", 1500, 3, 1),
		mk(2, "エルゴホスト Model E", 2100, 1, 2),
		mk(3, "ISUレーサー GT", 3100, 1, 3),
		mk(4, "メッシュフロー 40", 4100, 1, 4),
		mk(5, "ミッドセンチュリー・ラウンジ", 2500, 0, 5),
		mk(6, "ネオンストライク Z", 3000, 0, 6),
		mk(7, "スタンドフレックス", 3500, 0, 7),
		mk(8, "チャーチチェア 1920", 4000, 0, 8),
		mk(9, "プロシート・エディション", 4500, 0, 9),
		mk(10, "コンパクトワーク 01", 5000, 0, 10),
	}
}

func TestValidateInitialAuctionListOK(t *testing.T) {
	if err := ValidateInitialAuctionList(seedList()); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestValidateInitialAuctionListWrongPrice(t *testing.T) {
	list := seedList()
	list[0].CurrentPrice = 9999
	err := ValidateInitialAuctionList(list)
	if err == nil || !strings.Contains(err.Error(), "current_price") {
		t.Errorf("want current_price error, got %v", err)
	}
}

func TestValidateInitialAuctionListWrongCount(t *testing.T) {
	err := ValidateInitialAuctionList(seedList()[:9])
	if err == nil {
		t.Error("want error for missing auction, got nil")
	}
}

func TestValidateBidReflected(t *testing.T) {
	d := &AuctionDetail{
		AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1600},
		Bids: []Bid{
			{ID: 100, User: User{ID: 5}, Amount: 1600},
			{ID: 3, User: User{ID: 4}, Amount: 1500},
		},
	}
	bid := &BidCreated{ID: 100, AuctionID: 1, UserID: 5, Amount: 1600}
	if err := ValidateBidReflected(d, bid); err != nil {
		t.Errorf("want nil, got %v", err)
	}

	missing := &BidCreated{ID: 999, AuctionID: 1, UserID: 5, Amount: 1700}
	if err := ValidateBidReflected(d, missing); err == nil {
		t.Error("want error for missing bid, got nil")
	}
}
