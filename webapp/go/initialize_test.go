package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
	var l auctionListJSON
	if err := json.NewDecoder(res.Body).Decode(&l); err != nil {
		t.Fatal(err)
	}
	list := l.Auctions

	wantOrder := []int64{4, 2, 8, 6, 10, 1, 3, 5, 7, 9}
	// ページネーション導入後、len(list) は1ページの上限(auctionsPerPage)で
	// 飽和するため、実際の live 件数の検証には total_count を使う
	// (len(list) だけを見ると、live が上限を超えていても正しい値を報告できない)。
	if l.TotalCount != int64(len(wantOrder)) {
		t.Fatalf("total_count = %d, want %d", l.TotalCount, len(wantOrder))
	}
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

// 生成データ非搭載(ISUBID_INITIAL_DATA_DIR 未設定)では、従来どおり
// スキーマとシードだけが入る。webapp/go のテストはこの経路で走る。
func TestInitializeWithoutGeneratedData(t *testing.T) {
	if os.Getenv("ISUBID_INITIAL_DATA_DIR") != "" {
		t.Skip("生成データ搭載モードではこのテストは対象外")
	}
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var l auctionListJSON
	if err := json.NewDecoder(res.Body).Decode(&l); err != nil {
		t.Fatal(err)
	}
	// シードの live は10件のまま。len(l.Auctions) はページネーションで
	// auctionsPerPage(20件)に飽和するため、total_count で検証する。
	if l.TotalCount != 10 {
		t.Fatalf("live = %d件, want 10 (生成データが混入している)", l.TotalCount)
	}
}
