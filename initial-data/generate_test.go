package main

import (
	"sort"
	"testing"
	"time"
)

func TestGenerateUsers(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	if len(ds.Users) != cfg.Users {
		t.Fatalf("users = %d, want %d", len(ds.Users), cfg.Users)
	}
	// 生成ユーザーは必ずシードの次から採番する
	if ds.Users[0].ID != SeedMaxUserID+1 {
		t.Errorf("最初の user id = %d, want %d", ds.Users[0].ID, SeedMaxUserID+1)
	}
	// id は連番で、name は id から決まる
	for i, u := range ds.Users {
		wantID := int64(SeedMaxUserID + 1 + i)
		if u.ID != wantID {
			t.Fatalf("users[%d].ID = %d, want %d", i, u.ID, wantID)
		}
		if u.Name != "gen_user_"+pad5(u.ID) {
			t.Fatalf("users[%d].Name = %q, want %q", i, u.Name, "gen_user_"+pad5(u.ID))
		}
	}
}

// 同じ scale と seed なら生成結果は完全に一致する。
func TestGenerateIsDeterministic(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	a := Generate(cfg)
	b := Generate(cfg)

	if len(a.Users) != len(b.Users) {
		t.Fatalf("users 件数が不一致: %d vs %d", len(a.Users), len(b.Users))
	}
	for i := range a.Users {
		if a.Users[i] != b.Users[i] {
			t.Fatalf("users[%d] が不一致: %+v vs %+v", i, a.Users[i], b.Users[i])
		}
	}

	// Auctions も完全に一致する必要がある
	if len(a.Auctions) != len(b.Auctions) {
		t.Fatalf("auctions 件数が不一致: %d vs %d", len(a.Auctions), len(b.Auctions))
	}
	for i := range a.Auctions {
		if a.Auctions[i].ID != b.Auctions[i].ID ||
			a.Auctions[i].SellerID != b.Auctions[i].SellerID ||
			a.Auctions[i].CategoryID != b.Auctions[i].CategoryID ||
			a.Auctions[i].Title != b.Auctions[i].Title ||
			a.Auctions[i].Description != b.Auctions[i].Description ||
			a.Auctions[i].StartingPrice != b.Auctions[i].StartingPrice ||
			!a.Auctions[i].StartsAt.Equal(b.Auctions[i].StartsAt) ||
			!a.Auctions[i].EndsAt.Equal(b.Auctions[i].EndsAt) ||
			a.Auctions[i].Status != b.Auctions[i].Status {
			t.Fatalf("auctions[%d] が不一致: %+v vs %+v", i, a.Auctions[i], b.Auctions[i])
		}
		// WinnerID と WinningPrice はポインタなので値を比較する
		if (a.Auctions[i].WinnerID == nil) != (b.Auctions[i].WinnerID == nil) {
			t.Fatalf("auctions[%d].WinnerID が不一致: %v vs %v", i, a.Auctions[i].WinnerID, b.Auctions[i].WinnerID)
		}
		if a.Auctions[i].WinnerID != nil && *a.Auctions[i].WinnerID != *b.Auctions[i].WinnerID {
			t.Fatalf("auctions[%d].WinnerID が不一致: %d vs %d", i, *a.Auctions[i].WinnerID, *b.Auctions[i].WinnerID)
		}
		if (a.Auctions[i].WinningPrice == nil) != (b.Auctions[i].WinningPrice == nil) {
			t.Fatalf("auctions[%d].WinningPrice が不一致: %v vs %v", i, a.Auctions[i].WinningPrice, b.Auctions[i].WinningPrice)
		}
		if a.Auctions[i].WinningPrice != nil && *a.Auctions[i].WinningPrice != *b.Auctions[i].WinningPrice {
			t.Fatalf("auctions[%d].WinningPrice が不一致: %d vs %d", i, *a.Auctions[i].WinningPrice, *b.Auctions[i].WinningPrice)
		}
	}

	// Bids も完全に一致する必要がある
	if len(a.Bids) != len(b.Bids) {
		t.Fatalf("bids 件数が不一致: %d vs %d", len(a.Bids), len(b.Bids))
	}
	for i := range a.Bids {
		if a.Bids[i].ID != b.Bids[i].ID ||
			a.Bids[i].AuctionID != b.Bids[i].AuctionID ||
			a.Bids[i].UserID != b.Bids[i].UserID ||
			a.Bids[i].Amount != b.Bids[i].Amount ||
			!a.Bids[i].CreatedAt.Equal(b.Bids[i].CreatedAt) {
			t.Fatalf("bids[%d] が不一致: %+v vs %+v", i, a.Bids[i], b.Bids[i])
		}
	}

	// Notifications も完全に一致する必要がある
	if len(a.Notifications) != len(b.Notifications) {
		t.Fatalf("notifications 件数が不一致: %d vs %d", len(a.Notifications), len(b.Notifications))
	}
	for i := range a.Notifications {
		if a.Notifications[i].ID != b.Notifications[i].ID ||
			a.Notifications[i].UserID != b.Notifications[i].UserID ||
			a.Notifications[i].Type != b.Notifications[i].Type ||
			a.Notifications[i].AuctionID != b.Notifications[i].AuctionID ||
			a.Notifications[i].Message != b.Notifications[i].Message ||
			!a.Notifications[i].CreatedAt.Equal(b.Notifications[i].CreatedAt) {
			t.Fatalf("notifications[%d] が不一致: %+v vs %+v", i, a.Notifications[i], b.Notifications[i])
		}
	}
}

