package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// SnapshotCounts は生成データの総件数。
type SnapshotCounts struct {
	Users         int64 `json:"users"`
	Auctions      int64 `json:"auctions"`
	LiveAuctions  int64 `json:"live_auctions"`
	Bids          int64 `json:"bids"`
	Notifications int64 `json:"notifications"`
}

// SnapshotUser は生成ユーザーの正解値。initial-data/snapshot.go の同名型と対になる。
// IconSHA256 が空文字ならアイコン未設定(GET /users/:id/icon は 404 を返すべき)。
type SnapshotUser struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	IconSHA256 string `json:"icon_sha256"`
}

// SnapshotAuction は初期データの正解値。initial-data/snapshot.go の同名型と対になる。
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
	// EndsAtOffset は初期化時刻からの秒数。live/upcoming のみ。closed は 0。
	EndsAtOffset int    `json:"ends_at_offset"`
	WinnerID     *int64 `json:"winner_id"`
	WinningPrice *int64 `json:"winning_price"`
}

// Snapshot は initial-data ジェネレータが出力した正解スナップショット。
// 全オークションは載っていない(全 live + 全 upcoming + サンプル対象の closed のみ)。
type Snapshot struct {
	Seed             int64             `json:"seed"`
	Scale            string            `json:"scale"`
	Counts           SnapshotCounts    `json:"counts"`
	SampleAuctionIDs []int64           `json:"sample_auction_ids"`
	Users            []SnapshotUser    `json:"users"`
	Auctions         []SnapshotAuction `json:"auctions"`

	byID     map[int64]*SnapshotAuction
	userByID map[int64]*SnapshotUser
}

func LoadSnapshot(path string) (*Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("スナップショットの読み込みに失敗: %w", err)
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("スナップショットのJSONが不正 (%s): %w", path, err)
	}
	s.byID = make(map[int64]*SnapshotAuction, len(s.Auctions))
	for i := range s.Auctions {
		s.byID[s.Auctions[i].ID] = &s.Auctions[i]
	}
	s.userByID = make(map[int64]*SnapshotUser, len(s.Users))
	for i := range s.Users {
		s.userByID[s.Users[i].ID] = &s.Users[i]
	}
	return &s, nil
}

func (s *Snapshot) ByID(id int64) (*SnapshotAuction, bool) {
	a, ok := s.byID[id]
	return a, ok
}

// UserByID は生成ユーザーの正解値を返す。シードユーザーやベンチが走行中に
// 作ったユーザーは載っていないので、ok が false になる。
func (s *Snapshot) UserByID(id int64) (*SnapshotUser, bool) {
	u, ok := s.userByID[id]
	return u, ok
}
