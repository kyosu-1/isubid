package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
