package main

import (
	"bytes"
	"crypto/sha256"
	"image/png"
	"testing"
)

// TestGenerateIconIsDeterministic は同じ id から常に同じバイト列が返ることを固定する。
// ここが崩れると initial-data の「同じ scale と seed なら生成物は完全に一致する」
// という設計要件が壊れる。
func TestGenerateIconIsDeterministic(t *testing.T) {
	for _, id := range []int64{21, 100, 520} {
		a := GenerateIcon(id)
		b := GenerateIcon(id)
		if !bytes.Equal(a, b) {
			t.Errorf("id=%d: 2回の生成でバイト列が異なる (%d bytes vs %d bytes)", id, len(a), len(b))
		}
	}
}

// TestGenerateIconIsDistinctPerUser は id ごとに異なる画像になることを固定する。
// 全員同じ画像だと、参照実装が「1枚だけ返して使い回す」改悪をしてもベンチの
// sha256 照合が素通りしてしまう。
func TestGenerateIconIsDistinctPerUser(t *testing.T) {
	seen := map[[32]byte]int64{}
	for id := int64(21); id < 221; id++ {
		h := sha256.Sum256(GenerateIcon(id))
		if prev, ok := seen[h]; ok {
			t.Fatalf("id=%d と id=%d のアイコンが同一", id, prev)
		}
		seen[h] = id
	}
	if len(seen) != 200 {
		t.Errorf("distinct = %d, want 200", len(seen))
	}
}

// TestGenerateIconIsValidPNG は出力が本物の PNG で、寸法が仕様どおりであることを固定する。
func TestGenerateIconIsValidPNG(t *testing.T) {
	b := GenerateIcon(42)
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("PNG としてデコードできない: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != iconSize || bounds.Dy() != iconSize {
		t.Errorf("寸法が %dx%d, want %dx%d", bounds.Dx(), bounds.Dy(), iconSize, iconSize)
	}
}

// TestGenerateIconSizeIsBounded はアイコンが肥大化していないことを固定する。
// 上限は生成物をリポジトリにコミットする都合から置いている(SQL ダンプには
// 16進リテラルで入るのでファイル上は2倍になる)。実サイズは docs/phase4-notes.md に記録する。
func TestGenerateIconSizeIsBounded(t *testing.T) {
	const maxBytes = 8 * 1024
	for _, id := range []int64{21, 100, 520} {
		if n := len(GenerateIcon(id)); n > maxBytes {
			t.Errorf("id=%d: %d bytes (上限 %d bytes)", id, n, maxBytes)
		}
	}
}
