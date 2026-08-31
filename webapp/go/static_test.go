package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newStaticFixture は public ディレクトリの模型を作り、ISUBID_PUBLIC_DIR を向ける。
func newStaticFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "<!doctype html><html><body>isubid</body></html>")
	write(filepath.Join("assets", "app.js"), "console.log('isubid')")
	write(filepath.Join("assets", "app.css"), "body{margin:0}")
	t.Setenv("ISUBID_PUBLIC_DIR", dir)
	return dir
}

// getStatic は静的配信ハンドラだけを直接叩く(DB を必要としない)。
func getStatic(t *testing.T, urlPath string) *httptest.ResponseRecorder {
	t.Helper()
	h := &handler{}
	req := httptest.NewRequest(http.MethodGet, "http://example.com"+urlPath, nil)
	rec := httptest.NewRecorder()
	h.serveStatic(rec, req)
	return rec
}

func TestServeStaticIndex(t *testing.T) {
	newStaticFixture(t)
	rec := getStatic(t, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html...", ct)
	}
	if !strings.Contains(rec.Body.String(), "isubid") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestServeStaticAsset(t *testing.T) {
	newStaticFixture(t)
	rec := getStatic(t, "/assets/app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.js = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript...", ct)
	}
	if rec.Body.String() != "console.log('isubid')" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// 意図的に遅い実装であることの固定: キャッシュ関連ヘッダを付けない。
// これが付き始めたら仕込みが失われている(参加者の環境ではなく参照実装の話)。
//
// Accept-Ranges も見ている: http.ServeContent(..., time.Time{}, ...) のように
// modtime をゼロ値で渡すと Last-Modified は出ないが、Range 対応を示す
// Accept-Ranges: bytes は modtime に関係なく必ず付く。Last-Modified だけを
// 見ているとこの退行を素通りさせてしまう。
func TestServeStaticHasNoCacheHeaders(t *testing.T) {
	newStaticFixture(t)
	rec := getStatic(t, "/assets/app.js")
	for _, k := range []string{"Cache-Control", "ETag", "Last-Modified", "Content-Encoding", "Accept-Ranges"} {
		if v := rec.Header().Get(k); v != "" {
			t.Errorf("%s = %q, want 空(意図的に遅い実装)", k, v)
		}
	}
}

func TestServeStaticSPAFallback(t *testing.T) {
	newStaticFixture(t)
	for _, p := range []string{"/auctions/123", "/notifications", "/stats", "/login", "/sell"} {
		rec := getStatic(t, p)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200(SPAフォールバック)", p, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "isubid") {
			t.Errorf("GET %s は index.html を返すべき", p)
		}
	}
}

