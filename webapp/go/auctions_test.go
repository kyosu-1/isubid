package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

type auctionListJSON struct {
	Auctions   []auctionSummaryJSON `json:"auctions"`
	TotalCount int64                `json:"total_count"`
	HasNext    bool                 `json:"has_next"`
}

// getAuctionList は一覧を取得して 200 とデコード結果を返すヘルパー。
func getAuctionList(t *testing.T, tsURL, query string) auctionListJSON {
	t.Helper()
	var l auctionListJSON
	getJSON(t, tsURL+"/auctions"+query, &l)
	return l
}

func TestGetAuctionsResponseShape(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	l := getAuctionList(t, ts.URL, "")
	if len(l.Auctions) != 10 {
		t.Fatalf("auctions len = %d, want 10", len(l.Auctions))
	}
	if l.TotalCount != 10 {
		t.Errorf("total_count = %d, want 10", l.TotalCount)
	}
	if l.HasNext {
		t.Errorf("has_next = true, want false (10件は1ページに収まる)")
	}
}

func TestGetAuctions(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	l := getAuctionList(t, ts.URL, "")
	if len(l.Auctions) != 10 {
		t.Fatalf("len = %d, want 10", len(l.Auctions))
	}
	byID := map[int64]auctionSummaryJSON{}
	for _, a := range l.Auctions {
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

	list := getAuctionList(t, ts.URL, "").Auctions
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

// createLiveAuctions は duration_seconds を揃えて n 件出品する。
// シードの live は ends_at が +12/20/28/36/44 秒と +3600 秒以降に分かれているため、
// duration=100 で作った分はその中間にまとまって並ぶ。結果として一覧の順序は
// 「シードの短い5件 → 作成した n 件(id昇順) → シードの長い5件」で決定的になる。
func createLiveAuctions(t *testing.T, ts *httptest.Server, n int) []int64 {
	t.Helper()
	c := loginSeedUser(t, ts.URL, "seed_user_03")
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		body := fmt.Sprintf(
			`{"title":"ページング用 %02d","description":"説明","category_id":1,"starting_price":5000,"duration_seconds":100}`, i)
		res, err := c.Post(ts.URL+"/auctions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var created auctionCreatedJSON
		if res.StatusCode != http.StatusCreated {
			res.Body.Close()
			t.Fatalf("POST /auctions status = %d, want 201", res.StatusCode)
		}
		if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
			res.Body.Close()
			t.Fatal(err)
		}
		res.Body.Close()
		ids = append(ids, created.ID)
	}
	return ids
}

func TestGetAuctionsPagination(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	createLiveAuctions(t, ts, 15) // live は 10 + 15 = 25 件

	p1 := getAuctionList(t, ts.URL, "?page=1")
	if len(p1.Auctions) != 20 {
		t.Fatalf("page 1 の件数 = %d, want 20", len(p1.Auctions))
	}
	if p1.TotalCount != 25 {
		t.Errorf("page 1 の total_count = %d, want 25", p1.TotalCount)
	}
	if !p1.HasNext {
		t.Error("page 1 の has_next = false, want true")
	}

	p2 := getAuctionList(t, ts.URL, "?page=2")
	if len(p2.Auctions) != 5 {
		t.Fatalf("page 2 の件数 = %d, want 5", len(p2.Auctions))
	}
	if p2.TotalCount != 25 {
		t.Errorf("page 2 の total_count = %d, want 25", p2.TotalCount)
	}
	if p2.HasNext {
		t.Error("page 2 の has_next = true, want false")
	}

	// page 未指定は page=1 と同じ
	p0 := getAuctionList(t, ts.URL, "")
	if len(p0.Auctions) != 20 || p0.Auctions[0].ID != p1.Auctions[0].ID {
		t.Errorf("page 未指定が page=1 と一致しない")
	}

	// ページを跨いで id が重複せず、ends_at が非減少であること
	seen := map[int64]bool{}
	all := append(append([]auctionSummaryJSON{}, p1.Auctions...), p2.Auctions...)
	for _, a := range all {
		if seen[a.ID] {
			t.Errorf("id %d がページを跨いで重複している", a.ID)
		}
		seen[a.ID] = true
	}
	for i := 1; i < len(all); i++ {
		if all[i].EndsAt.Before(all[i-1].EndsAt) {
			t.Errorf("連結後の ends_at が昇順でない (index %d)", i)
		}
	}
	// 25件すべてが現れる = LIMIT/OFFSET が正しく歩けている
	if len(seen) != 25 {
		t.Errorf("全ページの合計が %d件, want 25", len(seen))
	}
}

func TestGetAuctionsPageOutOfRange(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	l := getAuctionList(t, ts.URL, "?page=99")
	if len(l.Auctions) != 0 {
		t.Errorf("範囲外ページの件数 = %d, want 0", len(l.Auctions))
	}
	if l.TotalCount != 10 {
		t.Errorf("範囲外ページの total_count = %d, want 10", l.TotalCount)
	}
	if l.HasNext {
		t.Error("範囲外ページの has_next = true, want false")
	}
}

func TestGetAuctionsEmptyArrayNotNull(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions?page=99")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"auctions":[]`) {
		t.Errorf("空結果が [] でない: %s", b)
	}
}

func TestGetAuctionsInvalidPage(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	for _, q := range []string{"?page=0", "?page=-1", "?page=abc", "?page=1.5", "?page=99999999999999999999"} {
		res, err := http.Get(ts.URL + "/auctions" + q)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("GET /auctions%s status = %d, want 400", q, res.StatusCode)
		}
	}
}
