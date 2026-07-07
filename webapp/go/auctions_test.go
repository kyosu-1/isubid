package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type auctionSummaryJSON struct {
	ID           int64     `json:"id"`
	Title        string    `json:"title"`
	CategoryID   int64     `json:"category_id"`
	Seller       userJSON  `json:"seller"`
	CurrentPrice int64     `json:"current_price"`
	BidCount     int64     `json:"bid_count"`
	StartsAt     time.Time `json:"starts_at"`
	EndsAt       time.Time `json:"ends_at"`
	Status       string    `json:"status"`
}

type userJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type bidJSON struct {
	ID        int64     `json:"id"`
	User      userJSON  `json:"user"`
	Amount    int64     `json:"amount"`
	CreatedAt time.Time `json:"created_at"`
}

type auctionDetailJSON struct {
	auctionSummaryJSON
	Description   string    `json:"description"`
	StartingPrice int64     `json:"starting_price"`
	Bids          []bidJSON `json:"bids"`
}

func TestGetAuctions(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var list []auctionSummaryJSON
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 10 {
		t.Fatalf("len = %d, want 10", len(list))
	}
	byID := map[int64]auctionSummaryJSON{}
	for _, a := range list {
		if a.Status != "live" {
			t.Errorf("auction %d status = %q, want live", a.ID, a.Status)
		}
		byID[a.ID] = a
	}
	a1 := byID[1]
	if a1.Title != "ヘリテージ・ウィングチェア" {
		t.Errorf("auction 1 title = %q", a1.Title)
	}
	if a1.CurrentPrice != 1500 {
		t.Errorf("auction 1 current_price = %d, want 1500", a1.CurrentPrice)
	}
	if a1.BidCount != 3 {
		t.Errorf("auction 1 bid_count = %d, want 3", a1.BidCount)
	}
	if a1.Seller.ID != 1 || a1.Seller.Name != "seed_user_01" {
		t.Errorf("auction 1 seller = %+v", a1.Seller)
	}
	if a5 := byID[5]; a5.CurrentPrice != 2500 || a5.BidCount != 0 {
		t.Errorf("auction 5 = %+v, want current_price 2500 / bid_count 0", a5)
	}
}

func TestGetAuctionDetail(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions/1")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var d auctionDetailJSON
	if err := json.NewDecoder(res.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.StartingPrice != 1000 {
		t.Errorf("starting_price = %d, want 1000", d.StartingPrice)
	}
	if len(d.Bids) != 3 {
		t.Fatalf("bids len = %d, want 3", len(d.Bids))
	}
	// created_at降順(新しい順)
	if d.Bids[0].Amount != 1500 || d.Bids[2].Amount != 1000 {
		t.Errorf("bids order unexpected: %+v", d.Bids)
	}
	if d.Bids[0].User.Name != "seed_user_04" {
		t.Errorf("top bid user = %+v", d.Bids[0].User)
	}
}

func TestGetAuctionNotFound(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions/99999")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}
