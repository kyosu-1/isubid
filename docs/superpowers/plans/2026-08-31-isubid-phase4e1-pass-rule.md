# Phase 4-E1 実装計画: 合否判定の liveness floor と減点の実効化

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 採点対象エンドポイントが1本でも「ほぼ成功していない」走行を自動で FAIL させ、あわせて実質的に機能していない減点を効くようにする。

**Architecture:** 成功回数は既に `result.Score.Breakdown()` に入っている(`step.AddScore(tag)` は成功時にのみ呼ばれる)ので新しい計測は要らない。判定ロジックを純関数として `bench/liveness.go` に切り出し、`bench/main.go` はそれを呼んで出力と `pass` 条件に反映するだけにする。

**Tech Stack:** Go 1.22 / isucandar

**Spec:** `docs/superpowers/specs/2026-08-31-isubid-phase4e1-pass-rule-design.md`

## Global Constraints

- **floor は `max(1, 走行秒数 / 10)`。** 既定の60秒走行なら 6
- **floor はワーカーが有効なエンドポイントにのみ課す。** 対応は `ScoreGETList`/`ScoreGETDetail` → `bidders > 0 || watchers > 0`、`ScoreGETSearch` → `watchers > 0`、`ScorePOSTBid`/`ScoreGETFeed` → `bidders > 0`、`ScoreGETNotifications` → `notifiers > 0`、`ScorePOSTAuction` → `sellers > 0`
- **`errorPenalty` は 1 → 20**
- **`errorLimit = 100`(絶対件数)は変更しない**
- **エラー率ベースの上限は入れない**(設計 §5 の理由による)
- 変更するのは `bench` モジュールのみ。`webapp/go` と `initial-data` は触らない
- コメント・エラーメッセージ・テストメッセージは日本語
- **ベンチマーカーが正しいアプリを誤って FAIL させること(false-FAIL)が、このプロジェクトで最悪の結果**

## 前提環境

- アプリ: `docker compose -f dev/compose.yaml up -d` で `http://localhost:8080`
- ベンチ: `cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json`
- `bench` のテストは MySQL やアプリを必要としない純粋な単体テスト: `cd bench && go test -count=1 ./...`

---

### Task 1: liveness 判定ロジックを純関数として切り出す

**Files:**
- Create: `bench/liveness.go`
- Create: `bench/liveness_test.go`
- Modify: `bench/score.go:36-39`(`errorPenalty`)

**Interfaces:**
- Consumes: `bench/score.go` の `ScoreGETList` 等7つのタグと `scoreTable`
- Produces:
  - `type scoredTag struct { Tag score.ScoreTag; Name string }`
  - `var scoredTags []scoredTag`(採点対象タグと表示名の順序付き一覧)
  - `type workerCounts struct { Bidders, Watchers, Notifiers, Sellers int }`
  - `func livenessFloor(d time.Duration) int64`
  - `func livenessRequired(tag score.ScoreTag, w workerCounts) bool`
  - `func checkLiveness(breakdown map[score.ScoreTag]int64, floor int64, w workerCounts) []scoredTag`
  - Task 2 がこれらすべてを使う

**なぜ `scoredTags` を切り出すのか:** 現在 `bench/main.go:94-105` にタグと表示名の並びが直書きされており、`raw` の計算(`main.go:60` の `scoreTable` ループ)とは別の場所にある。**片方に足し忘れると内訳の合計が `raw` と一致しなくなる**(4-B の Task 8 で実際に懸念された)。単一の定義にまとめ、`scoreTable` を網羅していることをテストで固定する。

- [ ] **Step 1: 失敗するテストを書く**

`bench/liveness_test.go` を新規作成:

