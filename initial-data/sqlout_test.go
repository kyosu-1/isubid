package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 生成文字列を SQL リテラルへ素で埋め込むため、クォートやバックスラッシュを
// 含まないことを保証する。含む素材を足したらこのテストが落ちる。
func TestGeneratedStringsAreSQLSafe(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	bad := func(s string) bool {
		return strings.ContainsAny(s, "'\"\\")
	}
	for _, u := range ds.Users {
		if bad(u.Name) {
			t.Fatalf("user %d の name にクォート/バックスラッシュ: %q", u.ID, u.Name)
		}
	}
	for _, a := range ds.Auctions {
		if bad(a.Title) || bad(a.Description) {
			t.Fatalf("auction %d の title/description にクォート/バックスラッシュ: %q / %q", a.ID, a.Title, a.Description)
		}
	}
	for _, n := range ds.Notifications {
		if bad(n.Message) {
			t.Fatalf("notification %d の message にクォート/バックスラッシュ: %q", n.ID, n.Message)
		}
	}
}

func TestWriteSQLProducesLoadableFiles(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	dir := t.TempDir()
	if err := WriteSQL(dir, ds); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"91_users.sql", "92_auctions.sql", "93_bids.sql", "94_notifications.sql"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		s := string(b)
		if !strings.HasPrefix(s, "INSERT INTO ") {
			t.Errorf("%s: INSERT で始まっていない", name)
		}
		if !strings.HasSuffix(strings.TrimSpace(s), ";") {
			t.Errorf("%s: セミコロンで終わっていない", name)
		}
		// マルチバリュー INSERT であること(1行1文だと読み込みが桁違いに遅い)
		stmts := strings.Count(s, "INSERT INTO ")
		rows := strings.Count(s, "),\n(") + stmts
		if rows <= stmts {
			t.Errorf("%s: 1文あたり1行しかない (stmts=%d rows=%d)", name, stmts, rows)
		}
	}

	// closed の winner は NULL でない値として、live/upcoming は NULL として出る
	b, err := os.ReadFile(filepath.Join(dir, "92_auctions.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "NULL") {
		t.Error("92_auctions.sql に NULL(未落札の winner_id) が現れない")
	}
}

// TestUsersSQLCarriesIconHex は users の INSERT にアイコンが16進リテラルで
// 載り、未設定は NULL になることを固定する。
func TestUsersSQLCarriesIconHex(t *testing.T) {
	dir := t.TempDir()
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)
	if err := WriteSQL(dir, ds); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "91_users.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sql := string(b)

	if !strings.Contains(sql, "INSERT INTO users (id, name, password_hash, icon) VALUES") {
		t.Error("users の INSERT に icon 列が無い")
	}
	if !strings.Contains(sql, ", NULL)") {
		t.Error("アイコン未設定の NULL が書かれていない")
	}
	// アイコンを持つ最初のユーザーの16進リテラルが含まれること
	for _, u := range ds.Users {
		if u.Icon == nil {
			continue
		}
		want := "0x" + hex.EncodeToString(u.Icon)
		if !strings.Contains(sql, want) {
			t.Errorf("user %d のアイコンが16進リテラルとして見つからない", u.ID)
		}
		break
	}
}
