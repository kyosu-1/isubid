package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
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