// 存在しないアセットに index.html を返すと「壊れているのに壊れて見えない」状態になる。
//
// /assets/logo は拡張子を持たない。規則3は
// `strings.HasPrefix(clean, "/assets/") || path.Ext(clean) != ""` の2項からなるが、
// 他のケース(/assets/missing.js 等)は全て拡張子を持つため第2項だけで404になり、
// 第1項(/assets/ プレフィックス)の検出力がテストされないまま隠れてしまう。
// /assets/logo は第2項(path.Ext != "")が false になるケースなので、第1項を
// 削除するとこのテストで検出できる(report.md に削除→RED→復元の実証を記載)。
func TestServeStaticMissingAssetIs404(t *testing.T) {
	newStaticFixture(t)
	for _, p := range []string{"/assets/missing.js", "/missing.png", "/favicon.ico", "/assets/logo"} {
		rec := getStatic(t, p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
	}
}

// 未知の API パスは SPA ではないので 404 を返す。
func TestServeStaticUnknownAPIIs404(t *testing.T) {
	newStaticFixture(t)
	for _, p := range []string{"/api", "/api/unknown", "/api/auctions/1/nope"} {
		rec := getStatic(t, p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<!doctype") {
			t.Errorf("GET %s が HTML を返した", p)
		}
	}
}

// ルータ経由でも同じ挙動になることを固定する。
//
// chi の Mux.NotFound は updateSubRoutes で既存のサブルータにも遡ってハンドラを配るため、
// r.Route("/api", ...) の後に r.NotFound(...) を書いても /api 配下の未知パスは
// serveStatic に届く(chi v5.3.1 の mux.go で確認済み)。この伝播が将来変わると
// /api/unknown が chi 既定のプレーンテキスト404になり、静かに挙動が変わる。
// DB を触るルートは叩かないので handler は空のままでよい。
func TestRouterSendsNonAPIPathsToStatic(t *testing.T) {
	newStaticFixture(t)
	r := routerFor(&handler{})

	cases := []struct {
		path     string
		wantCode int
		wantHTML bool
	}{
		{"/", http.StatusOK, true},
		{"/auctions/123", http.StatusOK, true},
		{"/assets/app.js", http.StatusOK, false},
		{"/assets/missing.js", http.StatusNotFound, false},
		{"/api/unknown", http.StatusNotFound, false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "http://example.com"+tc.path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != tc.wantCode {
			t.Errorf("GET %s = %d, want %d", tc.path, rec.Code, tc.wantCode)
			continue
		}
		isHTML := strings.Contains(rec.Body.String(), "<!doctype")
		if isHTML != tc.wantHTML {
			t.Errorf("GET %s: HTML=%v, want %v (body=%.60q)", tc.path, isHTML, tc.wantHTML, rec.Body.String())
		}
	}
}

// resolveStaticPath は敵対的な入力でも root の外を指さない、という性質を固定する。
// (path.Clean("/"+p) が ".." を先に潰すため、実際には外へ出る経路が無いことの証明)
//
// これは resolveStaticPath という純関数の字句的性質しか見ていない。ハンドラが
// 実在の root 外ファイルを配らないことは TestServeStaticTraversalDoesNotLeakParentFile
// (このファイル内)が end-to-end で検証する。serveStatic が将来 resolveStaticPath を
// 経由しない実装(例: root + r.URL.Path の直結合)に書き換わっても、この性質テスト
// 単体は通り続けてしまうため、end-to-end 側が必要になる。
func TestResolveStaticPathStaysUnderRoot(t *testing.T) {
	root := "/srv/public"
	inputs := []string{
		"/", "/index.html", "/assets/app.js",
		"/../etc/passwd", "/../../etc/passwd", "/assets/../../etc/passwd",
		"/./../../etc/passwd", "//etc/passwd", "/assets/./app.js",
	}
	for _, in := range inputs {
		got := resolveStaticPath(root, in)
		if got != root && !strings.HasPrefix(got, root+string(filepath.Separator)) {
			t.Errorf("resolveStaticPath(%q, %q) = %q は root の外を指している", root, in, got)
		}
	}
}

// serveStatic が実在の root 外ファイルを配らないことを end-to-end で検証する。
//
// フィクスチャ(public ディレクトリ)の親に秘密ファイルを置き、"/../" 越しの
// 要求がその中身を返さないことを見る。字句的性質(TestResolveStaticPathStaysUnderRoot)
// だけでは、resolveStaticPath を経由しない実装への退行を検出できないため、
// 実際のレスポンスボディを見るテストとして別に用意する。
// 現行実装では clean("/../secret.txt") == "/secret.txt" が public 配下に
// 存在せず、拡張子付き(.txt)なので規則3により 404 になる。
func TestServeStaticTraversalDoesNotLeakParentFile(t *testing.T) {
	dir := newStaticFixture(t)
	secret := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(secret, []byte("top-secret-value"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(secret) })

	rec := getStatic(t, "/../secret.txt")
	if strings.Contains(rec.Body.String(), "top-secret-value") {
		t.Fatalf("GET /../secret.txt が root 外のファイルの中身を漏らした: body=%q", rec.Body.String())
	}
	if rec.Code == http.StatusOK {
		t.Errorf("GET /../secret.txt = 200 (body=%q); root 外のファイルを配ってはならない", rec.Body.String())
	}
}
