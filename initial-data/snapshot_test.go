package main

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// スナップショットは全オークションを載せない。Prepare が照合するのは
// 一覧1ページ目・代表サンプル・総件数の3つだけなので、
// 全 live + 全 upcoming + サンプル対象の closed のみで足りる。
func TestBuildSnapshotContents(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)
	snap := BuildSnapshot(ds)

	if snap.Scale != cfg.Name || snap.Seed != cfg.Seed {
		t.Errorf("scale/seed = %q/%d, want %q/%d", snap.Scale, snap.Seed, cfg.Name, cfg.Seed)
	}
	if snap.Counts.Auctions != int64(len(ds.Auctions)) ||
		snap.Counts.Bids != int64(len(ds.Bids)) ||
		snap.Counts.Users != int64(len(ds.Users)) {
		t.Errorf("counts = %+v", snap.Counts)
	}
	if snap.Counts.LiveAuctions != int64(cfg.LiveAuctions) {
		t.Errorf("counts.live_auctions = %d, want %d", snap.Counts.LiveAuctions, cfg.LiveAuctions)
	}

	byID := map[int64]SnapshotAuction{}
	statuses := map[string]int{}
	for _, a := range snap.Auctions {
		byID[a.ID] = a
		statuses[a.Status]++
	}
	if statuses["live"] != cfg.LiveAuctions {
		t.Errorf("snapshot の live = %d件, want %d (全件載せる)", statuses["live"], cfg.LiveAuctions)
	}
	if statuses["upcoming"] != cfg.UpcomingAuctions {
		t.Errorf("snapshot の upcoming = %d件, want %d (全件載せる)", statuses["upcoming"], cfg.UpcomingAuctions)
	}
	if statuses["closed"] == 0 || statuses["closed"] >= cfg.ClosedAuctions {
		t.Errorf("snapshot の closed = %d件 (期待: 1件以上、かつ全件(%d)未満のサンプル)", statuses["closed"], cfg.ClosedAuctions)
	}
	if len(snap.SampleAuctionIDs) == 0 {
		t.Fatal("sample_auction_ids が空")
	}
	for _, id := range snap.SampleAuctionIDs {
		if _, ok := byID[id]; !ok {
			t.Fatalf("sample_auction_ids の %d が snapshot.auctions に無い", id)
		}
	}
}

// スナップショットの current_price / bid_count が、生成した bids から
// 導かれる値と一致すること。別ロジックで作られていないことの確認。
func TestSnapshotMatchesGeneratedBids(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)
	snap := BuildSnapshot(ds)

	count := map[int64]int64{}
	max := map[int64]int64{}
	for _, b := range ds.Bids {
		count[b.AuctionID]++
		if b.Amount > max[b.AuctionID] {
			max[b.AuctionID] = b.Amount
		}
	}
	start := map[int64]int64{}
	for _, a := range ds.Auctions {
		start[a.ID] = a.StartingPrice
	}

	for _, sa := range snap.Auctions {
		if sa.BidCount != count[sa.ID] {
			t.Fatalf("auction %d: snapshot bid_count = %d, 生成bidsからは %d", sa.ID, sa.BidCount, count[sa.ID])
		}
		want := start[sa.ID]
		if m := max[sa.ID]; m > want {
			want = m
		}
		if sa.CurrentPrice != want {
			t.Fatalf("auction %d: snapshot current_price = %d, 生成bidsからは %d", sa.ID, sa.CurrentPrice, want)
		}
	}
}

func TestSnapshotCarriesDescription(t *testing.T) {
	ds := Generate(Scales["small"])
	snap := BuildSnapshot(ds)

	descByID := map[int64]string{}
	for _, a := range ds.Auctions {
		descByID[a.ID] = a.Description
	}
	for _, sa := range snap.Auctions {
		want := descByID[sa.ID]
		if want == "" {
			t.Fatalf("auction %d: 生成データ側の description が空", sa.ID)
		}
		if sa.Description != want {
			t.Errorf("auction %d: snapshot の description が %q (期待: %q)", sa.ID, sa.Description, want)
		}
	}
}

// live/upcoming は generatedEpoch からのオフセットを持ち、closed は 0。
func TestSnapshotEndsAtOffsets(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)
	snap := BuildSnapshot(ds)

	for _, sa := range snap.Auctions {
		switch sa.Status {
		case "closed":
			if sa.EndsAtOffset != 0 {
				t.Errorf("closed auction %d の ends_at_offset = %d, want 0", sa.ID, sa.EndsAtOffset)
			}
		default:
			if sa.EndsAtOffset <= 0 {
				t.Errorf("%s auction %d の ends_at_offset = %d, want 正の値", sa.Status, sa.ID, sa.EndsAtOffset)
			}
		}
	}
}

// TestSnapshotCarriesUserIcons は生成ユーザーのアイコン sha256 が
// スナップショットに載ることを固定する。
func TestSnapshotCarriesUserIcons(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)
	snap := BuildSnapshot(ds)

	if len(snap.Users) != len(ds.Users) {
		t.Fatalf("snapshot.Users が %d件, want %d件", len(snap.Users), len(ds.Users))
	}
	byID := map[int64]SnapshotUser{}
	for _, su := range snap.Users {
		byID[su.ID] = su
	}
	withIcon, without := 0, 0
	for _, u := range ds.Users {
		su, ok := byID[u.ID]
		if !ok {
			t.Fatalf("user %d が snapshot に無い", u.ID)
		}
		if su.Name != u.Name {
			t.Errorf("user %d: name が %q (期待: %q)", u.ID, su.Name, u.Name)
		}
		if u.Icon == nil {
			without++
			if su.IconSHA256 != "" {
				t.Errorf("user %d: アイコン未設定なのに icon_sha256 が %q", u.ID, su.IconSHA256)
			}
			continue
		}
		withIcon++
		want := fmt.Sprintf("%x", sha256.Sum256(u.Icon))
		if su.IconSHA256 != want {
			t.Errorf("user %d: icon_sha256 が %q (期待: %q)", u.ID, su.IconSHA256, want)
		}
	}
	if withIcon == 0 || without == 0 {
		t.Errorf("アイコンあり %d件 / なし %d件 — どちらも1件以上あること", withIcon, without)
	}
}