```go
package main

import (
	"testing"
	"time"

	"github.com/isucon/isucandar/score"
)

// TestScoredTagsCoversScoreTable は採点対象の並び(内訳出力と liveness 判定が使う)が
// 配点表と過不足なく一致することを固定する。片方にタグを足し忘れると、内訳の合計が
// raw と一致しなくなるか、新しいエンドポイントが liveness 判定をすり抜ける。
func TestScoredTagsCoversScoreTable(t *testing.T) {
	if len(scoredTags) != len(scoreTable) {
		t.Fatalf("scoredTags %d件 と scoreTable %d件 が不一致", len(scoredTags), len(scoreTable))
	}
	seen := map[score.ScoreTag]bool{}
	for _, st := range scoredTags {
		if _, ok := scoreTable[st.Tag]; !ok {
			t.Errorf("scoredTags の %q が scoreTable に無い", st.Tag)
		}
		if seen[st.Tag] {
			t.Errorf("scoredTags に %q が重複している", st.Tag)
		}
		seen[st.Tag] = true
		if st.Name == "" {
			t.Errorf("%q の表示名が空", st.Tag)
		}
	}
}

// TestLivenessRequiredCoversAllTags は、すべてのワーカーが有効なら採点対象すべてに
// floor が課されることを固定する。新しい採点タグを livenessRequired へ足し忘れると、
// そのエンドポイントは黙って判定をすり抜ける。
func TestLivenessRequiredCoversAllTags(t *testing.T) {
	all := workerCounts{Bidders: 1, Watchers: 1, Notifiers: 1, Sellers: 1}
	for _, st := range scoredTags {
		if !livenessRequired(st.Tag, all) {
			t.Errorf("%q に floor が課されていない", st.Tag)
		}
	}
}

func TestLivenessFloor(t *testing.T) {
	for _, tt := range []struct {
		d    time.Duration
		want int64
	}{
		{60 * time.Second, 6},
		{10 * time.Second, 1},
		{9 * time.Second, 1},  // 端数は切り捨てだが最低 1
		{1 * time.Second, 1},
		{0, 1},
		{120 * time.Second, 12},
	} {
		if got := livenessFloor(tt.d); got != tt.want {
			t.Errorf("livenessFloor(%v) = %d, want %d", tt.d, got, tt.want)
		}
	}
}

// TestLivenessRequiredRespectsWorkerCounts は、ワーカーを止めた走行で
// そのワーカーしか叩かないエンドポイントに floor を課さないことを固定する。
// ここが効いていないと -sellers 0 のデバッグ走行が誤って FAIL する。
func TestLivenessRequiredRespectsWorkerCounts(t *testing.T) {
	noSellers := workerCounts{Bidders: 1, Watchers: 1, Notifiers: 1, Sellers: 0}
	if livenessRequired(ScorePOSTAuction, noSellers) {
		t.Error("sellers=0 なのに POST /auctions に floor が課されている")
	}
	if !livenessRequired(ScorePOSTBid, noSellers) {
		t.Error("bidders=1 なのに POST /auctions/:id/bids に floor が課されていない")
	}

	noBidders := workerCounts{Bidders: 0, Watchers: 1, Notifiers: 1, Sellers: 1}
	if livenessRequired(ScorePOSTBid, noBidders) {
		t.Error("bidders=0 なのに POST /auctions/:id/bids に floor が課されている")
	}
	if livenessRequired(ScoreGETFeed, noBidders) {
		t.Error("bidders=0 なのに GET /auctions/:id/bids に floor が課されている")
	}
	// 一覧と詳細は bidder と watcher の両方が叩くので、片方が生きていれば課す
	if !livenessRequired(ScoreGETList, noBidders) {
		t.Error("watchers=1 なのに GET /auctions に floor が課されていない")
	}
	if !livenessRequired(ScoreGETDetail, noBidders) {
		t.Error("watchers=1 なのに GET /auctions/:id に floor が課されていない")
	}

	noWatchers := workerCounts{Bidders: 1, Watchers: 0, Notifiers: 1, Sellers: 1}
	if livenessRequired(ScoreGETSearch, noWatchers) {
		t.Error("watchers=0 なのに GET /auctions (検索) に floor が課されている")
	}
	if !livenessRequired(ScoreGETList, noWatchers) {
		t.Error("bidders=1 なのに GET /auctions に floor が課されていない")
	}

	none := workerCounts{}
	for _, st := range scoredTags {
		if livenessRequired(st.Tag, none) {
			t.Errorf("ワーカーが全て0なのに %q に floor が課されている", st.Tag)
		}
	}
}

func TestCheckLiveness(t *testing.T) {
	all := workerCounts{Bidders: 8, Watchers: 4, Notifiers: 2, Sellers: 2}

	// 全て floor 以上なら空
	healthy := map[score.ScoreTag]int64{}
	for _, st := range scoredTags {
		healthy[st.Tag] = 100
	}
	if dead := checkLiveness(healthy, 6, all); len(dead) != 0 {
		t.Errorf("健全な走行で %d件が下回った: %+v", len(dead), dead)
	}

	// ちょうど floor は通る(境界)
	atFloor := map[score.ScoreTag]int64{}
	for _, st := range scoredTags {
		atFloor[st.Tag] = 6
	}
	if dead := checkLiveness(atFloor, 6, all); len(dead) != 0 {
		t.Errorf("ちょうど floor の走行が落ちた: %+v", dead)
	}

	// floor - 1 は落ちる(境界)
	belowFloor := map[score.ScoreTag]int64{}
	for _, st := range scoredTags {
		belowFloor[st.Tag] = 5
	}
	if dead := checkLiveness(belowFloor, 6, all); len(dead) != len(scoredTags) {
		t.Errorf("floor-1 で落ちたのが %d件, want %d件", len(dead), len(scoredTags))
	}

	// 4-A で見逃された形: 一覧経路が全滅し、通知と出品だけ生きている
	partial := map[score.ScoreTag]int64{
		ScoreGETList:          0,
		ScoreGETSearch:        0,
		ScoreGETDetail:        0,
		ScorePOSTBid:          0,
		ScoreGETFeed:          0,
		ScoreGETNotifications: 513,
		ScorePOSTAuction:      493,
	}
	dead := checkLiveness(partial, 6, all)
	if len(dead) != 5 {
		t.Fatalf("下回ったのが %d件, want 5件: %+v", len(dead), dead)
	}
	// 返る順序は scoredTags の順であること(出力の安定性のため)
	wantOrder := []score.ScoreTag{
		ScoreGETList, ScoreGETSearch, ScoreGETDetail, ScorePOSTBid, ScoreGETFeed,
	}
	for i, want := range wantOrder {
		if dead[i].Tag != want {
			t.Errorf("dead[%d] = %q, want %q", i, dead[i].Tag, want)
		}
	}

	// ワーカーを止めたぶんは 0回でも下回り扱いにしない
	noSellers := workerCounts{Bidders: 8, Watchers: 4, Notifiers: 2, Sellers: 0}
	zeroSeller := map[score.ScoreTag]int64{}
	for _, st := range scoredTags {
		zeroSeller[st.Tag] = 100
	}
	zeroSeller[ScorePOSTAuction] = 0
	if dead := checkLiveness(zeroSeller, 6, noSellers); len(dead) != 0 {
		t.Errorf("sellers=0 の走行で %+v が下回り扱いになった", dead)
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

Run: `cd bench && go test -count=1 -run 'TestScoredTags|TestLiveness|TestCheckLiveness' ./...`
Expected: FAIL(`scoredTags` / `workerCounts` / `livenessFloor` / `livenessRequired` / `checkLiveness` が未定義でコンパイルエラー)

- [ ] **Step 3: `bench/liveness.go` を実装する**

```go
package main

