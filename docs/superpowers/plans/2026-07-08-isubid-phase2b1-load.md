# ISUBID Phase 2b-1(Prepare残穴つぶし + 負荷走行骨格)実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to実装 this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ベンチマーカーに60秒のLoadフェーズ(入札者/ウォッチャーシナリオ)、スコアリング、Load後のValidationフェーズ(入札台帳との突合)を実装し、「ベンチを回すとスコアが出る」状態にする。あわせてPhase 2aレビューで指摘されたPrepareの残穴を塞ぐ。

**Architecture:** 入札者は「ログイン→一覧→詳細→入札(競り負けたら再取得して再入札)」を1イテレーションとするworker、ウォッチャーは「一覧→ランダム詳細+順序検証」のworker。ベンチは受理された入札を台帳(Ledger)に記録し、Validationフェーズで各オークションの詳細と突合(全入札の存在・current_price・順序)。スコアはタグ別配点(入札5点、閲覧1点)、エラーは減点+閾値Fail、整合性違反は即Fail。

**Tech Stack:** 既存と同じ(Go, isucandar: worker/score/failure)

## Global Constraints

- リポジトリルート: `/Users/abe/ghq/github.com/kyosu-1/isubid`。作業ブランチ: `phase-2b1`
- **意図的なボトルネック(`// 意図的に遅い実装`)とwebapp/は一切変更しない**(このフェーズはbenchのみ)
- **bench/scenario.go の no-op Load/Validation スタブは「削除」ではなく「本実装に置き換え」**(isucandarはLoad実装ゼロでデッドロックする — scenario.goのコメント参照。本計画でLoadに実装が入るため問題は消えるが、コメントの経緯は保持)
- シード期待値は `webapp/sql/90_seed_phase1.sql` が正(live 10件 id順=ends_at階段順、11=closed/12000/2入札、12=upcoming/8000/0入札)
- スコア配点(スペック準拠): `POST /auctions/:id/bids`成功=5点、`GET /auctions`=1点、`GET /auctions/:id`=1点。減点: エラー1件につき1点。Fail条件: 整合性違反(criticalエラー)1件以上、またはエラー計100件超、またはスコア0以下
- ベンチCLI互換: `-target`(既存)に加え `-duration`(default 60s)、`-prepare-only`(Prepareのみ実行、従来の `PREPARE: PASS/FAIL` 出力を維持)を追加
- isucandar API名がコンパイルで合わない場合は `go doc` で実名を確認して最小適応し、レポートに記録(前例: `WithoutPanicRecover`)
- コミットメッセージ末尾: `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: Prepare残穴つぶし(2aレビューのバックログ)

**Files:**
- Modify: `bench/validate.go`(ends_at期待値、詳細検証のフィールド追加)
- Modify: `bench/validate_test.go`(fixture修正+新分岐テスト)
- Modify: `bench/scenario.go`(not-live 400、closed/upcoming詳細、too-low body検証)

**Interfaces:**
- Consumes: 既存の `Client` / validators / シード仕様
- Produces:
  - `expectedAuction` に `EndsAtHour int` フィールド(id Nの ends_at = `2030-01-01 0N:00:00 UTC`)
  - `ValidateInitialAuctionDetail` が Title / Status / BidCount / CategoryID も照合する
  - Prepareが auction 11/12 への入札400、11/12の詳細内容、too-low応答のbody(`current_price`)を検証する

- [ ] **Step 1: 失敗するテストを書く**

`bench/validate_test.go` を修正:

(a) `seedList()` 内 `base` の直後にある `mk` はそのまま。`seedDetail()` の `AuctionSummary` を修正(BidCount 0→3、CategoryID追加):

```go
		AuctionSummary: AuctionSummary{ID: 1, Title: "ヘリテージ・ウィングチェア", CategoryID: 3, CurrentPrice: 1500, BidCount: 3, Status: "live"},
```

(b) 新テストを追加:

```go
func TestValidateInitialAuctionListWrongEndsAtValue(t *testing.T) {
	list := seedList()
	// idの並びは正しいが ends_at の絶対値が期待とずれている(全体を+1日シフト)
	for i := range list {
		list[i].EndsAt = list[i].EndsAt.Add(24 * time.Hour)
	}
	err := ValidateInitialAuctionList(list)
	if err == nil || !strings.Contains(err.Error(), "ends_at") {
		t.Errorf("want ends_at value error, got %v", err)
	}
}

func TestValidateInitialAuctionDetailWrongTitle(t *testing.T) {
	d := seedDetail()
	d.Title = "changed"
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want title error, got nil")
	}
}