// live の ends_at 順が id 順と相関していると、一覧の ORDER BY ends_at ASC を
// ORDER BY id ASC に書き換える改変を検出できなくなる。Phase 3 が
// initialAuctionOrder で作った性質を、生成データでも保つ必要がある。
func TestGeneratedLiveEndsAtNotCorrelatedWithID(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	var live []Auction
	for _, a := range ds.Auctions {
		if a.Status == "live" {
			live = append(live, a)
		}
	}
	if len(live) != cfg.LiveAuctions {
		t.Fatalf("live = %d件, want %d", len(live), cfg.LiveAuctions)
	}
	// live は id 昇順で並んでいる前提。その並びで ends_at が昇順ソート済みなら相関している
	sorted := sort.SliceIsSorted(live, func(i, j int) bool {
		return live[i].EndsAt.Before(live[j].EndsAt)
	})
	if sorted {
		t.Error("live の ends_at が id 順と相関している(ORDER BY id ASC を検出できなくなる)")
	}
}

// ends_at のオフセットが重複すると一覧の順序が一意に決まらない。
func TestGeneratedLiveEndsAtAreUnique(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	seen := map[int64]bool{}
	for _, a := range ds.Auctions {
		if a.Status != "live" {
			continue
		}
		off := int64(a.EndsAt.Sub(generatedEpoch).Seconds())
		if seen[off] {
			t.Fatalf("ends_at オフセット %d が重複している", off)
		}
		seen[off] = true
	}
}

func TestGeneratedAuctionStatusesAndIDs(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	want := cfg.ClosedAuctions + cfg.LiveAuctions + cfg.UpcomingAuctions
	if len(ds.Auctions) != want {
		t.Fatalf("auctions = %d件, want %d", len(ds.Auctions), want)
	}
	if ds.Auctions[0].ID != SeedMaxAuctionID+1 {
		t.Errorf("最初の auction id = %d, want %d", ds.Auctions[0].ID, SeedMaxAuctionID+1)
	}

	counts := map[string]int{}
	sellerIDs := map[int64]bool{}
	for i, a := range ds.Auctions {
		if a.ID != int64(SeedMaxAuctionID+1+i) {
			t.Fatalf("auctions[%d].ID = %d, want %d", i, a.ID, SeedMaxAuctionID+1+i)
		}
		counts[a.Status]++
		sellerIDs[a.SellerID] = true
		if a.StartingPrice < 1 {
			t.Fatalf("auctions[%d].StartingPrice = %d", i, a.StartingPrice)
		}
		if a.CategoryID < 1 || a.CategoryID > 3 {
			t.Fatalf("auctions[%d].CategoryID = %d (シードのカテゴリは1〜3)", i, a.CategoryID)
		}
	}
	if counts["closed"] != cfg.ClosedAuctions || counts["live"] != cfg.LiveAuctions || counts["upcoming"] != cfg.UpcomingAuctions {
		t.Errorf("status 内訳 = %+v", counts)
	}
	// シードユーザーを出品者にすると GET /stats/me のシード期待値が壊れる
	for id := range sellerIDs {
		if id <= SeedMaxUserID {
			t.Errorf("シードユーザー %d が出品者になっている", id)
		}
	}
}

// closed は過去の絶対時刻。走行時刻に依存しないため initialize で書き換えない。
func TestGeneratedClosedAuctionsAreInThePast(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, a := range ds.Auctions {
		if a.Status != "closed" {
			continue
		}
		if !a.EndsAt.Before(cutoff) {
			t.Fatalf("closed auction %d の ends_at が %v (期待: %v より前)", a.ID, a.EndsAt, cutoff)
		}
	}
}

