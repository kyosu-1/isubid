package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	body := `{
  "seed": 20260830,
  "scale": "small",
  "counts": {"users": 500, "auctions": 1075, "live_auctions": 50, "bids": 30000, "notifications": 1200},
  "sample_auction_ids": [13, 100],
  "auctions": [
    {"id": 13, "title": "テスト椅子", "category_id": 2, "seller_id": 21, "seller_name": "gen_user_00021",
     "starting_price": 1000, "current_price": 1500, "bid_count": 2, "status": "closed",
     "ends_at_offset": 0, "winner_id": 22, "winning_price": 1500},
    {"id": 100, "title": "テスト椅子2", "category_id": 1, "seller_id": 23, "seller_name": "gen_user_00023",
     "starting_price": 2000, "current_price": 2000, "bid_count": 0, "status": "live",
     "ends_at_offset": 42, "winner_id": null, "winning_price": null}
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	snap, err := LoadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Scale != "small" || snap.Seed != 20260830 {
		t.Errorf("scale/seed = %q/%d", snap.Scale, snap.Seed)
	}
	if snap.Counts.LiveAuctions != 50 {
		t.Errorf("counts.live_auctions = %d, want 50", snap.Counts.LiveAuctions)
	}
	if len(snap.SampleAuctionIDs) != 2 {
		t.Errorf("sample_auction_ids = %v", snap.SampleAuctionIDs)
	}

	a, ok := snap.ByID(13)
	if !ok {
		t.Fatal("id 13 が引けない")
	}
	if a.Title != "テスト椅子" || a.CurrentPrice != 1500 || a.BidCount != 2 {
		t.Errorf("auction 13 = %+v", a)
	}
	if a.WinnerID == nil || *a.WinnerID != 22 {
		t.Errorf("auction 13 winner_id = %v, want 22", a.WinnerID)
	}
	if b, _ := snap.ByID(100); b.WinnerID != nil {
		t.Errorf("auction 100 winner_id = %v, want nil", b.WinnerID)
	}
	if _, ok := snap.ByID(99999); ok {
		t.Error("存在しない id が引けてしまう")
	}
}

func TestLoadSnapshotMissingFile(t *testing.T) {
	if _, err := LoadSnapshot(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("存在しないファイルでエラーにならない")
	}
}