import (
	"time"

	"github.com/isucon/isucandar/score"
)

// scoredTag は採点対象タグと、スコア内訳に出す表示名の対。
//
// 内訳の出力(bench/main.go)と liveness 判定の両方がこの並びを使う。以前は
// main.go に直書きされており、scoreTable と二重管理になっていた ——
// 片方に足し忘れると内訳の合計が raw と一致しなくなる。単一の定義にまとめ、
// TestScoredTagsCoversScoreTable が scoreTable との一致を固定している。
type scoredTag struct {
	Tag  score.ScoreTag
	Name string
}

var scoredTags = []scoredTag{
	{ScoreGETList, "GET /auctions"},
	{ScoreGETSearch, "GET /auctions (検索)"},
	{ScoreGETDetail, "GET /auctions/:id"},
	{ScorePOSTBid, "POST /auctions/:id/bids"},
	{ScoreGETFeed, "GET /auctions/:id/bids"},
	{ScoreGETNotifications, "GET /notifications"},
	{ScorePOSTAuction, "POST /auctions"},
}

// workerCounts は Load を駆動するワーカー数。liveness floor の条件付けに使う。
type workerCounts struct {
	Bidders   int
	Watchers  int
	Notifiers int
	Sellers   int
}

// livenessFloor は走行時間から、採点対象1本あたりの最低成功回数を返す。
//
// 4-B の small 実測では、最小のエンドポイント(POST /auctions)でも60秒で493回
// 成功している。走行秒数/10(60秒なら6回)はその 1/80 であり、正しいアプリを
// 誤って落とす余地はほぼ無い。一方で「0回」だけでなく「ほぼ死んでいる」状態も
// 捕まえられる。走行時間に比例させているのは、-duration を短くしたデバッグ走行で
// 壊れないようにするため。
func livenessFloor(d time.Duration) int64 {
	if f := int64(d / (10 * time.Second)); f > 1 {
		return f
	}
	return 1
}