func TestValidateInitialAuctionDetailWrongStatus(t *testing.T) {
	d := seedDetail()
	d.Status = "closed"
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want status error, got nil")
	}
}

func TestValidateInitialAuctionDetailWrongBidCount(t *testing.T) {
	d := seedDetail()
	d.BidCount = 99 // フィールド単体の不一致(Bids配列は正しいまま)
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want bid_count error, got nil")
	}
}

func TestValidateInitialAuctionDetailWrongCategory(t *testing.T) {
	d := seedDetail()
	d.CategoryID = 99
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want category error, got nil")
	}
}
```

注: 既存の `TestValidateInitialAuctionDetailWrongBidCount`(Bidsを2件に切り詰めるもの)が存在する場合は `TestValidateInitialAuctionDetailTruncatedBids` にリネームして残し、上記の「フィールド単体の不一致」テストと共存させる。

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd bench && go test ./... -run 'TestValidateInitial' -v`
Expected: WrongEndsAtValue / WrongTitle / WrongStatus / WrongCategory がFAIL(検証未実装)。fixture修正によるコンパイルエラーが先に出る場合もある(CategoryIDは既存フィールドなのでコンパイルは通るはず)。

- [ ] **Step 3: validate.go を実装**

(a) `expectedAuction` と期待値テーブルを拡張:

```go
type expectedAuction struct {
	Title        string
	CurrentPrice int64
	BidCount     int64
	SellerID     int64
	CategoryID   int64
	EndsAtHour   int // ends_at = 2030-01-01 <hour>:00:00 UTC(シードの階段配置)
}

var expectedInitialAuctions = map[int64]expectedAuction{
	1:  {"ヘリテージ・ウィングチェア", 1500, 3, 1, 3, 1},
	2:  {"エルゴホスト Model E", 2100, 1, 2, 1, 2},
	3:  {"ISUレーサー GT", 3100, 1, 3, 2, 3},
	4:  {"メッシュフロー 40", 4100, 1, 4, 1, 4},
	5:  {"ミッドセンチュリー・ラウンジ", 2500, 0, 5, 3, 5},
	6:  {"ネオンストライク Z", 3000, 0, 6, 2, 6},
	7:  {"スタンドフレックス", 3500, 0, 7, 1, 7},
	8:  {"チャーチチェア 1920", 4000, 0, 8, 3, 8},
	9:  {"プロシート・エディション", 4500, 0, 9, 2, 9},
	10: {"コンパクトワーク 01", 5000, 0, 10, 1, 10},
}
```

(b) `ValidateInitialAuctionList` のループ内、ends_at非減少チェックの直後に絶対値照合を追加(既存の非減少チェックは残す):

```go
		if want := time.Date(2030, 1, 1, expectedInitialAuctions[a.ID].EndsAtHour, 0, 0, 0, time.UTC); !a.EndsAt.Equal(want) {
			return fmt.Errorf("auction %d: ends_at が %v (期待: %v)", a.ID, a.EndsAt, want)
		}
```

さらにカテゴリ照合を per-field チェック群に追加:

```go
		if a.CategoryID != want.CategoryID {
			return fmt.Errorf("auction %d: category_id が %d (期待: %d)", a.ID, a.CategoryID, want.CategoryID)
		}
```

(注意: `want := expectedInitialAuctions[a.ID]` の変数と衝突しないよう、ends_at照合は `wantEndsAt` 等の別名でもよい。コンパイルが通る形に整えること)

(c) `ValidateInitialAuctionDetail` の冒頭チェック群に追加(既存のdescription/starting_price/current_priceチェックの並びに):

```go
	if d.Title != "ヘリテージ・ウィングチェア" {
		return fmt.Errorf("auction 1: title が %q", d.Title)
	}
	if d.Status != "live" {
		return fmt.Errorf("auction 1: status が %q (期待: live)", d.Status)
	}
	if d.CategoryID != 3 {
		return fmt.Errorf("auction 1: category_id が %d (期待: 3)", d.CategoryID)
	}
	if d.BidCount != int64(len(expectedAuction1Bids)) {
		return fmt.Errorf("auction 1: bid_count が %d (期待: %d)", d.BidCount, len(expectedAuction1Bids))
	}
```

- [ ] **Step 4: scenario.go のPrepareに検証を追加**

(a) 「2b. シード詳細の検証」ブロックの直後に追加:

```go
	// 2c. closed / upcoming の詳細検証
	closedDetail, err := c.GetAuction(ctx, 11)
	if err != nil {
		return err
	}
	if closedDetail.Status != "closed" || closedDetail.CurrentPrice != 12000 || len(closedDetail.Bids) != 2 {
		return fmt.Errorf("auction 11: closed詳細が不正 (status=%q current_price=%d bids=%d, 期待: closed/12000/2)",
			closedDetail.Status, closedDetail.CurrentPrice, len(closedDetail.Bids))
	}
	if err := ValidateBidsOrdered(closedDetail.Bids); err != nil {
		return err
	}
	upcomingDetail, err := c.GetAuction(ctx, 12)
	if err != nil {
		return err
	}
	if upcomingDetail.Status != "upcoming" || upcomingDetail.CurrentPrice != 8000 || len(upcomingDetail.Bids) != 0 {
		return fmt.Errorf("auction 12: upcoming詳細が不正 (status=%q current_price=%d bids=%d, 期待: upcoming/8000/0)",
			upcomingDetail.Status, upcomingDetail.CurrentPrice, len(upcomingDetail.Bids))
	}
```

(b) 「4. 入札の検証」の too-low チェックを、bodyも検証する形に差し替える。既存の:

```go
	if _, code, err := seedClient.PostBid(ctx, auctionID, detail.CurrentPrice); err != nil {
		return err
	} else if code != 400 {
		return fmt.Errorf("POST /auctions/%d/bids: 現在価格以下の入札が status %d (期待: 400)", auctionID, code)
	}
```

を次に差し替え(doJSONは同一パッケージなので直接呼べる):

```go
	lowCode, lowBody, err := seedClient.doJSON(ctx, http.MethodPost,
		fmt.Sprintf("/auctions/%d/bids", auctionID), map[string]int64{"amount": detail.CurrentPrice})
	if err != nil {
		return err
	}
	if lowCode != 400 {
		return fmt.Errorf("POST /auctions/%d/bids: 現在価格以下の入札が status %d (期待: 400)", auctionID, lowCode)
	}
	var rejection struct {
		Error        string `json:"error"`
		CurrentPrice int64  `json:"current_price"`
	}
	if err := json.Unmarshal(lowBody, &rejection); err != nil {
		return fmt.Errorf("POST /auctions/%d/bids: too-low応答のJSONが不正: %w", auctionID, err)
	}
	if rejection.Error == "" || rejection.CurrentPrice != detail.CurrentPrice {
		return fmt.Errorf("POST /auctions/%d/bids: too-low応答bodyが不正 (%+v, 期待: current_price=%d)",
			auctionID, rejection, detail.CurrentPrice)
	}
```

(c) 「5. 未ログインの入札は401」ブロックの直後(最終re-initializeの前)に追加:

```go
	// 6. not-live オークションへの入札は400
	for _, id := range []int64{11, 12} {
		if _, code, err := seedClient.PostBid(ctx, id, 999999); err != nil {
			return err
		} else if code != 400 {
			return fmt.Errorf("POST /auctions/%d/bids: not-liveへの入札が status %d (期待: 400)", id, code)
		}
	}
```

import に `"encoding/json"` と `"net/http"` を追加。

- [ ] **Step 5: テストとビルド**

Run: `cd bench && go test ./... -v && go vet ./... && go build ./...`
Expected: 全PASS・クリーン。

- [ ] **Step 6: Commit**

```bash
git add bench
git commit -m "feat: Prepareにclosed/upcoming検証・not-live入札400・too-low body検証・ends_at厳密照合を追加"
```

---

### Task 2: 入札台帳(Ledger)とスコア定義

**Files:**
- Create: `bench/ledger.go`
- Create: `bench/score.go`
- Test: `bench/ledger_test.go`

**Interfaces:**
- Produces(Task 3〜5が依存):
  - `type AcceptedBid struct { BidID, AuctionID, UserID, Amount int64 }`
  - `NewLedger() *Ledger` / `(*Ledger) Record(b AcceptedBid)` / `(*Ledger) ByAuction() map[int64][]AcceptedBid`(コピーを返す) / `(*Ledger) MaxAmount(auctionID int64) (int64, bool)`
  - スコアタグ定数: `ScoreGETList` / `ScoreGETDetail` / `ScorePOSTBid`(`score.ScoreTag` 型)
  - `var scoreTable = map[score.ScoreTag]int64{...}`(list=1, detail=1, bid=5)
  - failureコード: `ErrCritical failure.StringCode = "critical"` / `ErrApplication failure.StringCode = "application"`
  - `const errorLimit = 100`(超えたらFail) / `const errorPenalty = 1`

- [ ] **Step 1: 失敗するテストを書く**

`bench/ledger_test.go`:

