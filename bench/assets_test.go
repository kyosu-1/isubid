package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

const publicDirForTest = "../webapp/public"

// bundleSizeLimit は webapp/public の合計サイズ上限(未圧縮)。
// バンドルが肥大すると Load 中の静的配信コストが支配的になり、
// 「入札が主役」という配点設計(score.go)が崩れる。
const bundleSizeLimit = 300 * 1024

// TestManifestMatchesPublicDir はフロントエンドを再ビルドしたのに
// genmanifest を流し忘れた状態を検出する。
func TestManifestMatchesPublicDir(t *testing.T) {
	if _, err := os.Stat(publicDirForTest); os.IsNotExist(err) {
		t.Skip("webapp/public が無い(ベンチ単体で配布された場合)")
	}
	m, err := LoadEmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	var total int64
	err = filepath.Walk(publicDirForTest, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(publicDirForTest, p)
		if err != nil {
			return err
		}
		urlPath := "/" + filepath.ToSlash(rel)
		seen[urlPath] = true
		total += int64(len(b))

		want, ok := m.ByPath(urlPath)
		if !ok {
			t.Errorf("%s がマニフェストに無い(cd bench && go run ./cmd/genmanifest -public ../webapp/public -out assets/manifest.json を実行したか?)", urlPath)
			return nil
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != want.SHA256 {
			t.Errorf("%s のsha256が不一致: 実ファイル=%s マニフェスト=%s(genmanifest の実行忘れ)", urlPath, got, want.SHA256)
		}
		if int64(len(b)) != want.Size {
			t.Errorf("%s のサイズが不一致: 実ファイル=%d マニフェスト=%d", urlPath, len(b), want.Size)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range m.Files {
		if !seen[f.Path] {
			t.Errorf("マニフェストの %s が webapp/public に存在しない", f.Path)
		}
	}
	if total >= bundleSizeLimit {
		t.Errorf("webapp/public の合計が %d バイトで上限 %d を超えた。依存を減らすこと", total, bundleSizeLimit)
	}
}
