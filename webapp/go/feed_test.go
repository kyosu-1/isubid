package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

type feedResponseJSON struct {
	Bids []bidJSON `json:"bids"`
}

func TestGetAuctionBids(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	// auction 1 の seed 入札は id 1,2,3 (amount 1000/1200/1500)
	var all feedResponseJSON
	getJSON(t, ts.URL+"/auctions/1/bids", &all)
	if len(all.Bids) != 3 {
		t.Fatalf("since 無し: %d件, want 3", len(all.Bids))
	}
	// id ASC / 金額は受理順なので単調増加
	for i := 1; i < len(all.Bids); i++ {
		if all.Bids[i].ID <= all.Bids[i-1].ID {
			t.Errorf("id ASC でない: [%d].ID=%d, [%d].ID=%d", i-1, all.Bids[i-1].ID, i, all.Bids[i].ID)
		}
		if all.Bids[i].Amount <= all.Bids[i-1].Amount {
			t.Errorf("金額が単調増加でない: %d -> %d", all.Bids[i-1].Amount, all.Bids[i].Amount)
		}
	}
	if all.Bids[0].User.Name != "seed_user_02" {
		t.Errorf("bids[0].user.name = %q, want seed_user_02", all.Bids[0].User.Name)
	}

	// since で絞り込む
	var after feedResponseJSON
	getJSON(t, ts.URL+"/auctions/1/bids?since=2", &after)
	if len(after.Bids) != 1 {
		t.Fatalf("since=2: %d件, want 1", len(after.Bids))
	}
	if after.Bids[0].ID != 3 {
		t.Errorf("since=2 の先頭 id = %d, want 3", after.Bids[0].ID)
	}

	// 全部読み切った後は空配列(null ではない)
	var none feedResponseJSON
	getJSON(t, ts.URL+"/auctions/1/bids?since=3", &none)
	if none.Bids == nil {
		t.Error("bids が null (期待: 空配列)")
	}
	if len(none.Bids) != 0 {
		t.Errorf("since=3: %d件, want 0", len(none.Bids))
	}
}

func TestGetAuctionBidsErrors(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	for _, tt := range []struct {
		path string
		want int
	}{
		{"/auctions/1/bids?since=abc", http.StatusBadRequest},
		{"/auctions/1/bids?since=-1", http.StatusBadRequest},
		{"/auctions/abc/bids", http.StatusBadRequest},
		{"/auctions/99999/bids", http.StatusNotFound},
	} {
		res, err := http.Get(ts.URL + tt.path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tt.want {
			t.Errorf("GET %s status = %d, want %d", tt.path, res.StatusCode, tt.want)
		}
	}
}

// エラーボディが {"error": "..."} 形式であること
func TestGetAuctionBidsErrorBody(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	res, err := http.Get(ts.URL + "/auctions/1/bids?since=abc")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error == "" {
		t.Error("error フィールドが空")
	}
}