```go
package main

import (
	"sync"
	"testing"
)

func TestLedgerRecordAndQuery(t *testing.T) {
	l := NewLedger()
	l.Record(AcceptedBid{BidID: 1, AuctionID: 1, UserID: 5, Amount: 1600})
	l.Record(AcceptedBid{BidID: 2, AuctionID: 1, UserID: 6, Amount: 1700})
	l.Record(AcceptedBid{BidID: 3, AuctionID: 2, UserID: 5, Amount: 2200})

	byAuction := l.ByAuction()
	if len(byAuction[1]) != 2 || len(byAuction[2]) != 1 {
		t.Fatalf("byAuction = %+v", byAuction)
	}
	if max, ok := l.MaxAmount(1); !ok || max != 1700 {
		t.Errorf("MaxAmount(1) = %d, %v; want 1700, true", max, ok)
	}
	if _, ok := l.MaxAmount(99); ok {
		t.Error("MaxAmount(99) should be false")
	}
}

func TestLedgerConcurrentRecord(t *testing.T) {
	l := NewLedger()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l.Record(AcceptedBid{BidID: int64(i), AuctionID: int64(i % 5), UserID: 1, Amount: int64(1000 + i)})
		}(i)
	}
	wg.Wait()
	total := 0
	for _, bids := range l.ByAuction() {
		total += len(bids)
	}
	if total != 100 {
		t.Errorf("total = %d, want 100", total)
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd bench && go test ./... -run TestLedger -v`
Expected: コンパイルエラー(未定義)。

- [ ] **Step 3: 実装**

`bench/ledger.go`:

```go
package main

import "sync"

// AcceptedBid はベンチが201を受け取った入札の記録。Validationフェーズで実データと突合する。
type AcceptedBid struct {
	BidID     int64
	AuctionID int64
	UserID    int64
	Amount    int64
}

type Ledger struct {
	mu       sync.Mutex
	accepted map[int64][]AcceptedBid // auctionID -> bids
}

func NewLedger() *Ledger {
	return &Ledger{accepted: map[int64][]AcceptedBid{}}
}

func (l *Ledger) Record(b AcceptedBid) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.accepted[b.AuctionID] = append(l.accepted[b.AuctionID], b)
}

// ByAuction は記録のコピーを返す(以降のRecordの影響を受けない)。
func (l *Ledger) ByAuction() map[int64][]AcceptedBid {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[int64][]AcceptedBid, len(l.accepted))
	for id, bids := range l.accepted {
		cp := make([]AcceptedBid, len(bids))
		copy(cp, bids)
		out[id] = cp
	}
	return out
}

func (l *Ledger) MaxAmount(auctionID int64) (int64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	bids, ok := l.accepted[auctionID]
	if !ok || len(bids) == 0 {
		return 0, false
	}
	max := bids[0].Amount
	for _, b := range bids[1:] {
		if b.Amount > max {
			max = b.Amount
		}
	}
	return max, true
}
```

`bench/score.go`:

```go
package main

import (
	"github.com/isucon/isucandar/failure"
	"github.com/isucon/isucandar/score"
)

const (
	ScoreGETList   score.ScoreTag = "GET /auctions"
	ScoreGETDetail score.ScoreTag = "GET /auctions/:id"
	ScorePOSTBid   score.ScoreTag = "POST /auctions/:id/bids"
)

// 配点(スペック準拠: 入札が主役)
var scoreTable = map[score.ScoreTag]int64{
	ScoreGETList:   1,
	ScoreGETDetail: 1,
	ScorePOSTBid:   5,
}

const (
	// ErrCritical は整合性違反。1件でもFail。
	ErrCritical failure.StringCode = "critical"
	// ErrApplication は5xx・予期しない応答など。減点対象。
	ErrApplication failure.StringCode = "application"
)

const (
	errorLimit   = 100 // アプリエラーがこれを超えたらFail
	errorPenalty = 1   // エラー1件あたりの減点
)
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd bench && go test ./... -run TestLedger -v && go vet ./...`
Expected: PASS・クリーン。`go test -race ./... -run TestLedgerConcurrent` も1回実行しrace検出なしを確認。

- [ ] **Step 5: Commit**

```bash
git add bench/ledger.go bench/score.go bench/ledger_test.go
git commit -m "feat: 入札台帳とスコア/エラー分類の定義を追加"
```

---

### Task 3: main.go 再構成(duration/prepare-onlyフラグ、スコア出力)

**Files:**
- Modify: `bench/main.go`(全面書き換え)
- Modify: `bench/scenario.go`(Scenario構造体にフィールド追加のみ。Load/Validationの中身はTask 4/5)

