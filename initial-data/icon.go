package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// iconSize はアイコンの一辺のピクセル数。
const iconSize = 64

// iconCell は市松模様1マスの一辺のピクセル数。
const iconCell = 8

// GenerateIcon は user id から決定論的に 64x64 の PNG を作る。
//
// 同じ id からは常に同じバイト列が返る(Go の image/png エンコーダは
// 同じ入力・同じ設定に対して決定論的)。
//
// **id ごとに異なる画像であることが要件。** 全員同じ画像だと、参照実装が
// 「1枚だけ返して使い回す」改悪をしてもベンチの sha256 照合が素通りしてしまう。
func GenerateIcon(id int64) []byte {
	bg := iconColor(id, 0)
	fg := iconColor(id, 1)
	// 位相を id で変えることで、色が近い2人でも模様がずれる。
	phase := int(id % 2)

	img := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize))
	for y := 0; y < iconSize; y++ {
		for x := 0; x < iconSize; x++ {
			c := bg
			if (x/iconCell+y/iconCell+phase)%2 == 0 {
				c = fg
			}
			img.SetRGBA(x, y, c)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		// *image.RGBA の符号化は失敗しない。ここに来るならプログラムの誤り。
		panic(err)
	}
	return buf.Bytes()
}

// iconColor は id と役割(0=背景 / 1=前景)から色を導く。
// 大きめの奇数を掛けて散らすことで、隣接する id が似た色にならないようにする。
func iconColor(id int64, role int64) color.RGBA {
	h := uint32((id*2654435761 + role*40503) & 0xffffff)
	return color.RGBA{R: uint8(h >> 16), G: uint8(h >> 8), B: uint8(h), A: 255}
}
