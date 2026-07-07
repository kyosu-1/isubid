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
		a.EndsAt = time.Date(2030, 1, 1, int(id), 0, 0, 0, time.UTC)
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

func TestValidateInitialAuctionListWrongOrder(t *testing.T) {
	list := seedList()
	list[0], list[1] = list[1], list[0]
	err := ValidateInitialAuctionList(list)
	if err == nil {
		t.Error("want order error, got nil")
	}
}

func TestValidateInitialAuctionListNotLive(t *testing.T) {
	list := seedList()
	list[3].Status = "closed"
	err := ValidateInitialAuctionList(list)
	if err == nil || !strings.Contains(err.Error(), "status") {
		t.Errorf("want status error, got %v", err)
	}
}

func TestValidateInitialAuctionListWrongSeller(t *testing.T) {
	list := seedList()
	list[0].Seller.Name = "hacker"
	err := ValidateInitialAuctionList(list)
	if err == nil || !strings.Contains(err.Error(), "seller") {
		t.Errorf("want seller error, got %v", err)
	}
}

func seedDetail() *AuctionDetail {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	return &AuctionDetail{
		AuctionSummary: AuctionSummary{ID: 1, Title: "ヘリテージ・ウィングチェア", CurrentPrice: 1500, BidCount: 3, Status: "live"},
		Description:    "英国アンティークの本革ウィングチェア",
		StartingPrice:  1000,
		Bids: []Bid{
			{ID: 3, User: User{ID: 4, Name: "seed_user_04"}, Amount: 1500, CreatedAt: t0.Add(2 * time.Hour)},
			{ID: 2, User: User{ID: 3, Name: "seed_user_03"}, Amount: 1200, CreatedAt: t0.Add(time.Hour)},
			{ID: 1, User: User{ID: 2, Name: "seed_user_02"}, Amount: 1000, CreatedAt: t0},
		},
	}
}

func TestValidateInitialAuctionDetailOK(t *testing.T) {
	if err := ValidateInitialAuctionDetail(seedDetail()); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestValidateInitialAuctionDetailWrongDescription(t *testing.T) {
	d := seedDetail()
	d.Description = "changed"
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want description error, got nil")
	}
}

func TestValidateInitialAuctionDetailWrongBidOrder(t *testing.T) {
	d := seedDetail()
	d.Bids[0], d.Bids[2] = d.Bids[2], d.Bids[0] // ASC順に崩す
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want bid order error, got nil")
	}
}

func TestValidateBidsOrdered(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	ok := []Bid{
		{ID: 5, CreatedAt: t0.Add(time.Hour)},
		{ID: 4, CreatedAt: t0},
		{ID: 2, CreatedAt: t0}, // 同時刻はid降順
	}
	if err := ValidateBidsOrdered(ok); err != nil {
		t.Errorf("want nil, got %v", err)
	}
	ng := []Bid{
		{ID: 4, CreatedAt: t0},
		{ID: 5, CreatedAt: t0}, // 同時刻でid昇順は違反
	}
	if err := ValidateBidsOrdered(ng); err == nil {
		t.Error("want order error, got nil")
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

func TestValidateBidReflectedWrongContent(t *testing.T) {
	d := &AuctionDetail{
		AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1600},
		Bids:           []Bid{{ID: 100, User: User{ID: 5}, Amount: 1600}},
	}
	// 金額不一致
	if err := ValidateBidReflected(d, &BidCreated{ID: 100, UserID: 5, Amount: 1700, AuctionID: 1}); err == nil {
		t.Error("want mismatch error, got nil")
	}
	// current_price が入札額より小さい
	d2 := &AuctionDetail{
		AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1500},
		Bids:           []Bid{{ID: 100, User: User{ID: 5}, Amount: 1600}},
	}
	if err := ValidateBidReflected(d2, &BidCreated{ID: 100, UserID: 5, Amount: 1600, AuctionID: 1}); err == nil {
		t.Error("want current_price error, got nil")
	}
}