**Interfaces:**
- Consumes: `scoreTable` / `ErrCritical` / `ErrApplication` / `errorLimit` / `errorPenalty` / `NewLedger`(Task 2)
- Produces:
  - `Scenario` 構造体: `Target string; PrepareOnly bool; Bidders int; Watchers int; Ledger *Ledger`
  - CLI: `-target`(既存) `-duration`(default 60s) `-prepare-only`(bool) `-bidders`(default 8) `-watchers`(default 4)
  - 出力: prepare-only時は従来の `PREPARE: PASS|FAIL`。通常時は下記フォーマット+exit code(PASS=0, FAIL=1)

```
SCORE: <total>  (raw <raw>, penalty <penalty>)
  GET /auctions            : <count>回 (<pt>点)
  GET /auctions/:id        : <count>回 (<pt>点)
  POST /auctions/:id/bids  : <count>回 (<pt>点)
ERRORS: <n>件 (critical: <m>件)
RESULT: PASS | FAIL
```

- [ ] **Step 1: scenario.go の構造体を拡張**

```go
// Scenario はISUBIDベンチのシナリオ。
type Scenario struct {
	Target      string
	PrepareOnly bool
	Bidders     int
	Watchers    int
	Ledger      *Ledger
}
```

既存の `Load` no-opの冒頭に(本実装はTask 4で入るが、この時点で):

```go
	if s.PrepareOnly {
		return nil
	}
```

- [ ] **Step 2: main.go を全面書き換え**

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/isucon/isucandar"
	"github.com/isucon/isucandar/failure"
)

func main() {
	target := flag.String("target", "http://localhost:8080", "ベンチ対象のベースURL")
	duration := flag.Duration("duration", 60*time.Second, "負荷走行時間")
	prepareOnly := flag.Bool("prepare-only", false, "Prepare(整合性チェック)のみ実行")
	bidders := flag.Int("bidders", 8, "入札者worker数")
	watchers := flag.Int("watchers", 4, "ウォッチャーworker数")
	flag.Parse()

	s := &Scenario{
		Target:      *target,
		PrepareOnly: *prepareOnly,
		Bidders:     *bidders,
		Watchers:    *watchers,
		Ledger:      NewLedger(),
	}

	b, err := isucandar.NewBenchmark(
		isucandar.WithoutPanicRecover(),
		isucandar.WithLoadTimeout(*duration),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b.AddScenario(s)

	result := b.Start(context.Background())

	for tag, mag := range scoreTable {
		result.Score.Set(tag, mag)
	}

	errs := result.Errors.All()
	criticalCount := 0
	appCount := 0
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "ERR: %v\n", e)
		if failure.IsCode(e, ErrCritical) {
			criticalCount++
		} else {
			appCount++
		}
	}

	if *prepareOnly {
		if len(errs) > 0 {
			fmt.Println("PREPARE: FAIL")
			os.Exit(1)
		}
		fmt.Println("PREPARE: PASS")
		return
	}

	raw := result.Score.Sum()
	penalty := int64(len(errs) * errorPenalty)
	total := raw - penalty
	if total < 0 {
		total = 0
	}

	fmt.Printf("SCORE: %d  (raw %d, penalty %d)\n", total, raw, penalty)
	breakdown := result.Score.Breakdown()
	for _, st := range []struct {
		tag  string
		name string
	}{
		{string(ScoreGETList), "GET /auctions"},
		{string(ScoreGETDetail), "GET /auctions/:id"},
		{string(ScorePOSTBid), "POST /auctions/:id/bids"},
	} {
		count := breakdown[score.ScoreTag(st.tag)]
		pt := count * scoreTable[score.ScoreTag(st.tag)]
		fmt.Printf("  %-25s: %d回 (%d点)\n", st.name, count, pt)
	}
	fmt.Printf("ERRORS: %d件 (critical: %d件)\n", len(errs), criticalCount)

	pass := criticalCount == 0 && appCount <= errorLimit && total > 0
	if pass {
		fmt.Println("RESULT: PASS")
		return
	}
	fmt.Println("RESULT: FAIL")
	os.Exit(1)
}
```

注意:
- `score` パッケージのimport(`github.com/isucon/isucandar/score`)が必要。`Breakdown()` の戻り型・`failure.IsCode` のシグネチャはisucandarの実APIに合わせて最小適応すること(`go doc github.com/isucon/isucandar/score Score` / `go doc github.com/isucon/isucandar/failure IsCode` で確認)。名前が違ってもcount×配点の合算ロジックは変えない
- prepare-only判定はエラー出力の後に置く(FAIL理由が見えるように)

- [ ] **Step 3: ビルドとPrepare互換確認**

```bash
cd bench && go build ./... && go vet ./... && go test ./...
docker compose -f ../dev/compose.yaml up -d --build
sleep 5
go run . -target http://localhost:8080 -prepare-only
```

Expected: `PREPARE: PASS`(従来互換)。`go run . -target http://localhost:9999 -prepare-only` は `PREPARE: FAIL` + exit 1。

