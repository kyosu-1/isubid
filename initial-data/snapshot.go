package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Counts は生成データの総件数。Prepare の健全性確認に使う。
type Counts struct {
	Users         int64 `json:"users"`
	Auctions      int64 `json:"auctions"`
	LiveAuctions  int64 `json:"live_auctions"`
	Bids          int64 `json:"bids"`
	Notifications int64 `json:"notifications"`
}

// SnapshotAuction は Prepare が照合するオークションの正解値。
type SnapshotAuction struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	CategoryID    int64  `json:"category_id"`
	SellerID      int64  `json:"seller_id"`
	SellerName    string `json:"seller_name"`
	StartingPrice int64  `json:"starting_price"`
	CurrentPrice  int64  `json:"current_price"`
	BidCount      int64  `json:"bid_count"`
	Status        string `json:"status"`
	// EndsAtOffset は generatedEpoch からの秒数。live/upcoming のみ。closed は 0。
	// POST /initialize がこのオフセットを現在時刻へ付け替える。
	EndsAtOffset int    `json:"ends_at_offset"`
	WinnerID     *int64 `json:"winner_id"`
	WinningPrice *int64 `json:"winning_price"`
}

// SnapshotUser は生成ユーザーの正解値。
// IconSHA256 が空文字ならアイコン未設定(GET /users/:id/icon は 404 を返すべき)。
type SnapshotUser struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	IconSHA256 string `json:"icon_sha256"`
}

// Snapshot は初期データの正解スナップショット。bench/snapshot.go が読む側として対になる。
//
// 全オークションは載せない。Prepare が照合するのは一覧1ページ目・代表サンプル・
// 総件数の3つだけなので、全 live + 全 upcoming + サンプル対象の closed で足りる。
// 1万件を載せると JSON が数MBになり、ベンチの起動が重くなるだけで得るものがない。
type Snapshot struct {
	Seed             int64             `json:"seed"`
	Scale            string            `json:"scale"`
	Counts           Counts            `json:"counts"`
	SampleAuctionIDs []int64           `json:"sample_auction_ids"`
	Users            []SnapshotUser    `json:"users"`
	Auctions         []SnapshotAuction `json:"auctions"`
}

// closedSampleCount はスナップショットに載せる closed オークションの件数。
const closedSampleCount = 30

func BuildSnapshot(ds *Dataset) *Snapshot {
	bidCount := map[int64]int64{}
	maxAmount := map[int64]int64{}
	for _, b := range ds.Bids {
		bidCount[b.AuctionID]++
		if b.Amount > maxAmount[b.AuctionID] {
			maxAmount[b.AuctionID] = b.Amount
		}
	}
	userName := make(map[int64]string, len(ds.Users))
	for _, u := range ds.Users {
		userName[u.ID] = u.Name
	}

	toSnapshot := func(a Auction) SnapshotAuction {
		price := a.StartingPrice
		if m := maxAmount[a.ID]; m > price {
			price = m
		}
		off := 0
		if a.Status != "closed" {
			off = int(a.EndsAt.Sub(generatedEpoch).Seconds())
		}
		return SnapshotAuction{
			ID: a.ID, Title: a.Title, Description: a.Description, CategoryID: a.CategoryID,
			SellerID: a.SellerID, SellerName: userName[a.SellerID],
			StartingPrice: a.StartingPrice, CurrentPrice: price,
			BidCount: bidCount[a.ID], Status: a.Status,
			EndsAtOffset: off, WinnerID: a.WinnerID, WinningPrice: a.WinningPrice,
		}
	}

	snap := &Snapshot{Seed: ds.Config.Seed, Scale: ds.Config.Name}

	snap.Users = make([]SnapshotUser, 0, len(ds.Users))
	for _, u := range ds.Users {
		su := SnapshotUser{ID: u.ID, Name: u.Name}
		if u.Icon != nil {
			su.IconSHA256 = fmt.Sprintf("%x", sha256.Sum256(u.Icon))
		}
		snap.Users = append(snap.Users, su)
	}

	var closed []Auction
	for _, a := range ds.Auctions {
		switch a.Status {
		case "live", "upcoming":
			snap.Auctions = append(snap.Auctions, toSnapshot(a))
		case "closed":
			closed = append(closed, a)
		}
		snap.Counts.Auctions++
		if a.Status == "live" {
			snap.Counts.LiveAuctions++
		}
	}
	snap.Counts.Users = int64(len(ds.Users))
	snap.Counts.Bids = int64(len(ds.Bids))
	snap.Counts.Notifications = int64(len(ds.Notifications))

	// closed は等間隔にサンプリングする(決定的で、id 範囲全体に散る)
	if len(closed) > 0 {
		n := closedSampleCount
		if n > len(closed) {
			n = len(closed)
		}
		step := len(closed) / n
		if step < 1 {
			step = 1
		}
		for i := 0; i < len(closed) && len(snap.SampleAuctionIDs) < n; i += step {
			sa := toSnapshot(closed[i])
			snap.Auctions = append(snap.Auctions, sa)
			snap.SampleAuctionIDs = append(snap.SampleAuctionIDs, sa.ID)
		}
	}
	// live からも代表を数件サンプルに加える
	for _, sa := range snap.Auctions {
		if sa.Status == "live" && len(snap.SampleAuctionIDs) < closedSampleCount+10 {
			snap.SampleAuctionIDs = append(snap.SampleAuctionIDs, sa.ID)
		}
	}
	sort.Slice(snap.Auctions, func(i, j int) bool { return snap.Auctions[i].ID < snap.Auctions[j].ID })
	sort.Slice(snap.SampleAuctionIDs, func(i, j int) bool { return snap.SampleAuctionIDs[i] < snap.SampleAuctionIDs[j] })
	return snap
}

func WriteSnapshot(dir string, snap *Snapshot) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "snapshot.json"), append(b, '\n'), 0o644)
}
