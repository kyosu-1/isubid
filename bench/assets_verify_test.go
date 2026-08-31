package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
)

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var (
	indexBody = []byte(`<!doctype html><html><head><link rel="stylesheet" href="/assets/a.css"></head><body><script type="module" src="/assets/a.js"></script></body></html>`)
	jsBody    = []byte(`console.log(1)`)
	cssBody   = []byte(`body{margin:0}`)
)

func fixtureManifest() *Manifest {
	return &Manifest{Files: []ManifestFile{
		{Path: "/index.html", SHA256: hashOf(indexBody), Size: int64(len(indexBody))},
		{Path: "/assets/a.js", SHA256: hashOf(jsBody), Size: int64(len(jsBody))},
		{Path: "/assets/a.css", SHA256: hashOf(cssBody), Size: int64(len(cssBody))},
	}}
}

func fixturePageLoad() *PageLoad {
	return &PageLoad{
		IndexStatus: http.StatusOK,
		IndexType:   "text/html; charset=utf-8",
		IndexBody:   indexBody,
		Resources: map[string]LoadedResource{
			"/assets/a.js":  {Path: "/assets/a.js", Status: 200, Type: "text/javascript; charset=utf-8", Body: jsBody},
			"/assets/a.css": {Path: "/assets/a.css", Status: 200, Type: "text/css; charset=utf-8", Body: cssBody},
		},
	}
}

func TestVerifyAssetsOK(t *testing.T) {
	if err := VerifyAssets(fixtureManifest(), fixturePageLoad()); err != nil {
		t.Fatalf("正常系で err = %v", err)
	}
}

// 304 と圧縮は正しい最適化なので受理する。agent が本文を差し戻すので Body は実体のまま。
func TestVerifyAssetsAccepts304(t *testing.T) {
	pl := fixturePageLoad()
	r := pl.Resources["/assets/a.js"]
	r.Status = http.StatusNotModified
	r.Type = "" // 304 は Content-Type を返さないことがある
	pl.Resources["/assets/a.js"] = r
	if err := VerifyAssets(fixtureManifest(), pl); err != nil {
		t.Fatalf("304 を受理すべきなのに err = %v", err)
	}
}

func TestVerifyAssetsDetectsTamperedBody(t *testing.T) {
	pl := fixturePageLoad()
	r := pl.Resources["/assets/a.js"]
	r.Body = []byte(`console.log(2)`)
	pl.Resources["/assets/a.js"] = r
	err := VerifyAssets(fixtureManifest(), pl)
	if err == nil || !strings.Contains(err.Error(), "/assets/a.js") {
		t.Fatalf("改ざんを検出できていない: %v", err)
	}
}

func TestVerifyAssetsDetectsMissingReference(t *testing.T) {
	pl := fixturePageLoad()
	delete(pl.Resources, "/assets/a.js")
	err := VerifyAssets(fixtureManifest(), pl)
	if err == nil || !strings.Contains(err.Error(), "辿れない") {
		t.Fatalf("参照の欠落を検出できていない: %v", err)
	}
}

func TestVerifyAssetsDetectsTamperedIndex(t *testing.T) {
	pl := fixturePageLoad()
	pl.IndexBody = []byte(`<!doctype html><html></html>`)
	if err := VerifyAssets(fixtureManifest(), pl); err == nil {
		t.Fatal("index.html の改ざんを検出できていない")
	}
}

// アセットが text/html で返るのは、存在しないファイルを SPA フォールバックが
// 飲み込んでいる状態そのものである。
func TestVerifyAssetsDetectsHTMLServedAsAsset(t *testing.T) {
	pl := fixturePageLoad()
	r := pl.Resources["/assets/a.js"]
	r.Type = "text/html; charset=utf-8"
	pl.Resources["/assets/a.js"] = r
	err := VerifyAssets(fixtureManifest(), pl)
	if err == nil || !strings.Contains(err.Error(), "text/html") {
		t.Fatalf("text/html で返るアセットを検出できていない: %v", err)
	}
}

func TestVerifyAssetsDetectsBadStatus(t *testing.T) {
	pl := fixturePageLoad()
	r := pl.Resources["/assets/a.css"]
	r.Status = http.StatusInternalServerError
	pl.Resources["/assets/a.css"] = r
	if err := VerifyAssets(fixtureManifest(), pl); err == nil {
		t.Fatal("5xx を検出できていない")
	}
}

// マニフェストに無いパスを参加者が足すのは自由。
func TestVerifyAssetsIgnoresExtraResources(t *testing.T) {
	pl := fixturePageLoad()
	pl.Resources["/assets/extra.js"] = LoadedResource{
		Path: "/assets/extra.js", Status: 200, Type: "text/javascript", Body: []byte("x"),
	}
	if err := VerifyAssets(fixtureManifest(), pl); err != nil {
		t.Fatalf("追加アセットで落ちてはいけない: %v", err)
	}
}