- [ ] **Step 4: Commit**

```bash
git add bench
git commit -m "feat: ベンチCLIにduration/prepare-only/スコア出力を追加"
```

---

### Task 4: Loadフェーズ(入札者/ウォッチャーシナリオ)

**Files:**
- Create: `bench/load.go`(入札者・ウォッチャーのイテレーション実装)
- Modify: `bench/scenario.go`(Loadを本実装に置き換え)

**Interfaces:**
- Consumes: `Client` / `Ledger` / スコアタグ / `ErrCritical` / `ErrApplication`(Task 2-3)
- Produces: `(s *Scenario) bidderIteration(ctx, step)` / `(s *Scenario) watcherIteration(ctx, step)`(Task 5のValidationは `s.Ledger` を読む)

- [ ] **Step 1: load.go を実装**

```go
package main

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/isucon/isucandar"
	"github.com/isucon/isucandar/failure"
)

// seedUserName はシードユーザー(1..20)のログイン名を返す。パスワードは全員 'password'。
func seedUserName() string {
	return fmt.Sprintf("seed_user_%02d", rand.Intn(20)+1)
}

// bidderIteration は「ログイン→一覧→詳細→入札(競り負けたら再挑戦)」の1セッション。
func (s *Scenario) bidderIteration(ctx context.Context, step *isucandar.BenchmarkStep) {
	c, err := NewClient(s.Target)
	if err != nil {
		step.AddError(failure.NewError(ErrApplication, err))
		return
	}
	user, err := c.Login(ctx, seedUserName(), "password")
	if err != nil {
		step.AddError(failure.NewError(ErrApplication, err))
		return
	}
	list, err := c.GetAuctions(ctx)
	if err != nil {
		step.AddError(failure.NewError(ErrApplication, err))
		return
	}
	step.AddScore(ScoreGETList)
	if len(list) == 0 {
		step.AddError(failure.NewError(ErrCritical, fmt.Errorf("GET /auctions: 開催中オークションが0件")))
		return
	}
	target := list[rand.Intn(len(list))]

	// 競り負け(400 too-low)たら現在価格を取り直して上乗せ。最大5回。
	for attempt := 0; attempt < 5; attempt++ {
		d, err := c.GetAuction(ctx, target.ID)
		if err != nil {
			step.AddError(failure.NewError(ErrApplication, err))
			return
		}
		step.AddScore(ScoreGETDetail)
		amount := d.CurrentPrice + 100 + rand.Int63n(400)
		bid, code, err := c.PostBid(ctx, target.ID, amount)
		if err != nil {
			step.AddError(failure.NewError(ErrApplication, err))
			return
		}
		switch code {
		case 201:
			if bid.UserID != user.ID || bid.Amount != amount {
				step.AddError(failure.NewError(ErrCritical,
					fmt.Errorf("POST /auctions/%d/bids: 応答内容が不一致 (got user=%d amount=%d, want user=%d amount=%d)",
						target.ID, bid.UserID, bid.Amount, user.ID, amount)))
				return
			}
			s.Ledger.Record(AcceptedBid{BidID: bid.ID, AuctionID: target.ID, UserID: user.ID, Amount: amount})
			step.AddScore(ScorePOSTBid)
			return
		case 400:
			continue // 競り負け。取り直して再入札
		default:
			step.AddError(failure.NewError(ErrApplication,
				fmt.Errorf("POST /auctions/%d/bids: 予期しない status %d", target.ID, code)))
			return
		}
	}
	// 5連敗は人気オークションなら起こりうる。エラーにしない。
}

// watcherIteration は「一覧→ランダム詳細+不変条件チェック」の回遊。
func (s *Scenario) watcherIteration(ctx context.Context, step *isucandar.BenchmarkStep) {
	c, err := NewClient(s.Target)
	if err != nil {
		step.AddError(failure.NewError(ErrApplication, err))
		return
	}
	list, err := c.GetAuctions(ctx)
	if err != nil {
		step.AddError(failure.NewError(ErrApplication, err))
		return
	}
	step.AddScore(ScoreGETList)
	for _, a := range list {
		if a.Status != "live" {
			step.AddError(failure.NewError(ErrCritical,
				fmt.Errorf("GET /auctions: live以外が混入 (id=%d status=%q)", a.ID, a.Status)))
			return
		}
	}
	if len(list) == 0 {
		return
	}
	d, err := c.GetAuction(ctx, list[rand.Intn(len(list))].ID)
	if err != nil {
		step.AddError(failure.NewError(ErrApplication, err))
		return
	}
	step.AddScore(ScoreGETDetail)
	if err := ValidateBidsOrdered(d.Bids); err != nil {
		step.AddError(failure.NewError(ErrCritical, fmt.Errorf("auction %d: %w", d.ID, err)))
		return
	}
	if int64(len(d.Bids)) != d.BidCount {
		step.AddError(failure.NewError(ErrCritical,
			fmt.Errorf("auction %d: bid_count %d と bids件数 %d が不一致", d.ID, d.BidCount, len(d.Bids))))
	}
}
```