// livenessRequired は tag に floor を課すべきかを返す。
//
// -sellers 0 のようにワーカーを止めたデバッグ走行で、そのワーカーしか叩かない
// エンドポイントを誤って FAIL させないための条件付け。対応は bench/load.go の
// 各 *Iteration が実際に呼ぶエンドポイントから導いている。
//
// 新しい採点タグをここへ足し忘れると、そのエンドポイントは黙って判定をすり抜ける。
// TestLivenessRequiredCoversAllTags がそれを検出する。
func livenessRequired(tag score.ScoreTag, w workerCounts) bool {
	switch tag {
	case ScoreGETList, ScoreGETDetail:
		// bidder も watcher も一覧を引いてから詳細を開く
		return w.Bidders > 0 || w.Watchers > 0
	case ScoreGETSearch:
		// 検索付きの一覧を叩くのは watcher だけ
		return w.Watchers > 0
	case ScorePOSTBid, ScoreGETFeed:
		// 入札とフィード追従は bidder だけ
		return w.Bidders > 0
	case ScoreGETNotifications:
		return w.Notifiers > 0
	case ScorePOSTAuction:
		return w.Sellers > 0
	}
	return false
}

// checkLiveness は floor を下回った採点対象を scoredTags の順で返す。
// 空スライスなら、課すべき全エンドポイントが floor に到達している。
//
// これは 4-A で見逃された欠陥への対処である。エラー上限が絶対件数(100)であるため、
// 10秒でタイムアウトする「遅い全滅」は60秒間で高々数十件のエラーしか生まず、
// 採点対象が軒並み0回のまま RESULT: PASS が出ていた。
func checkLiveness(breakdown map[score.ScoreTag]int64, floor int64, w workerCounts) []scoredTag {
	var dead []scoredTag
	for _, st := range scoredTags {
		if !livenessRequired(st.Tag, w) {
			continue
		}
		if breakdown[st.Tag] < floor {
			dead = append(dead, st)
		}
	}
	return dead
}
```

- [ ] **Step 4: テストが通ることを確認する**

Run: `cd bench && go test -count=1 -run 'TestScoredTags|TestLiveness|TestCheckLiveness' ./...`
Expected: PASS

- [ ] **Step 5: `errorPenalty` を引き上げる**

`bench/score.go:36-39` を差し替える:

```go
const (
	errorLimit = 100 // アプリエラーがこれを超えたらFail
	// errorPenalty はエラー1件あたりの減点。
	//
	// 成功1リクエストあたりの平均得点は 4-B の small 実測で約1.85点(19086点/10331回)
	// なので、エラー1件が成功約11リクエストぶんの損失になる。上限の100件で2000点、
	// raw 19086 に対して約10.5%。
	//
	// 以前は 1 だった。4-A(raw 6177)では100件で1.6%の減点だったが、4-B の
	// ページネーション導入で raw が19086まで上がり、同じ100件が0.5%になって
	// 減点が事実上機能しなくなっていた。採用スケールを変えると相対重みはまた
	// 変わるので、4-E で採用スケールを再決定する際に再調整すること。
	errorPenalty = 20
)
```

- [ ] **Step 6: 全テストとビルド**

Run: `cd bench && go build ./... && go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: PASS / vet クリーン / gofmt 出力なし

