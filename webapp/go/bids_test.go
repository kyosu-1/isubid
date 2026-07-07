package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func loginSeedUser(t *testing.T, tsURL string, n string) *http.Client {
	t.Helper()
	client := newClientWithJar(t)
	res := postJSON(t, client, tsURL+"/login", `{"name":"`+n+`","password":"password"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("seed login status = %d", res.StatusCode)
	}
	return client
}

func TestPostBid(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := loginSeedUser(t, ts.URL, "seed_user_05")

	// auction 1 の現在価格は1500(シード)
	res := postJSON(t, client, ts.URL+"/auctions/1/bids", `{"amount":1600}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", res.StatusCode)
	}
	var b struct {
		ID        int64     `json:"id"`
		AuctionID int64     `json:"auction_id"`
		UserID    int64     `json:"user_id"`
		Amount    int64     `json:"amount"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.NewDecoder(res.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	if b.AuctionID != 1 || b.UserID != 5 || b.Amount != 1600 || b.ID == 0 {
		t.Errorf("unexpected bid: %+v", b)
	}

	// 詳細に反映されている
	res2, err := http.Get(ts.URL + "/auctions/1")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	var d auctionDetailJSON
	if err := json.NewDecoder(res2.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.CurrentPrice != 1600 || d.BidCount != 4 {
		t.Errorf("current_price = %d / bid_count = %d, want 1600 / 4", d.CurrentPrice, d.BidCount)
	}
}

func TestPostBidTooLow(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := loginSeedUser(t, ts.URL, "seed_user_05")

	res := postJSON(t, client, ts.URL+"/auctions/1/bids", `{"amount":1500}`) // 現在価格と同額はNG
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
	var body struct {
		Error        string `json:"error"`
		CurrentPrice int64  `json:"current_price"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.CurrentPrice != 1500 {
		t.Errorf("current_price = %d, want 1500", body.CurrentPrice)
	}
}

func TestPostBidUnauthorized(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := newClientWithJar(t)

	res := postJSON(t, client, ts.URL+"/auctions/1/bids", `{"amount":9999}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
}

func TestPostBidAuctionNotFound(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := loginSeedUser(t, ts.URL, "seed_user_05")

	res := postJSON(t, client, ts.URL+"/auctions/99999/bids", `{"amount":9999}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}

func TestPostBidNotLive(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := loginSeedUser(t, ts.URL, "seed_user_05")

	// 11=closed, 12=upcoming — どちらも入札不可
	for _, id := range []string{"11", "12"} {
		res := postJSON(t, client, ts.URL+"/auctions/"+id+"/bids", `{"amount":999999}`)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("auction %s: status = %d, want 400", id, res.StatusCode)
		}
	}
}