注: context キャンセル(Load終了時)由来のエラーはエラーとして数えたくない。各 `step.AddError` の前に `if ctx.Err() != nil { return }` を入れること(上記コードに追記する形で全AddError箇所に適用。ヘルパー `func addErr(ctx context.Context, step *isucandar.BenchmarkStep, code failure.StringCode, err error)` を作って一元化してよい)。

- [ ] **Step 2: scenario.go のLoadを本実装に置き換え**

no-opの `Load` を以下に差し替える(コメントの「削除しないこと」経緯はValidation側に残すか、このLoadが本実装になった旨に更新):

```go
func (s *Scenario) Load(ctx context.Context, step *isucandar.BenchmarkStep) error {
	if s.PrepareOnly {
		return nil
	}
	bidder, err := worker.NewWorker(func(ctx context.Context, _ int) {
		s.bidderIteration(ctx, step)
	}, worker.WithInfinityLoop(), worker.WithMaxParallelism(int32(s.Bidders)))
	if err != nil {
		return err
	}
	watcher, err := worker.NewWorker(func(ctx context.Context, _ int) {
		s.watcherIteration(ctx, step)
	}, worker.WithInfinityLoop(), worker.WithMaxParallelism(int32(s.Watchers)))
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); bidder.Process(ctx) }()
	go func() { defer wg.Done(); watcher.Process(ctx) }()
	wg.Wait()
	return nil
}
```

import に `"sync"` と `"github.com/isucon/isucandar/worker"` を追加。`WithMaxParallelism` の引数型(int32/int)は実APIに合わせること。

- [ ] **Step 3: ビルド確認と短時間走行**

```bash
cd bench && go build ./... && go vet ./... && go test ./...
docker compose -f ../dev/compose.yaml up -d --build && sleep 5
go run . -target http://localhost:8080 -duration 10s
```

Expected: 10秒走行後、SCORE行(0より大)・タグ別内訳・`RESULT: PASS` が出て exit 0。criticalエラーが出た場合は入札検証ロジックのバグを疑い調査する(アプリはFOR UPDATE直列化で正しいはず)。

- [ ] **Step 4: Commit**

```bash
git add bench
git commit -m "feat: Loadフェーズ(入札者/ウォッチャーシナリオ)を実装"
```

---

### Task 5: Validationフェーズ(台帳突合)と統合確認

**Files:**
- Modify: `bench/scenario.go`(Validation本実装)
- Modify: `README.md`(ベンチ使用方法の更新)
- Modify: `docs/phase2-notes.md`(2b-1完了の反映)

**Interfaces:**
- Consumes: `Ledger.ByAuction()` / `expectedInitialAuctions` / `ValidateBidsOrdered`

- [ ] **Step 1: Validationを本実装に置き換え**

```go
// Validation はLoad終了後に台帳と実データを突合する。
// ベンチ以外に入札者はいないため、各オークションの期待状態は
// 「シード入札 + 台帳の受理入札」で完全に決まる。
func (s *Scenario) Validation(ctx context.Context, step *isucandar.BenchmarkStep) error {
	if s.PrepareOnly {
		return nil
	}
	c, err := NewClient(s.Target)
	if err != nil {
		return err
	}
	for auctionID, accepted := range s.Ledger.ByAuction() {
		d, err := c.GetAuction(ctx, auctionID)
		if err != nil {
			step.AddError(failure.NewError(ErrCritical, err))
			continue
		}
		byID := make(map[int64]Bid, len(d.Bids))
		for _, b := range d.Bids {
			byID[b.ID] = b
		}
		for _, ab := range accepted {
			got, ok := byID[ab.BidID]
			if !ok {
				step.AddError(failure.NewError(ErrCritical,
					fmt.Errorf("auction %d: 受理された入札 id=%d が消失", auctionID, ab.BidID)))
				continue
			}
			if got.Amount != ab.Amount || got.User.ID != ab.UserID {
				step.AddError(failure.NewError(ErrCritical,
					fmt.Errorf("auction %d: 入札 id=%d の内容が改変 (got amount=%d user=%d, want amount=%d user=%d)",
						auctionID, ab.BidID, got.Amount, got.User.ID, ab.Amount, ab.UserID)))
			}
		}
		// current_price = max(シード時点の現在価格, 台帳の最大受理額)
		expected := expectedInitialAuctions[auctionID].CurrentPrice
		if max, ok := s.Ledger.MaxAmount(auctionID); ok && max > expected {
			expected = max
		}
		if d.CurrentPrice != expected {
			step.AddError(failure.NewError(ErrCritical,
				fmt.Errorf("auction %d: current_price が %d (期待: %d)", auctionID, d.CurrentPrice, expected)))
		}
		if err := ValidateBidsOrdered(d.Bids); err != nil {
			step.AddError(failure.NewError(ErrCritical, fmt.Errorf("auction %d: %w", auctionID, err)))
		}
	}
	return nil
}
```

