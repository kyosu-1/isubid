package main

import (
	"sort"
	"strings"
	"testing"
	"time"
)

func seedList() []AuctionSummary {
	// Independent hand-typed literals to catch drift in expectedInitialAuctions.
	// Built in ends_at order (initialAuctionOrder) to match real app behavior.
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	return []AuctionSummary{
		// ID 4: ends_at offset 12s
		{
			ID:           4,
			Title:        "メッシュフロー 40",
			CategoryID:   1,
			CurrentPrice: 4100,
			BidCount:     1,
			Seller:       User{ID: 4, Name: "seed_user_04"},
			EndsAt:       base.Add(12 * time.Second),
			Status:       "live",
			StartsAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		// ID 2: ends_at offset 20s
		{
			ID:           2,
			Title:        "エルゴホスト Model E",
			CategoryID:   1,
			CurrentPrice: 2100,
			BidCount:     1,
			Seller:       User{ID: 2, Name: "seed_user_02"},
			EndsAt:       base.Add(20 * time.Second),
			Status:       "live",
			StartsAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		// ID 8: ends_at offset 28s
		{
			ID:           8,
			Title:        "チャーチチェア 1920",
			CategoryID:   3,
			CurrentPrice: 4000,
			BidCount:     0,
			Seller:       User{ID: 8, Name: "seed_user_08"},
			EndsAt:       base.Add(28 * time.Second),
			Status:       "live",
			StartsAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		// ID 6: ends_at offset 36s
		{
			ID:           6,
			Title:        "ネオンストライク Z",
			CategoryID:   2,
			CurrentPrice: 3000,
			BidCount:     0,
			Seller:       User{ID: 6, Name: "seed_user_06"},
			EndsAt:       base.Add(36 * time.Second),
			Status:       "live",
			StartsAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		// ID 10: ends_at offset 44s
		{
			ID:           10,
			Title:        "コンパクトワーク 01",
			CategoryID:   1,
			CurrentPrice: 5000,
			BidCount:     0,
			Seller:       User{ID: 10, Name: "seed_user_10"},
			EndsAt:       base.Add(44 * time.Second),
			Status:       "live",
			StartsAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		// ID 1: ends_at offset 3600s
		{
			ID:           1,
			Title:        "ヘリテージ・ウィングチェア",
			CategoryID:   3,
			CurrentPrice: 1500,
			BidCount:     3,
			Seller:       User{ID: 1, Name: "seed_user_01"},
			EndsAt:       base.Add(3600 * time.Second),
			Status:       "live",
			StartsAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		// ID 3: ends_at offset 3660s
		{
			ID:           3,
			Title:        "ISUレーサー GT",
			CategoryID:   2,
			CurrentPrice: 3100,
			BidCount:     1,
			Seller:       User{ID: 3, Name: "seed_user_03"},
			EndsAt:       base.Add(3660 * time.Second),
			Status:       "live",
			StartsAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		// ID 5: ends_at offset 3720s
		{
			ID:           5,
			Title:        "ミッドセンチュリー・ラウンジ",
			CategoryID:   3,
			CurrentPrice: 2500,
			BidCount:     0,
			Seller:       User{ID: 5, Name: "seed_user_05"},
			EndsAt:       base.Add(3720 * time.Second),
			Status:       "live",
			StartsAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		// ID 7: ends_at offset 3780s
		{
			ID:           7,
			Title:        "スタンドフレックス",
			CategoryID:   1,
			CurrentPrice: 3500,
			BidCount:     0,
			Seller:       User{ID: 7, Name: "seed_user_07"},
			EndsAt:       base.Add(3780 * time.Second),
			Status:       "live",
			StartsAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		// ID 9: ends_at offset 3840s
		{
			ID:           9,
			Title:        "プロシート・エディション",
			CategoryID:   2,
			CurrentPrice: 4500,
			BidCount:     0,
			Seller:       User{ID: 9, Name: "seed_user_09"},
			EndsAt:       base.Add(3840 * time.Second),
			Status:       "live",
			StartsAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}
}

func TestValidateInitialAuctionListOK(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	if err := ValidateInitialAuctionList(seedList(), base); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestValidateInitialAuctionListWrongPrice(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	list := seedList()
	list[0].CurrentPrice = 9999
	err := ValidateInitialAuctionList(list, base)
	if err == nil || !strings.Contains(err.Error(), "current_price") {
		t.Errorf("want current_price error, got %v", err)
	}
}

func TestValidateInitialAuctionListWrongCount(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	err := ValidateInitialAuctionList(seedList()[:9], base)
	if err == nil {
		t.Error("want error for missing auction, got nil")
	}
}

func TestValidateInitialAuctionListWrongOrder(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	list := seedList()
	list[0], list[1] = list[1], list[0]
	err := ValidateInitialAuctionList(list, base)
	if err == nil {
		t.Error("want order error, got nil")
	}
}

func TestValidateInitialAuctionListWrongEndsAtOrder(t *testing.T) {
	// IDs are in ends_at order, but one entry's EndsAt is earlier than predecessor
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	list := seedList()
	list[4].EndsAt = list[3].EndsAt.Add(-time.Hour)
	err := ValidateInitialAuctionList(list, base)
	if err == nil || !strings.Contains(err.Error(), "ends_at") {
		t.Errorf("want ends_at error, got %v", err)
	}
}

func TestValidateInitialAuctionListNotLive(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	list := seedList()
	list[3].Status = "closed"
	err := ValidateInitialAuctionList(list, base)
	if err == nil || !strings.Contains(err.Error(), "status") {
		t.Errorf("want status error, got %v", err)
	}
}

func TestValidateInitialAuctionListWrongSeller(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	list := seedList()
	list[0].Seller.Name = "hacker"
	err := ValidateInitialAuctionList(list, base)
	if err == nil || !strings.Contains(err.Error(), "seller") {
		t.Errorf("want seller error, got %v", err)
	}
}

func TestValidateInitialAuctionListWrongCategory(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	list := seedList()
	list[0].CategoryID = 99
	err := ValidateInitialAuctionList(list, base)
	if err == nil || !strings.Contains(err.Error(), "category_id") {
		t.Errorf("want category_id error, got %v", err)
	}
}

func TestValidateInitialAuctionListWrongEndsAtValue(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	list := seedList()
	// ends_at の相対値が期待とずれている(全体を+1日シフト)
	for i := range list {
		list[i].EndsAt = list[i].EndsAt.Add(24 * time.Hour)
	}
	err := ValidateInitialAuctionList(list, base)
	if err == nil || !strings.Contains(err.Error(), "ends_at") {
		t.Errorf("want ends_at value error, got %v", err)
	}
}

func seedDetail() *AuctionDetail {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	return &AuctionDetail{
		AuctionSummary: AuctionSummary{ID: 1, Title: "ヘリテージ・ウィングチェア", CategoryID: 3, CurrentPrice: 1500, BidCount: 3, Status: "live"},
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

func TestValidateInitialAuctionDetailWrongID(t *testing.T) {
	d := seedDetail()
	d.ID = 2
	if err := ValidateInitialAuctionDetail(d); err == nil || !strings.Contains(err.Error(), "id") {
		t.Errorf("want id error, got %v", err)
	}
}

func TestValidateInitialAuctionDetailWrongDescription(t *testing.T) {
	d := seedDetail()
	d.Description = "changed"
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want description error, got nil")
	}
}

func TestValidateInitialAuctionDetailWrongStartingPrice(t *testing.T) {
	d := seedDetail()
	d.StartingPrice = 999
	if err := ValidateInitialAuctionDetail(d); err == nil || !strings.Contains(err.Error(), "starting_price") {
		t.Errorf("want starting_price error, got %v", err)
	}
}

func TestValidateInitialAuctionDetailWrongCurrentPrice(t *testing.T) {
	d := seedDetail()
	d.CurrentPrice = 1234
	if err := ValidateInitialAuctionDetail(d); err == nil || !strings.Contains(err.Error(), "current_price") {
		t.Errorf("want current_price error, got %v", err)
	}
}

func TestValidateInitialAuctionDetailTruncatedBids(t *testing.T) {
	d := seedDetail()
	d.Bids = d.Bids[:2] // Remove one bid
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want bid count error, got nil")
	}
}

func TestValidateInitialAuctionDetailWrongTitle(t *testing.T) {
	d := seedDetail()
	d.Title = "changed"
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want title error, got nil")
	}
}

func TestValidateInitialAuctionDetailWrongStatus(t *testing.T) {
	d := seedDetail()
	d.Status = "closed"
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want status error, got nil")
	}
}

func TestValidateInitialAuctionDetailWrongBidCount(t *testing.T) {
	d := seedDetail()
	d.BidCount = 99 // フィールド単体の不一致(Bids配列は正しいまま)
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want bid_count error, got nil")
	}
}

func TestValidateInitialAuctionDetailWrongCategory(t *testing.T) {
	d := seedDetail()
	d.CategoryID = 99
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want category error, got nil")
	}
}

func TestValidateInitialAuctionDetailWrongBidOrder(t *testing.T) {
	d := seedDetail()
	d.Bids[0], d.Bids[2] = d.Bids[2], d.Bids[0] // ASC順に崩す
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want bid order error, got nil")
	}
}

func TestValidateInitialAuctionDetailWrongBidCreatedAtOrder(t *testing.T) {
	// Bids match position-for-position in Amount/User but CreatedAt is out of order
	// This exercises the ValidateBidsOrdered delegation at end of ValidateInitialAuctionDetail
	d := seedDetail()
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// Make bids[1].CreatedAt later than bids[0], violating DESC order
	d.Bids[1].CreatedAt = t0.Add(3 * time.Hour)
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want bid order error from ValidateBidsOrdered, got nil")
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

func TestValidateBidAmountsMonotonicOK(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	bids := []Bid{
		{ID: 3, Amount: 1500, CreatedAt: t0.Add(2 * time.Hour)},
		{ID: 2, Amount: 1200, CreatedAt: t0.Add(time.Hour)},
		{ID: 1, Amount: 1000, CreatedAt: t0},
	}
	if err := ValidateBidAmountsMonotonic(bids); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestValidateBidAmountsMonotonicEqualFails(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// FOR UPDATEが外れて2件が同額で受理されたケース(非増加=違反)。
	bids := []Bid{
		{ID: 6, Amount: 1200, CreatedAt: t0.Add(2 * time.Hour)},
		{ID: 5, Amount: 1200, CreatedAt: t0.Add(time.Hour)},
	}
	err := ValidateBidAmountsMonotonic(bids)
	if err == nil || !strings.Contains(err.Error(), "単調増加違反") {
		t.Errorf("want 単調増加違反 error, got %v", err)
	}
}

func TestValidateBidAmountsMonotonicIncreasingInDescFails(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// DESC順で並んでいるはずなのに金額が増加している(逆転)ケース。
	bids := []Bid{
		{ID: 6, Amount: 1000, CreatedAt: t0.Add(2 * time.Hour)},
		{ID: 5, Amount: 1200, CreatedAt: t0.Add(time.Hour)},
	}
	err := ValidateBidAmountsMonotonic(bids)
	if err == nil || !strings.Contains(err.Error(), "単調増加違反") {
		t.Errorf("want 単調増加違反 error, got %v", err)
	}
}

func TestValidateBidAmountsMonotonicEmptyOrSingleOK(t *testing.T) {
	if err := ValidateBidAmountsMonotonic(nil); err != nil {
		t.Errorf("want nil for empty, got %v", err)
	}
	if err := ValidateBidAmountsMonotonic([]Bid{{ID: 1, Amount: 1000}}); err != nil {
		t.Errorf("want nil for single bid, got %v", err)
	}
}

func TestValidateBidsInvariantDelegatesBoth(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	ok := []Bid{
		{ID: 3, Amount: 1500, CreatedAt: t0.Add(2 * time.Hour)},
		{ID: 2, Amount: 1200, CreatedAt: t0.Add(time.Hour)},
		{ID: 1, Amount: 1000, CreatedAt: t0},
	}
	if err := ValidateBidsInvariant(ok); err != nil {
		t.Errorf("want nil, got %v", err)
	}

	// created_at順は正しいが金額が非減少(FOR UPDATE除去の典型的な症状)。
	monotonicViolation := []Bid{
		{ID: 3, Amount: 1000, CreatedAt: t0.Add(2 * time.Hour)},
		{ID: 2, Amount: 1200, CreatedAt: t0.Add(time.Hour)},
		{ID: 1, Amount: 1000, CreatedAt: t0},
	}
	err := ValidateBidsInvariant(monotonicViolation)
	if err == nil || !strings.Contains(err.Error(), "単調増加違反") {
		t.Errorf("want 単調増加違反 error, got %v", err)
	}

	// 順序自体が壊れているケースはValidateBidsOrderedが先に検知する。
	orderViolation := []Bid{
		{ID: 1, Amount: 1000, CreatedAt: t0},
		{ID: 2, Amount: 1200, CreatedAt: t0.Add(time.Hour)},
	}
	if err := ValidateBidsInvariant(orderViolation); err == nil {
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

// ends_at は「initialize 応答受信時刻 + オフセット」± 許容幅で照合する。
func TestValidateInitialAuctionListRelativeEndsAt(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	build := func(mutate func(list []AuctionSummary)) []AuctionSummary {
		list := make([]AuctionSummary, 0, len(initialAuctionOrder))
		for _, id := range initialAuctionOrder {
			w := expectedInitialAuctions[id]
			list = append(list, AuctionSummary{
				ID:           id,
				Title:        w.Title,
				CategoryID:   w.CategoryID,
				Seller:       User{ID: w.SellerID, Name: "seed_user_" + pad2(w.SellerID)},
				CurrentPrice: w.CurrentPrice,
				BidCount:     w.BidCount,
				EndsAt:       base.Add(time.Duration(w.EndsAtOffset) * time.Second),
				Status:       "live",
			})
		}
		if mutate != nil {
			mutate(list)
		}
		return list
	}

	if err := ValidateInitialAuctionList(build(nil), base); err != nil {
		t.Fatalf("正しい一覧が拒否された: %v", err)
	}

	// 許容幅の内側(3秒ずれ)は通る
	if err := ValidateInitialAuctionList(build(func(l []AuctionSummary) {
		l[0].EndsAt = l[0].EndsAt.Add(3 * time.Second)
	}), base); err != nil {
		t.Errorf("許容幅内のずれが拒否された: %v", err)
	}

	// 許容幅の外側(30秒ずれ)は落ちる
	if err := ValidateInitialAuctionList(build(func(l []AuctionSummary) {
		l[0].EndsAt = l[0].EndsAt.Add(30 * time.Second)
	}), base); err == nil {
		t.Error("許容幅外のずれが検出されなかった")
	}

	// id 昇順に並べ替えたもの(= ORDER BY id ASC 相当)は落ちる
	if err := ValidateInitialAuctionList(build(func(l []AuctionSummary) {
		sort.Slice(l, func(i, j int) bool { return l[i].ID < l[j].ID })
	}), base); err == nil {
		t.Error("ORDER BY id ASC 相当の並びが検出されなかった")
	}
}

// ends_at を過ぎたオークションは closed になっていなければならない
// (終了処理バッチが止まっていることの検出)。
func TestValidateAuctionClosedIfDue(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	grace := 5 * time.Second

	// 期限を大きく過ぎているのに live → 違反
	overdue := &AuctionDetail{AuctionSummary: AuctionSummary{
		ID: 1, Status: "live", EndsAt: now.Add(-30 * time.Second)}}
	if err := ValidateAuctionClosedIfDue(overdue, now, grace); err == nil {
		t.Error("期限切れ live が検出されなかった")
	}

	// 期限直後(猶予の内側)は許容
	justEnded := &AuctionDetail{AuctionSummary: AuctionSummary{
		ID: 1, Status: "live", EndsAt: now.Add(-2 * time.Second)}}
	if err := ValidateAuctionClosedIfDue(justEnded, now, grace); err != nil {
		t.Errorf("猶予内の live が拒否された: %v", err)
	}

	// 期限前の live は当然OK
	future := &AuctionDetail{AuctionSummary: AuctionSummary{
		ID: 1, Status: "live", EndsAt: now.Add(time.Hour)}}
	if err := ValidateAuctionClosedIfDue(future, now, grace); err != nil {
		t.Errorf("期限前の live が拒否された: %v", err)
	}

	// closed ならいつでもOK
	closed := &AuctionDetail{AuctionSummary: AuctionSummary{
		ID: 1, Status: "closed", EndsAt: now.Add(-30 * time.Second)}}
	if err := ValidateAuctionClosedIfDue(closed, now, grace); err != nil {
		t.Errorf("closed が拒否された: %v", err)
	}
}