// generatedEpoch は必ず未来日付でなければならない。過去日付に「整地」すると、
// POST /initialize がダンプ投入から applyGeneratedSchedule 実行までの一瞬、
// 生成liveオークションの ends_at がエポック起点のオフセットそのままの値になり、
// runAuctionCloser(毎秒 status='live' AND ends_at<=NOW(6) を閉じるバッチ)がそれを
// 期限切れとみなして拾ってしまう。すると won 通知が auto-increment id で挿入され、
// notifications の採番カウンタが1を超えた後に 94_notifications.sql が id=1 から
// 明示挿入する際に Duplicate entry で衝突する(確率的な初期化失敗、60秒走行7回中
// 3回で発生した実測あり)。
func TestGeneratedEpochIsInTheFuture(t *testing.T) {
	if !generatedEpoch.After(time.Now()) {
		t.Fatalf("generatedEpoch = %v, want after now(過去日付に戻すとPOST /initializeでrunAuctionCloserとの競合が確率的に再発する)",
			generatedEpoch)
	}
}

// webapp/go/initialize.go の applyGeneratedSchedule は、生成bidのcreated_atが
// auctionと同じ TIMESTAMPDIFF(SECOND, generatedEpochLiteral, ...) でシフトされる
// ことに依存しており、それはさらに「生成liveオークションのどのbidのcreated_atも
// generatedEpochを超えない」という前提があって初めて正しく機能する。もしこの前提が
// 崩れてbidのcreated_atがgeneratedEpochを超えると、TIMESTAMPDIFFの符号が反転し、
// 他のbidと不整合なシフト量になって単調性検査(reconcileAuction経由の
// ValidateBidsInvariant)が再び壊れる。生成時点(現在は1 liveあたり最大42件)で
// bidsをauction数に対して増やすと(Scalesの Bids を LiveAuctions に対して相対的に
// 引き上げると)この余白は縮む。
func TestGeneratedLiveAuctionBidsEndBeforeEpoch(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	liveIDs := map[int64]bool{}
	for _, a := range ds.Auctions {
		if a.Status == "live" {
			liveIDs[a.ID] = true
		}
	}

	maxCreated := map[int64]time.Time{}
	for _, b := range ds.Bids {
		if !liveIDs[b.AuctionID] {
			continue
		}
		if cur, ok := maxCreated[b.AuctionID]; !ok || b.CreatedAt.After(cur) {
			maxCreated[b.AuctionID] = b.CreatedAt
		}
	}
	if len(maxCreated) == 0 {
		t.Fatal("生成liveオークションにbidが1件も無い(このテストが検証対象を持てていない)")
	}

	for auctionID, maxT := range maxCreated {
		if !maxT.Before(generatedEpoch) {
			t.Fatalf("live auction %d の最終bid created_at = %v, want before generatedEpoch(%v)",
				auctionID, maxT, generatedEpoch)
		}
	}
}

// ベンチの ValidateBidsInvariant / ValidateFeedPage は
// 「各オークション内で id 昇順に amount が厳密単調増加」を検証する。
// 生成データがこれを破ると Prepare とウォッチャーが即 critical を出す。
func TestGeneratedBidsAreMonotonicPerAuction(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	byAuction := map[int64][]Bid{}
	for _, b := range ds.Bids {
		byAuction[b.AuctionID] = append(byAuction[b.AuctionID], b)
	}
	for auctionID, bids := range byAuction {
		for i := 1; i < len(bids); i++ {
			prev, cur := bids[i-1], bids[i]
			if cur.ID <= prev.ID {
				t.Fatalf("auction %d: bid id が昇順でない (%d の次が %d)", auctionID, prev.ID, cur.ID)
			}
			if cur.Amount <= prev.Amount {
				t.Fatalf("auction %d: amount が厳密単調増加でない (id=%d amount=%d の次が id=%d amount=%d)",
					auctionID, prev.ID, prev.Amount, cur.ID, cur.Amount)
			}
			if !cur.CreatedAt.After(prev.CreatedAt) {
				t.Fatalf("auction %d: created_at が id と同順でない (id=%d %v の次が id=%d %v)",
					auctionID, prev.ID, prev.CreatedAt, cur.ID, cur.CreatedAt)
			}
			if cur.UserID == prev.UserID {
				t.Fatalf("auction %d: 同一ユーザーが連続して入札している (user=%d)", auctionID, cur.UserID)
			}
		}
	}
}

