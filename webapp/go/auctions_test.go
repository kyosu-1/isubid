package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	WinnerID      *int64    `json:"winner_id"`
	WinningPrice  *int64    `json:"winning_price"`
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

func TestGetAuctionsOrderedByEndsAt(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var list []auctionSummaryJSON
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	// 初期化時にends_atが相対値へ書き換わり、ends_at昇順がid昇順と一致しないことが保証される。
	// これはORDER BY ends_at ASCをORDER BY id ASCに誤って書き換えるバグを検出するため。
	wantOrder := []int64{4, 2, 8, 6, 10, 1, 3, 5, 7, 9}
	for i, want := range wantOrder {
		if list[i].ID != want {
			t.Errorf("list[%d].ID = %d, want %d (ends_at ASC の期待順序)", i, list[i].ID, want)
		}
	}
	for i := 1; i < len(list); i++ {
		if list[i].EndsAt.Before(list[i-1].EndsAt) {
			t.Fatalf("list[%d].EndsAt %v < list[%d].EndsAt %v (ends_at order violation)", i, list[i].EndsAt, i-1, list[i-1].EndsAt)
		}
	}
}

func TestGetAuctionClosedDetail(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions/11")
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
	if d.Status != "closed" {
		t.Errorf("status = %q, want closed", d.Status)
	}
	if d.CurrentPrice != 12000 || d.BidCount != 2 {
		t.Errorf("current_price = %d / bid_count = %d, want 12000 / 2", d.CurrentPrice, d.BidCount)
	}
}

func TestGetAuctionInvalidID(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions/notanumber")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
}

func TestGetAuctionExposesWinner(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	// auction 11 は seed で closed / winner_id=12 / winning_price=12000
	var closed auctionDetailJSON
	getJSON(t, ts.URL+"/auctions/11", &closed)
	if closed.WinnerID == nil || *closed.WinnerID != 12 {
		t.Errorf("auction 11 winner_id = %v, want 12", closed.WinnerID)
	}
	if closed.WinningPrice == nil || *closed.WinningPrice != 12000 {
		t.Errorf("auction 11 winning_price = %v, want 12000", closed.WinningPrice)
	}

	// live のオークションは null
	var live auctionDetailJSON
	getJSON(t, ts.URL+"/auctions/1", &live)
	if live.WinnerID != nil || live.WinningPrice != nil {
		t.Errorf("auction 1 (live) winner = %v/%v, want null/null", live.WinnerID, live.WinningPrice)
	}
}

// getJSON は GET して JSON をデコードするテストヘルパー。
func getJSON(t *testing.T, url string, dest any) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", url, res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(dest); err != nil {
		t.Fatal(err)
	}
}

type auctionCreatedJSON struct {
	ID            int64     `json:"id"`
	Title         string    `json:"title"`
	StartingPrice int64     `json:"starting_price"`
	EndsAt        time.Time `json:"ends_at"`
	Status        string    `json:"status"`
}

func TestPostAuction(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	c := loginSeedUser(t, ts.URL, "seed_user_03")

	before := time.Now().UTC()
	res, err := c.Post(ts.URL+"/auctions", "application/json", strings.NewReader(
		`{"title":"テスト椅子","description":"説明","category_id":1,"starting_price":5000,"duration_seconds":30}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", res.StatusCode)
	}
	var created auctionCreatedJSON
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 {
		t.Error("id が 0")
	}
	if created.Status != "live" {
		t.Errorf("status = %q, want live", created.Status)
	}
	if created.StartingPrice != 5000 {
		t.Errorf("starting_price = %d, want 5000", created.StartingPrice)
	}
	// ends_at は now + 30秒 のはず
	lo, hi := before.Add(29*time.Second), time.Now().UTC().Add(31*time.Second)
	if created.EndsAt.Before(lo) || created.EndsAt.After(hi) {
		t.Errorf("ends_at = %v, want in [%v, %v]", created.EndsAt, lo, hi)
	}

	// 一覧に live として現れ、詳細も引ける
	var d auctionDetailJSON
	getJSON(t, fmt.Sprintf("%s/auctions/%d", ts.URL, created.ID), &d)
	if d.Status != "live" || d.CurrentPrice != 5000 || len(d.Bids) != 0 {
		t.Errorf("詳細が不正: status=%q current_price=%d bids=%d", d.Status, d.CurrentPrice, len(d.Bids))
	}
	if d.Seller.Name != "seed_user_03" {
		t.Errorf("seller = %q, want seed_user_03", d.Seller.Name)
	}
}

func TestPostAuctionValidation(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	c := loginSeedUser(t, ts.URL, "seed_user_03")

	for _, tt := range []struct {
		name string
		body string
		want int
	}{
		{"title が空", `{"title":"","description":"d","category_id":1,"starting_price":5000,"duration_seconds":30}`, http.StatusBadRequest},
		{"starting_price が0", `{"title":"t","description":"d","category_id":1,"starting_price":0,"duration_seconds":30}`, http.StatusBadRequest},
		{"duration が短すぎる", `{"title":"t","description":"d","category_id":1,"starting_price":5000,"duration_seconds":5}`, http.StatusBadRequest},
		{"duration が長すぎる", `{"title":"t","description":"d","category_id":1,"starting_price":5000,"duration_seconds":301}`, http.StatusBadRequest},
		{"存在しないカテゴリ", `{"title":"t","description":"d","category_id":999,"starting_price":5000,"duration_seconds":30}`, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res, err := c.Post(ts.URL+"/auctions", "application/json", strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", res.StatusCode, tt.want)
			}
		})
	}

	// 未ログインは 401
	res, err := http.Post(ts.URL+"/auctions", "application/json", strings.NewReader(
		`{"title":"t","description":"d","category_id":1,"starting_price":5000,"duration_seconds":30}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("未ログイン status = %d, want 401", res.StatusCode)
	}
}
