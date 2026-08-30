package main

import "time"

type User struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type AuctionSummary struct {
	ID           int64     `json:"id"`
	Title        string    `json:"title"`
	CategoryID   int64     `json:"category_id"`
	Seller       User      `json:"seller"`
	CurrentPrice int64     `json:"current_price"`
	BidCount     int64     `json:"bid_count"`
	StartsAt     time.Time `json:"starts_at"`
	EndsAt       time.Time `json:"ends_at"`
	Status       string    `json:"status"`
}

type Bid struct {
	ID        int64     `json:"id"`
	User      User      `json:"user"`
	Amount    int64     `json:"amount"`
	CreatedAt time.Time `json:"created_at"`
}

type AuctionDetail struct {
	AuctionSummary
	Description   string `json:"description"`
	StartingPrice int64  `json:"starting_price"`
	WinnerID      *int64 `json:"winner_id"`
	WinningPrice  *int64 `json:"winning_price"`
	Bids          []Bid  `json:"bids"`
}

type BidCreated struct {
	ID        int64     `json:"id"`
	AuctionID int64     `json:"auction_id"`
	UserID    int64     `json:"user_id"`
	Amount    int64     `json:"amount"`
	CreatedAt time.Time `json:"created_at"`
}

type AuctionCreated struct {
	ID            int64     `json:"id"`
	Title         string    `json:"title"`
	StartingPrice int64     `json:"starting_price"`
	EndsAt        time.Time `json:"ends_at"`
	Status        string    `json:"status"`
}

type Stats struct {
	ListedCount int64 `json:"listed_count"`
	SoldCount   int64 `json:"sold_count"`
	TotalSales  int64 `json:"total_sales"`
	LiveCount   int64 `json:"live_count"`
}

// AuctionList は GET /auctions のレスポンス。
type AuctionList struct {
	Auctions   []AuctionSummary `json:"auctions"`
	TotalCount int64            `json:"total_count"`
	HasNext    bool             `json:"has_next"`
}

type Notification struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	AuctionID int64     `json:"auction_id"`
	Message   string    `json:"message"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
}
