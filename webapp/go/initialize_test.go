package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestServer はテスト用サーバーを起動する。compose の mysql が起動している前提。
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := connectDB()
	if err != nil {
		t.Fatalf("connectDB: %v (dev/compose.yaml の mysql は起動していますか?)", err)
	}
	t.Cleanup(func() { db.Close() })
	ts := httptest.NewServer(newRouter(db))
	t.Cleanup(ts.Close)
	return ts
}

// initApp は POST /initialize でDBを初期状態に戻す。
func initApp(t *testing.T, ts *httptest.Server) {
	t.Helper()
	res, err := http.Post(ts.URL+"/initialize", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST /initialize: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST /initialize status = %d, want 200", res.StatusCode)
	}
}

func TestInitialize(t *testing.T) {
	ts := newTestServer(t)
	res, err := http.Post(ts.URL+"/initialize", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body struct {
		Lang string `json:"lang"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Lang != "go" {
		t.Errorf("lang = %q, want %q", body.Lang, "go")
	}
}

// initialize 後、live オークションの ends_at は初期化時刻からの相対配置になる。
// ends_at 昇順が id 昇順と一致しないこと（ORDER BY id ASC と区別可能であること）を含めて検証する。
func TestInitializeSetsRelativeEndsAt(t *testing.T) {
	ts := newTestServer(t)
	before := time.Now().UTC()
	initApp(t, ts)
	after := time.Now().UTC()

	res, err := http.Get(ts.URL + "/auctions")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var list []auctionSummaryJSON
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}

	wantOrder := []int64{4, 2, 8, 6, 10, 1, 3, 5, 7, 9}
	if len(list) != len(wantOrder) {
		t.Fatalf("len = %d, want %d", len(list), len(wantOrder))
	}
	for i, want := range wantOrder {
		if list[i].ID != want {
			t.Errorf("list[%d].ID = %d, want %d (ends_at ASC の期待順序)", i, list[i].ID, want)
		}
	}
	for _, a := range list {
		off, ok := auctionEndOffsets[a.ID]
		if !ok {
			t.Fatalf("auction %d が auctionEndOffsets にない", a.ID)
		}
		// ends_at は [before+off, after+off] の範囲に入るはず
		lo := before.Add(time.Duration(off) * time.Second)
		hi := after.Add(time.Duration(off) * time.Second)
		if a.EndsAt.Before(lo.Add(-time.Second)) || a.EndsAt.After(hi.Add(time.Second)) {
			t.Errorf("auction %d: ends_at = %v, want in [%v, %v]", a.ID, a.EndsAt, lo, hi)
		}
	}
}
