package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"testing"
)

func TestGetUserIcon(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	// シードユーザーは全員 icon が NULL なので、200 の経路を見るために
	// テスト内で直接 BLOB を入れる。initApp が毎テスト前に戻すので他へ影響しない。
	db, err := connectDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x01, 0x02, 0x03}
	if _, err := db.Exec("UPDATE users SET icon = ? WHERE id = 1", want); err != nil {
		t.Fatal(err)
	}

	res, err := http.Get(ts.URL + "/users/1/icon")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("本文が %d bytes (期待: %d bytes、内容不一致)", len(got), len(want))
	}
}

func TestGetUserIconNotSet(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	// シードユーザーは全員 icon が NULL
	res, err := http.Get(ts.URL + "/users/2/icon")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("アイコン未設定の status = %d, want 404", res.StatusCode)
	}
}

func TestGetUserIconUnknownUser(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/users/99999/icon")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("存在しないユーザーの status = %d, want 404", res.StatusCode)
	}
}

func TestGetUserIconInvalidID(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	for _, id := range []string{"notanumber", "1.5", "99999999999999999999"} {
		res, err := http.Get(fmt.Sprintf("%s/users/%s/icon", ts.URL, id))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("id=%q の status = %d, want 400", id, res.StatusCode)
		}
	}
}
