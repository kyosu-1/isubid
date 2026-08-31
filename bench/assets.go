package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// manifestJSON はビルド成果物の正解ハッシュ表。
//
// ファイル渡し(-snapshot と同じ形)にせず埋め込みにしているのは意図的である。
// 「参加者が差し替えられないこと」がこの検証の前提であり、外部ファイルにすると
// 検証そのものが無意味になる。
//
//go:embed assets/manifest.json
var manifestJSON []byte

type ManifestFile struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

type Manifest struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Files       []ManifestFile `json:"files"`
}

// LoadEmbeddedManifest は埋め込みマニフェストを読む。
func LoadEmbeddedManifest() (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return nil, fmt.Errorf("assets/manifest.json: 不正なJSON: %w", err)
	}
	if len(m.Files) == 0 {
		return nil, fmt.Errorf("assets/manifest.json: ファイルが1件も無い")
	}
	return &m, nil
}

// ByPath はパスから期待値を引く。
func (m *Manifest) ByPath(p string) (ManifestFile, bool) {
	for _, f := range m.Files {
		if f.Path == p {
			return f, true
		}
	}
	return ManifestFile{}, false
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// VerifyAssets はページロードの結果をマニフェストと突き合わせる。
//
// 200 と 304 の両方を受理し、ハッシュはデコード後のバイト列で取る。
// これは「キャッシュを効かせる」「gzip を有効にする」という正しい改善を罰しないためである。
// Content-Type の照合を限定しているのも同じ理由で、nginx の既定 mime 表と
// アプリの自前表は application/javascript と text/javascript のように正当に食い違う。
// 一方「アセットが text/html で返る」は、存在しないファイルを SPA フォールバックが
// 飲み込んでいる状態そのものであり、実際にページが壊れるので検出する。
func VerifyAssets(m *Manifest, pl *PageLoad) error {
	if pl.IndexStatus != http.StatusOK && pl.IndexStatus != http.StatusNotModified {
		return fmt.Errorf("GET /: status %d (期待: 200 か 304)", pl.IndexStatus)
	}
	if pl.IndexStatus == http.StatusOK && !strings.HasPrefix(pl.IndexType, "text/html") {
		return fmt.Errorf("GET /: Content-Type が %q (期待: text/html で始まること)", pl.IndexType)
	}
	want, ok := m.ByPath("/index.html")
	if !ok {
		return fmt.Errorf("マニフェストに /index.html が無い(ベンチのビルドが壊れている)")
	}
	if got := sha256hex(pl.IndexBody); got != want.SHA256 {
		return fmt.Errorf("GET /: 中身がビルド成果物と異なる (sha256=%s, 期待=%s)", got, want.SHA256)
	}

	for _, f := range m.Files {
		if f.Path == "/index.html" {
			continue
		}
		r, ok := pl.Resources[f.Path]
		if !ok {
			return fmt.Errorf("%s が HTML から辿れない(script/link の参照が消えている)", f.Path)
		}
		if r.Status != http.StatusOK && r.Status != http.StatusNotModified {
			return fmt.Errorf("GET %s: status %d (期待: 200 か 304)", f.Path, r.Status)
		}
		if r.Status == http.StatusOK && strings.HasPrefix(r.Type, "text/html") {
			return fmt.Errorf("GET %s: Content-Type が text/html になっている"+
				"(存在しないアセットが index.html で代替されている疑い)", f.Path)
		}
		if got := sha256hex(r.Body); got != f.SHA256 {
			return fmt.Errorf("GET %s: 中身がビルド成果物と異なる (sha256=%s, 期待=%s)", f.Path, got, f.SHA256)
		}
	}
	return nil
}