// ベンチの reconcileClosedAuction は winner = argmax(bids) を厳密に検証する。
func TestGeneratedClosedAuctionWinnersMatchMaxBid(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	maxBid := map[int64]Bid{}
	for _, b := range ds.Bids {
		if cur, ok := maxBid[b.AuctionID]; !ok || b.Amount > cur.Amount {
			maxBid[b.AuctionID] = b
		}
	}
	for _, a := range ds.Auctions {
		if a.Status != "closed" {
			if a.WinnerID != nil || a.WinningPrice != nil {
				t.Fatalf("auction %d (%s) に落札者が設定されている", a.ID, a.Status)
			}
			continue
		}
		top, hasBid := maxBid[a.ID]
		if !hasBid {
			if a.WinnerID != nil || a.WinningPrice != nil {
				t.Fatalf("auction %d: 入札0件なのに落札者がいる", a.ID)
			}
			continue
		}
		if a.WinnerID == nil || *a.WinnerID != top.UserID {
			t.Fatalf("auction %d: winner_id = %v, want %d", a.ID, a.WinnerID, top.UserID)
		}
		if a.WinningPrice == nil || *a.WinningPrice != top.Amount {
			t.Fatalf("auction %d: winning_price = %v, want %d", a.ID, a.WinningPrice, top.Amount)
		}
	}
}

func TestGeneratedBidsCountAndIDs(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	if len(ds.Bids) != cfg.Bids {
		t.Fatalf("bids = %d件, want %d", len(ds.Bids), cfg.Bids)
	}
	if ds.Bids[0].ID != SeedMaxBidID+1 {
		t.Errorf("最初の bid id = %d, want %d", ds.Bids[0].ID, SeedMaxBidID+1)
	}
	// upcoming には入札しない
	status := map[int64]string{}
	for _, a := range ds.Auctions {
		status[a.ID] = a.Status
	}
	for _, b := range ds.Bids {
		if status[b.AuctionID] == "upcoming" {
			t.Fatalf("upcoming の auction %d に入札がある", b.AuctionID)
		}
		if b.UserID <= SeedMaxUserID {
			t.Fatalf("bid %d の入札者がシードユーザー %d", b.ID, b.UserID)
		}
	}
}

// 通知は生成ユーザー宛のみ。シードユーザー宛を作ると
// GET /notifications の応答が初手から巨大になり、通知確認シナリオのコストが
// 実データではなく生成データに支配される。
func TestGeneratedNotificationsTargetGeneratedUsersOnly(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	if len(ds.Notifications) == 0 {
		t.Fatal("通知が1件も生成されていない(user_id フルスキャンに走査対象を与える目的が果たせない)")
	}
	for _, n := range ds.Notifications {
		if n.UserID <= SeedMaxUserID {
			t.Fatalf("シードユーザー %d 宛の通知が生成されている (notification %d)", n.UserID, n.ID)
		}
		if n.Type != "outbid" && n.Type != "won" {
			t.Fatalf("notification %d: type = %q", n.ID, n.Type)
		}
	}
}

// won 通知は「落札者が確定した closed オークション」にだけ、その落札者宛に1件。
func TestGeneratedWonNotificationsMatchWinners(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	wonBy := map[int64][]int64{} // auctionID -> userIDs
	for _, n := range ds.Notifications {
		if n.Type == "won" {
			wonBy[n.AuctionID] = append(wonBy[n.AuctionID], n.UserID)
		}
	}
	for _, a := range ds.Auctions {
		got := wonBy[a.ID]
		if a.Status != "closed" || a.WinnerID == nil {
			if len(got) != 0 {
				t.Fatalf("auction %d (status=%s winner=%v) に won 通知が %d件", a.ID, a.Status, a.WinnerID, len(got))
			}
			continue
		}
		if len(got) != 1 || got[0] != *a.WinnerID {
			t.Fatalf("auction %d: won 通知が %v (期待: [%d] の1件)", a.ID, got, *a.WinnerID)
		}
	}
}

// notifications.message は VARCHAR(255)。Phase 3 で、溢れると closeAuction の
// トランザクションがロールバックしオークションが永久に live のまま残る問題を修正した。
// 生成データも同じ制約を満たす必要がある。
func TestGeneratedNotificationMessagesFitColumn(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	for _, n := range ds.Notifications {
		if r := len([]rune(n.Message)); r > 255 {
			t.Fatalf("notification %d の message が %d文字 (VARCHAR(255) を超える)", n.ID, r)
		}
		if n.Message == "" {
			t.Fatalf("notification %d の message が空", n.ID)
		}
	}
}