- [ ] **Step 7: コミット**

```bash
git add bench/liveness.go bench/liveness_test.go bench/score.go
git commit -m "feat: liveness判定を純関数として切り出しerrorPenaltyを20へ引き上げる"
```

---

### Task 2: main.go へ組み込む

**Files:**
- Modify: `bench/main.go:92-118`

**Interfaces:**
- Consumes: Task 1 の `scoredTags` / `workerCounts` / `livenessFloor` / `checkLiveness`
- Produces: `LIVENESS:` 行の出力と、`pass` 条件への `len(dead) == 0` の追加

- [ ] **Step 1: 内訳出力を `scoredTags` に差し替え、liveness 判定を足す**

`bench/main.go` の `raw := result.Score.Sum()` から `os.Exit(1)` までを差し替える:

```go
	raw := result.Score.Sum()
	penalty := int64(len(errs) * errorPenalty)
	total := raw - penalty
	if total < 0 {
		total = 0
	}

	fmt.Printf("SCORE: %d  (raw %d, penalty %d)\n", total, raw, penalty)
	breakdown := result.Score.Breakdown()
	for _, st := range scoredTags {
		count := breakdown[st.Tag]
		pt := count * scoreTable[st.Tag]
		fmt.Printf("  %-25s: %d回 (%d点)\n", st.Name, count, pt)
	}
	fmt.Printf("ERRORS: %d件 (critical: %d件)\n", len(errs), criticalCount)

	// 採点対象が「ほぼ成功していない」走行を落とす。エラー上限が絶対件数である
	// ため、10秒でタイムアウトする「遅い全滅」は数十件のエラーしか生まず、
	// 採点対象が軒並み0回のまま PASS が出ていた(docs/phase4-notes.md 持ち越し2)。
	floor := livenessFloor(*duration)
	workers := workerCounts{
		Bidders: *bidders, Watchers: *watchers, Notifiers: *notifiers, Sellers: *sellers,
	}
	dead := checkLiveness(breakdown, floor, workers)
	if len(dead) == 0 {
		fmt.Printf("LIVENESS: PASS (floor %d回、採点%d本すべて到達)\n", floor, len(scoredTags))
	} else {
		fmt.Printf("LIVENESS: FAIL (floor %d回)\n", floor)
		for _, st := range dead {
			fmt.Printf("  %-25s: %d回\n", st.Name, breakdown[st.Tag])
		}
	}

	pass := criticalCount == 0 && appCount <= errorLimit && total > 0 && len(dead) == 0
	if pass {
		fmt.Println("RESULT: PASS")
		return
	}
	fmt.Println("RESULT: FAIL")
	os.Exit(1)
```

`bench/main.go` の import から `"github.com/isucon/isucandar/score"` が不要になっていれば削除すること(`score.ScoreTag` の直書き参照が消えるため)。**`go build` が通ることで確認できる。**

- [ ] **Step 2: ビルドとテスト**