注: `expectedInitialAuctions[auctionID]` はゼロ値アクセスの危険がある(台帳のauctionIDはliveの1〜10のみのはずだが、mapに無いidならCurrentPrice=0となり誤検知しうる)。`want, ok := expectedInitialAuctions[auctionID]; if !ok { critical(想定外のauctionに入札が受理された) }` とする方が安全 — この形で実装すること。

- [ ] **Step 2: 統合確認(60秒フル走行)**

```bash
docker compose -f dev/compose.yaml up -d --build && sleep 5
cd bench && go run . -target http://localhost:8080
```

Expected: 60秒走行 → SCORE > 0、critical 0件、`RESULT: PASS`、exit 0。出力(スコアとタグ別内訳)をレポートに記録。

- [ ] **Step 3: ベンチのベンチ(整合性違反でFAILすることの実証)**

一時的に `webapp/go/bids.go` の `FOR UPDATE` を外して(`" FROM auctions WHERE id = ? FOR UPDATE"` → `" FROM auctions WHERE id = ?"`)ビルド・起動し、`-duration 15s -bidders 16` で走らせて監査に引っかかるか観察する:

```bash
# 変更 → docker compose up -d --build → bench実行
# 期待: 単調増加違反(同額以下の受理)がValidation/入札応答検証でcriticalになりRESULT: FAILする
#       (レース頻度依存のため、1回でFAILしなければ2〜3回試行してよい。
#        それでもPASSしてしまう場合は「検出できなかった」事実をレポートに正直に記録)
# 最後に git checkout webapp/go/bids.go → 再ビルド → フル走行PASSを再確認
```

working treeがクリーンであることを `git status` で確認。

- [ ] **Step 4: README と phase2-notes を更新**

`README.md` のクイックスタートのベンチ実行部分を差し替え:

```markdown
# 2. ベンチ実行(60秒の負荷走行+整合性検証)
cd bench && go run . -target http://localhost:8080

# 整合性チェックのみ(負荷なし)
go run . -target http://localhost:8080 -prepare-only
```

「## ステータス」を「Phase 2b-1(負荷走行・スコアリング)まで完了。初期データジェネレータ・pub/sub要素・フロントエンドは今後のPhase」に更新。

`docs/phase2-notes.md` の「Phase 2b バックログ」のPrepare系項目に `[済]` を付け、`WithLoadTimeout` 項目にも `[済]` を付ける。

- [ ] **Step 5: 最終テストとCommit**

```bash
cd webapp/go && go test -count=1 ./... && cd ../../bench && go test -count=1 ./...
git add bench README.md docs/phase2-notes.md
git commit -m "feat: Validationフェーズ(台帳突合)を実装しREADMEを更新"
```

---

## 完了条件(Phase 2b-1)

- `bench` フル走行(60秒)で SCORE > 0・`RESULT: PASS`・exit 0
- `-prepare-only` で従来の `PREPARE: PASS` 互換
- FOR UPDATE を外した実装への走行で整合性違反が検出される(または検出できなかった事実が記録されている)
- webapp/ への変更は承認済みの1件のみ(`GET /auctions/:id` の読み取りをトランザクションで一貫化。実走行で bid_count と bids 件数の不整合レースが検出されたための正当な修正。意図的なボトルネック=N+1等はトランザクション内にそのまま温存)
- 両モジュール `go test -count=1 ./...` 全PASS

## Phase 2b-2(次計画)に残すもの

- 初期データジェネレータ(ユーザー/オークション大量生成、id順とends_at順の非相関化、スナップショットJSON駆動の期待値)
- 出品者シナリオ・オークション終了処理(live→closed遷移)とその検証 — スペックのPhase 3要素と合流
- 負荷の段階増加(スコア連動の並列数調整)
