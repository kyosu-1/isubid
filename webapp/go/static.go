package main

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// contentTypes は拡張子から Content-Type を引く自前の表。
//
// 意図的に遅い実装: mime パッケージにも http.ServeContent にも頼らない。
// 静的配信まわりを素朴なままにしておくことで、nginx 直配信への移行が
// 参加者にとって意味のある改善になる。
var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".svg":  "image/svg+xml",
	".ico":  "image/x-icon",
	".json": "application/json; charset=utf-8",
	".map":  "application/json; charset=utf-8",
	".png":  "image/png",
	".webp": "image/webp",
}

// resolveStaticPath は URL パスを public ディレクトリ内の実パスへ解決する。
//
// path.Clean("/"+urlPath) は先頭に "/" を付けてから正規化するため、結果は必ず "/" 始まりで
// ".." を含まない。したがって filepath.Join の結果が root の外を指すことはない。
// この性質は TestResolveStaticPathStaysUnderRoot で敵対的な入力ごと固定してある。
func resolveStaticPath(root, urlPath string) string {
	clean := path.Clean("/" + urlPath)
	return filepath.Join(root, filepath.FromSlash(clean))
}

// serveStatic は webapp/public 配下のビルド済み SPA を配信する。
//
// 意図的に遅い実装: リクエストのたびにファイル全体を os.ReadFile でメモリに読み、
// Cache-Control / ETag / Last-Modified を一切付けず、gzip も行わない。
// 条件付き GET は常に 200 になる。ベンチは 304 も圧縮も受理するので、
// nginx 直配信・キャッシュヘッダ・gzip のいずれもスコアが伸びる方向にしか働かない。
//
// 配信規則:
//  1. /api 配下は未知のAPIパスなので 404(SPA ではない)
//  2. 実ファイルがあればそれを返す
//  3. 無い場合、/assets/ 配下か拡張子付きのパスは 404。
//     ここで index.html を 200 で返すと「壊れているのに壊れて見えない」状態を作ってしまう
//  4. それ以外(SPA のクライアントルート)は index.html を 200 で返す
func (h *handler) serveStatic(w http.ResponseWriter, r *http.Request) {
	root := getEnv("ISUBID_PUBLIC_DIR", "../public")
	clean := path.Clean("/" + r.URL.Path)

	if clean == "/api" || strings.HasPrefix(clean, "/api/") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	full := resolveStaticPath(root, r.URL.Path)
	if b, err := os.ReadFile(full); err == nil {
		writeStaticFile(w, full, b)
		return
	}

	if strings.HasPrefix(clean, "/assets/") || path.Ext(clean) != "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	index := filepath.Join(root, "index.html")
	b, err := os.ReadFile(index)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeStaticFile(w, index, b)
}

func writeStaticFile(w http.ResponseWriter, name string, b []byte) {
	ct, ok := contentTypes[strings.ToLower(filepath.Ext(name))]
	if !ok {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