Run: `cd bench && go build ./... && go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: PASS / vet クリーン / gofmt 出力なし

- [ ] **Step 3: 正常な走行で PASS することを確認する**

```bash
docker compose -f dev/compose.yaml up -d
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json
```

Expected: `LIVENESS: PASS (floor 6回、採点7本すべて到達)` と `RESULT: PASS`。

**内訳の合計が `raw` と一致することも確認すること**(`scoredTags` へ差し替えた影響が無いこと)。`penalty` が 0 でない場合は `errors × 20` になっていること。

- [ ] **Step 4: Prepare の挙動が変わっていないことを確認する**

Run: `cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json -prepare-only`
Expected: `PREPARE: PASS`。`LIVENESS:` 行は**出ない**(`prepareOnly` は判定より手前で return するため)。

- [ ] **Step 5: コミット**

```bash
git add bench/main.go
git commit -m "feat: 合否判定に liveness floor を追加し内訳出力を単一定義へ寄せる"
```

---

### Task 3: ゲート測定と文書化

**Files:**
- Modify: `docs/phase4-notes.md`

**Interfaces:**
- Consumes: Task 1・2 の全て
- Produces: なし(最終タスク)

**注意:** 測定値は**必ずツールの実出力から転記**すること。数字を辻褄合わせで再構成してはならない。出力を確実に復元できない場合は「復元できない」と正直に書く。**このプロジェクトでは過去に、報告書のスコア内訳が算術的に成立しない事故と、存在しないコミットハッシュが報告される事故があった。**

- [ ] **Step 1: G1(正常な走行)**

```bash
docker compose -f dev/compose.yaml up -d
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json
```

Expected: `LIVENESS: PASS`、`RESULT: PASS`、critical 0件。出力全文を記録する。

- [ ] **Step 2: G2(遅い全滅を検出できること)← このフェーズの存在理由**

**4-A で見逃された欠陥そのものを再現する。** 速い失敗(即 500)ではなく、**クライアントのタイムアウト(10秒)を超えるまでブロックする**改変であることが重要。速い失敗はエラー件数が上限を超えるので既存の判定でも落ちてしまい、この欠陥の再現にならない。

`webapp/go/auctions.go` の `getAuctions` の**先頭**に1行入れる:

```go
func (h *handler) getAuctions(w http.ResponseWriter, r *http.Request) {
	time.Sleep(15 * time.Second) // 改悪: クライアントタイムアウト(10秒)を超えてブロックする
	q, err := parseAuctionListQuery(r.URL.Query())
```

`webapp/go/auctions.go` は既に `time` を import している。

```bash
docker compose -f dev/compose.yaml build app && docker compose -f dev/compose.yaml up -d
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json
```

Expected:
- `LIVENESS: FAIL (floor 6回)` に続いて、`GET /auctions` / `GET /auctions (検索)` / `GET /auctions/:id` / `POST /auctions/:id/bids` / `GET /auctions/:id/bids` の5本が名指しされる(bidder は一覧を引けないと詳細以降へ進めない)
- `RESULT: FAIL`

**そのうえで、この走行が liveness 判定なしなら PASS していたことを確認・記録すること:**
- `critical: 0件` であること
- `ERRORS` のアプリエラー件数が **100件以下**であること(上限に達していない)
- `SCORE` が 0 より大きいこと

この3つが揃っていれば、旧ルール `criticalCount == 0 && appCount <= errorLimit && total > 0` は真になっていた。**これが 4-A で `full` が PASS した機構そのものである。**

確認後、必ず戻すこと:

```bash
git checkout webapp/go/auctions.go
docker compose -f dev/compose.yaml build app && docker compose -f dev/compose.yaml up -d
git status --short   # クリーンであること
```

- [ ] **Step 3: G3(ワーカーを止めても誤検知しないこと)**

```bash
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json -sellers 0
```

Expected: `POST /auctions` が 0回でも `LIVENESS: PASS`。`RESULT: PASS`。

**注意:** `-sellers 0` だと出品が起きないので、`Board` 経由で新規出品へ入札する経路が使われなくなる。それ自体は正常。critical が出ていないことを確認すること。

- [ ] **Step 4: G4(短い走行でも壊れないこと)**

```bash
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json -duration 10s
```

Expected: `LIVENESS: PASS (floor 1回、…)` と表示され、`RESULT: PASS`。

**10秒走行で floor 1 すら満たせないエンドポイントがあれば記録すること。** その場合は floor の設計ではなく走行時間が短すぎることが原因なので、事実として記録し、判断は 4-E へ持ち越す。

- [ ] **Step 5: G5(Prepare の非回帰)**

```bash
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json -prepare-only
```

Expected: `PREPARE: PASS`。`LIVENESS:` 行が出ないこと。所要時間も記録する(6秒基準内)。

- [ ] **Step 6: `docs/phase4-notes.md` に 4-E1 のセクションを追記する**

以下を含めること。

- **持ち越し2 を「4-E1 で対応済み」と明記する。** 持ち越しリストから消すのではなく、4-B の持ち越し7 と同じ形でインラインに追記する
- G1〜G5 の実測ログ(**ツールの実出力を転記**)
- **G2 の「旧ルールなら PASS していた」ことの証拠**(critical 0件 / アプリエラーが100件以下 / SCORE > 0)
- 設計判断: floor を走行時間比例(`max(1, 秒数/10)`)にした理由、ワーカー数で条件付けした理由、`errorPenalty` を定数20にした理由
- **ゲート3の手順を書き換える。** 4-A・4-B のゲート手順にある「採点対象すべてが0回でないことを**目視で明示的に確認**すること」を、「`LIVENESS: PASS` を確認すること」に差し替える。以降のフェーズがこの新しい手順を使う
- 新規の持ち越しとして次を記録する:
  - **エラーのエンドポイント紐付けと、割合ベースのエラー上限。** 現在エラーはフラットな配列で、どのエンドポイントで起きたか分からない。そのため「一部のエンドポイントだけが遅く壊れている」(例: 6回成功・60回失敗)は、全体のエラー率では捕まえられない(試行1万回に対して0.6%)。`addErr` の全呼び出し箇所にタグを配る変更が必要
  - **`errorPenalty = 20` は採用スケールに依存する。** 4-E で採用スケールを再決定する際に再調整が要る
  - **floor が「ほぼ死んでいる」の閾値として保守的すぎる可能性。** 実測の最小値は493回で floor は 6。桁違いに安全側だが、その分「60秒で10回しか成功しない」ような degradation は通してしまう

- [ ] **Step 7: 全モジュールの最終確認**

```bash
cd /Users/abe/ghq/github.com/kyosu-1/isubid
gofmt -l ./webapp/go ./bench ./initial-data
(cd webapp/go && go vet ./... && go test -count=1 ./...)
(cd bench && go vet ./... && go test -count=1 ./...)
(cd initial-data && go vet ./... && go test -count=1 ./...)
git status --short
```

Expected: gofmt 出力なし、vet クリーン、3モジュールとも `ok`、作業ツリーがクリーン(G2 の改悪を戻し忘れていないこと)。

- [ ] **Step 8: コミット**

```bash
git add docs/phase4-notes.md
git commit -m "docs: 4-E1のゲート実測と持ち越しを記録しゲート3の手順を差し替える"
```

---

## Self-Review メモ

**Spec カバレッジ:**

| Spec セクション | 実装タスク |
|---|---|
| §1 liveness floor のルール | Task 1(`checkLiveness`) |
| §1 floor の値 | Task 1(`livenessFloor`) |
| §1 ワーカー有効性による条件付け | Task 1(`livenessRequired`) |
| §1 出力 | Task 2 |
| §2 `errorPenalty` の引き上げ | Task 1 Step 5 |
| §3 影響範囲 | Task 1・2・3 で全ファイルを網羅 |
| §4 完了ゲート G1〜G5 | Task 3 |
| §5 やらないこと | Task 3 Step 6(持ち越しとして記録) |

**型の一貫性:** `scoredTag` / `scoredTags` / `workerCounts` / `livenessFloor` / `livenessRequired` / `checkLiveness` の名前とシグネチャは Task 1 で定義し Task 2 が使う。`bench/main.go` の flag 変数(`*duration` / `*bidders` / `*watchers` / `*notifiers` / `*sellers`)は既存のものをそのまま使う。
