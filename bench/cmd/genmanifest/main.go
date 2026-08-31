// genmanifest は webapp/public を歩き、ベンチが埋め込む正解ハッシュ表を出力する。
//
// フロントエンドを再ビルドしたら必ず実行すること:
//
//	cd bench && go run ./cmd/genmanifest -public ../webapp/public -out assets/manifest.json
//
// 実行し忘れは bench/assets_test.go の TestManifestMatchesPublicDir が検出する。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

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

// contentTypeOf は webapp/go/static.go の contentTypes と同じ規則である。
// 記録用であって照合基準ではない(ベンチは text/html か否かだけを見る)。
var contentTypeOf = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".svg":  "image/svg+xml",
	".ico":  "image/x-icon",
	".json": "application/json; charset=utf-8",
	".png":  "image/png",
	".webp": "image/webp",
}

func main() {
	publicDir := flag.String("public", "../webapp/public", "ビルド成果物のディレクトリ")
	out := flag.String("out", "assets/manifest.json", "出力先")
	flag.Parse()

	m, err := Build(*publicDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%d files -> %s\n", len(m.Files), *out)
}

// Build は dir を歩いてマニフェストを組み立てる。パスは URL パス(/ 始まり)で持つ。
func Build(dir string) (*Manifest, error) {
	var files []ManifestFile
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		files = append(files, ManifestFile{
			Path:        "/" + filepath.ToSlash(rel),
			SHA256:      hex.EncodeToString(sum[:]),
			Size:        int64(len(b)),
			ContentType: contentTypeOf[strings.ToLower(path.Ext(rel))],
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s にファイルが1つも無い(フロントエンドをビルドしたか?)", dir)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return &Manifest{GeneratedAt: time.Now().UTC().Truncate(time.Second), Files: files}, nil
}
