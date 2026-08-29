# ISUBID Phase 3（pub/sub要素）実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 走行中に live→closed 遷移が起き、入札フィード・通知ファンアウト・落札確定が動くようにし、ベンチがそれらの整合性（落札者の正しさ・フィード反映2秒・通知欠落）を検証してスコアを出せるようにする。

**Architecture:** 参照実装に4つのAPI（出品・入札フィード・通知一覧・売上集計）とアプリ内終了処理バッチを足し、`POST /initialize` が `ends_at` を初期化時刻からの相対値で書き換えることで60秒走行中にオークションが閉じるようにする。ベンチ側は既存の pending-intent 台帳を拡張し、検証ロジックは純粋関数として切り出してテーブル駆動テストで固める。

**Tech Stack:** Go 1.26 / chi v5 / sqlx / MySQL 8 / isucandar（`worker`・`score`・`failure`・`pubsub`）/ Docker Compose

**Spec:** `docs/superpowers/specs/2026-08-29-isubid-phase3-pubsub-design.md`

## Global Constraints

- **意図的な遅さは仕様である。** 新規に足すクエリもインデックスを張らず、N+1 を解消せず、バルク化しない。`docs/phase2-notes.md` の「仕込みインベントリ」に追記すること
- **エラーボディ形式は `{"error": "..."}`** を守る（既存の `writeError` を使う）
- **`ends_at` オフセットの正は `webapp/go/initialize.go` の `auctionEndOffsets`。** ベンチ側 `bench/validate.go` の期待値はそれに追従する
- **ベンチの入札者・出品者はシードユーザー（`seed_user_01`〜`seed_user_20`、パスワードは全員 `password`）のみを使う。** 落札者から `seed_user_%02d` でログイン名を逆引きする検証がこれに依存する
- **走行中にオークションが閉じるため、`POST /auctions/:id/bids` の 400 は正常動作。** critical に分類しない
- **ctx キャンセル起因の false-FAIL を作らない。** 「〜秒以内に現れない」系の critical を上げる前に `ctx.Err() != nil` を確認する
- **テスト実行前提:** `docker compose -f dev/compose.yaml up -d mysql`（webapp のテストは実 MySQL を使う）

## 仕様からの意図的な差分

計画を起こす過程で、spec の記述より単純または厳密にできると判明した3点。実装はこちらに従う。

1. **台帳に outbid イベント / フィード反映観測を別途記録しない。** spec は台帳の拡張として
   この2つを挙げていたが、outbid の期待値は既存の確定受理台帳（`AcceptedBid` の `BidID` 順）
   から導出でき（Task 15）、フィード反映は入札直後にその場でポーリングして判定できる（Task 10）。
   保持する状態を増やさないぶん、検証ロジックを純粋関数に閉じ込めやすい
2. **「通知が自分宛のみ」の検証方法を変えた。** 通知レスポンスに `user_id` を含めない設計に
   したため、返ってきた通知が自分宛かをベンチが直接確認できない。代わりに Validation で
   「一度も入札していない新規ユーザーの通知が0件であること」を確認する（Task 16）。
   `WHERE user_id = ?` を落とす改変を決定的に検出できる
3. **「`ends_at` を過ぎた auction は closed」検証を追加した。** spec の検証項目表には無いが、
   これが無いと終了処理バッチを止めても「closed が1件も無い」だけで落札照合が素通りし、
   バッチ停止を検出できない（Task 6・Task 7 で実証する）

## File Structure

**新規作成**

| ファイル | 責務 |
|---|---|
| `webapp/go/closer.go` | 終了処理バッチ（`closeAuction` 1件処理 / `closeDueAuctions` 1周 / `runAuctionCloser` ループ） |
| `webapp/go/closer_test.go` | 上記のテスト |
| `webapp/go/feed.go` | `GET /auctions/:id/bids?since=` |
| `webapp/go/feed_test.go` | 同テスト |
| `webapp/go/notifications.go` | `GET /notifications` |
| `webapp/go/notifications_test.go` | 同テスト |
| `webapp/go/stats.go` | `GET /stats/me` |
| `webapp/go/stats_test.go` | 同テスト |
| `bench/notify.go` | 通知検証の純粋関数（`ExpectedOutbidCounts` ほか） |
| `bench/notify_test.go` | 同テスト |
| `docs/phase3-notes.md` | Phase 3 の設計判断・ベンチのベンチ実測記録 |

**変更**

| ファイル | 変更内容 |
|---|---|
| `webapp/go/initialize.go` | `ends_at` の相対時刻書き換え |
| `webapp/go/main.go` | ルート追加、`routerFor` 分離、closer goroutine 起動 |
| `webapp/go/auctions.go` | `winner_id`/`winning_price` の露出、`POST /auctions` |
| `webapp/go/bids.go` | outbid ファンアウト |
| `bench/model.go` | `AuctionDetail` に `WinnerID`/`WinningPrice`、`Notification` 型 |
| `bench/client.go` | `GetBidFeed` / `GetNotifications` / `PostAuction` |
| `bench/validate.go` | `ends_at` の相対照合、フィード・通知の順序検証、`ValidateAuctionClosedIfDue` |
| `bench/reconcile.go` | `reconcileClosedAuction` |
| `bench/ledger.go` | 出品台帳（`RecordListing`/`Listings`） |
| `bench/load.go` | closed の正常系扱い、フィードポーリング、通知確認・出品者シナリオ |
| `bench/scenario.go` | Prepare の相対照合、Validation の落札・通知照合、Load のワーカー追加 |
| `bench/score.go` | スコアタグ3種の追加 |
| `bench/main.go` | `-sellers`/`-notifiers` フラグ、breakdown 出力 |

---

## 3-1: 時間軸 + 終了処理 + 落札 Validation

### Task 1: `/initialize` の相対時刻書き換え

**Files:**
- Modify: `webapp/go/initialize.go`
- Test: `webapp/go/initialize_test.go`

**Interfaces:**
- Consumes: なし
- Produces: `var auctionEndOffsets map[int64]int`（auction id → 初期化時刻からの `ends_at` オフセット秒）。Task 2 のベンチ期待値がこの表に追従する

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/initialize_test.go` に追記:

```go
// initialize 後、live オークションの ends_at は初期化時刻からの相対配置になる。
// ends_at 昇順が id 昇順と一致しないこと（ORDER BY id ASC と区別可能であること）を含めて検証する。
func TestInitializeSetsRelativeEndsAt(t *testing.T) {
	ts := newTestServer(t)
	before := time.Now().UTC()
	initApp(t, ts)
	after := time.Now().UTC()

	res, err := http.Get(ts.URL + "/auctions")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var list []auctionSummaryJSON
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}

	wantOrder := []int64{4, 2, 8, 6, 10, 1, 3, 5, 7, 9}
	if len(list) != len(wantOrder) {
		t.Fatalf("len = %d, want %d", len(list), len(wantOrder))
	}
	for i, want := range wantOrder {
		if list[i].ID != want {
			t.Errorf("list[%d].ID = %d, want %d (ends_at ASC の期待順序)", i, list[i].ID, want)
		}
	}
	for _, a := range list {
		off, ok := auctionEndOffsets[a.ID]
		if !ok {
			t.Fatalf("auction %d が auctionEndOffsets にない", a.ID)
		}
		// ends_at は [before+off, after+off] の範囲に入るはず
		lo := before.Add(time.Duration(off) * time.Second)
		hi := after.Add(time.Duration(off) * time.Second)
		if a.EndsAt.Before(lo.Add(-time.Second)) || a.EndsAt.After(hi.Add(time.Second)) {
			t.Errorf("auction %d: ends_at = %v, want in [%v, %v]", a.ID, a.EndsAt, lo, hi)
		}
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd webapp/go && go test -run TestInitializeSetsRelativeEndsAt ./...`
Expected: コンパイルエラー `undefined: auctionEndOffsets`

- [ ] **Step 3: 実装する**

`webapp/go/initialize.go` を編集。`import` に `"time"` を追加し、以下を追記:

```go
// auctionEndOffsets は初期化時刻からの ends_at オフセット(秒)。
// ends_at 昇順が id 昇順と一致しないよう意図的に非相関に配置してある
// (一覧の ORDER BY ends_at ASC を ORDER BY id ASC に書き換えるとベンチが落ちる)。
// +12〜+44 の5件は60秒走行中に closed へ遷移し、+3600〜+3840 の5件は走行を通して live のまま残る。
// ベンチ側の期待値(bench/validate.go)はこの表が正。
var auctionEndOffsets = map[int64]int{
	4: 12, 2: 20, 8: 28, 6: 36, 10: 44,
	1: 3600, 3: 3660, 5: 3720, 7: 3780, 9: 3840,
}

const (
	// upcoming(auction 12)は走行中ずっと upcoming のまま維持する。
	upcomingStartOffset = 300
	upcomingEndOffset   = 600
)

// applyRelativeSchedule は seed 投入後のオークション時刻を base 基準の相対値へ書き換える。
// base を1つに固定することで、ベンチが「初期化時刻 + オフセット」で期待値を組み立てられる。
func applyRelativeSchedule(ctx context.Context, db *sqlx.DB, base time.Time) error {
	for id, off := range auctionEndOffsets {
		if _, err := db.ExecContext(ctx,
			"UPDATE auctions SET starts_at = DATE_SUB(?, INTERVAL 1 HOUR), ends_at = DATE_ADD(?, INTERVAL ? SECOND) WHERE id = ?",
			base, base, off, id); err != nil {
			return err
		}
	}
	if _, err := db.ExecContext(ctx,
		"UPDATE auctions SET starts_at = DATE_ADD(?, INTERVAL ? SECOND), ends_at = DATE_ADD(?, INTERVAL ? SECOND) WHERE id = 12",
		base, upcomingStartOffset, base, upcomingEndOffset); err != nil {
		return err
	}
	return nil
}
```

`postInitialize` の SQL 投入ループ直後（`writeJSON` の前）に以下を挿入:

```go
	if err := applyRelativeSchedule(r.Context(), db, time.Now().UTC()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
```

`import` に `"context"` と `"time"`、`"github.com/jmoiron/sqlx"`（既にある）が必要。

- [ ] **Step 4: テストが通ることを確認**

Run: `cd webapp/go && go test -run 'TestInitialize' ./...`
Expected: PASS

- [ ] **Step 5: 既存テストの破損を確認して直す**

Run: `cd webapp/go && go test ./...`
Expected: `TestGetAuctions` など ends_at の絶対値や id 順に依存したテストが落ちる可能性がある。落ちたものは新しい順序（`4,2,8,6,10,1,3,5,7,9`）に合わせて修正する。`byID` マップ経由で照合しているテストは影響を受けない。

- [ ] **Step 6: コミット**

```bash
git add webapp/go/initialize.go webapp/go/initialize_test.go webapp/go/auctions_test.go
git commit -m "feat: initializeでends_atを初期化時刻基準の相対値に書き換える"
```

---

### Task 2: Prepare の `ends_at` 照合を相対オフセット方式へ変更

**Files:**
- Modify: `bench/validate.go`, `bench/scenario.go`
- Test: `bench/validate_test.go`

**Interfaces:**
- Consumes: `auctionEndOffsets`（Task 1、webapp 側の表。bench 側に同じ値を写経する）
- Produces: `func ValidateInitialAuctionList(list []AuctionSummary, base time.Time) error`（シグネチャ変更）、`var initialAuctionOrder []int64`、`const endsAtTolerance`

- [ ] **Step 1: 失敗するテストを書く**

`bench/validate_test.go` に追記:

```go
// ends_at は「initialize 応答受信時刻 + オフセット」± 許容幅で照合する。
func TestValidateInitialAuctionListRelativeEndsAt(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	build := func(mutate func(list []AuctionSummary)) []AuctionSummary {
		list := make([]AuctionSummary, 0, len(initialAuctionOrder))
		for _, id := range initialAuctionOrder {
			w := expectedInitialAuctions[id]
			list = append(list, AuctionSummary{
				ID:           id,
				Title:        w.Title,
				CategoryID:   w.CategoryID,
				Seller:       User{ID: w.SellerID, Name: "seed_user_" + pad2(w.SellerID)},
				CurrentPrice: w.CurrentPrice,
				BidCount:     w.BidCount,
				EndsAt:       base.Add(time.Duration(w.EndsAtOffset) * time.Second),
				Status:       "live",
			})
		}
		if mutate != nil {
			mutate(list)
		}
		return list
	}

	if err := ValidateInitialAuctionList(build(nil), base); err != nil {
		t.Fatalf("正しい一覧が拒否された: %v", err)
	}

	// 許容幅の内側(3秒ずれ)は通る
	if err := ValidateInitialAuctionList(build(func(l []AuctionSummary) {
		l[0].EndsAt = l[0].EndsAt.Add(3 * time.Second)
	}), base); err != nil {
		t.Errorf("許容幅内のずれが拒否された: %v", err)
	}

	// 許容幅の外側(30秒ずれ)は落ちる
	if err := ValidateInitialAuctionList(build(func(l []AuctionSummary) {
		l[0].EndsAt = l[0].EndsAt.Add(30 * time.Second)
	}), base); err == nil {
		t.Error("許容幅外のずれが検出されなかった")
	}

	// id 昇順に並べ替えたもの(= ORDER BY id ASC 相当)は落ちる
	if err := ValidateInitialAuctionList(build(func(l []AuctionSummary) {
		sort.Slice(l, func(i, j int) bool { return l[i].ID < l[j].ID })
	}), base); err == nil {
		t.Error("ORDER BY id ASC 相当の並びが検出されなかった")
	}
}
```

`import` に `"sort"` と `"time"` が必要。

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd bench && go test -run TestValidateInitialAuctionListRelativeEndsAt ./...`
Expected: コンパイルエラー（`initialAuctionOrder` 未定義、`EndsAtOffset` 未定義、`ValidateInitialAuctionList` の引数不一致）

- [ ] **Step 3: 実装する**

`bench/validate.go` の `expectedAuction` 構造体の `EndsAtHour int` フィールドを `EndsAtOffset int` に差し替え、値を webapp の `auctionEndOffsets` に合わせる:

```go
// expectedAuction は webapp/sql/90_seed_phase1.sql と
// webapp/go/initialize.go の auctionEndOffsets に一致させること(あちらが正)。
type expectedAuction struct {
	Title        string
	CurrentPrice int64
	BidCount     int64
	SellerID     int64
	CategoryID   int64
	EndsAtOffset int // ends_at = initialize時刻 + このオフセット(秒)
}

var expectedInitialAuctions = map[int64]expectedAuction{
	1:  {"ヘリテージ・ウィングチェア", 1500, 3, 1, 3, 3600},
	2:  {"エルゴホスト Model E", 2100, 1, 2, 1, 20},
	3:  {"ISUレーサー GT", 3100, 1, 3, 2, 3660},
	4:  {"メッシュフロー 40", 4100, 1, 4, 1, 12},
	5:  {"ミッドセンチュリー・ラウンジ", 2500, 0, 5, 3, 3720},
	6:  {"ネオンストライク Z", 3000, 0, 6, 2, 36},
	7:  {"スタンドフレックス", 3500, 0, 7, 1, 3780},
	8:  {"チャーチチェア 1920", 4000, 0, 8, 3, 28},
	9:  {"プロシート・エディション", 4500, 0, 9, 2, 3840},
	10: {"コンパクトワーク 01", 5000, 0, 10, 1, 44},
}

// initialAuctionOrder は ends_at 昇順に並べた期待 id 列。id 昇順と一致しないことが重要
// (一致していると ORDER BY id ASC への書き換えを検出できない)。
var initialAuctionOrder = []int64{4, 2, 8, 6, 10, 1, 3, 5, 7, 9}

// endsAtTolerance は ends_at 照合の許容幅。ベンチが initialize の応答を受け取った時刻を
// base とするが、アプリが基準時刻を採ったのはその少し前なので、初期化処理の所要時間ぶんの
// ずれを吸収する(Phase 2b-1 時点の Prepare 実測は 0.6〜1.2 秒)。
const endsAtTolerance = 5 * time.Second
```

`ValidateInitialAuctionList` を書き換える:

```go
func ValidateInitialAuctionList(list []AuctionSummary, base time.Time) error {
	if len(list) != len(expectedInitialAuctions) {
		return fmt.Errorf("GET /auctions: 件数が %d (期待: %d)", len(list), len(expectedInitialAuctions))
	}
	var prevEndsAt time.Time
	for i, a := range list {
		if a.ID != initialAuctionOrder[i] {
			return fmt.Errorf("GET /auctions: %d番目が id=%d (期待: id=%d / ends_at ASC順)", i, a.ID, initialAuctionOrder[i])
		}
		if a.EndsAt.Before(prevEndsAt) {
			return fmt.Errorf("GET /auctions: ends_at が昇順でない (id=%d)", a.ID)
		}
		prevEndsAt = a.EndsAt
		want := expectedInitialAuctions[a.ID]
		wantEndsAt := base.Add(time.Duration(want.EndsAtOffset) * time.Second)
		if d := a.EndsAt.Sub(wantEndsAt); d > endsAtTolerance || d < -endsAtTolerance {
			return fmt.Errorf("auction %d: ends_at が %v (期待: %v ± %v)", a.ID, a.EndsAt, wantEndsAt, endsAtTolerance)
		}
		if a.Status != "live" {
			return fmt.Errorf("auction %d: status が %q (期待: live)", a.ID, a.Status)
		}
		if a.Title != want.Title {
			return fmt.Errorf("auction %d: title が %q (期待: %q)", a.ID, a.Title, want.Title)
		}
		if a.CurrentPrice != want.CurrentPrice {
			return fmt.Errorf("auction %d: current_price が %d (期待: %d)", a.ID, a.CurrentPrice, want.CurrentPrice)
		}
		if a.BidCount != want.BidCount {
			return fmt.Errorf("auction %d: bid_count が %d (期待: %d)", a.ID, a.BidCount, want.BidCount)
		}
		if a.CategoryID != want.CategoryID {
			return fmt.Errorf("auction %d: category_id が %d (期待: %d)", a.ID, a.CategoryID, want.CategoryID)
		}
		if a.Seller.ID != want.SellerID || a.Seller.Name != "seed_user_"+pad2(want.SellerID) {
			return fmt.Errorf("auction %d: seller が %+v (期待: id=%d)", a.ID, a.Seller, want.SellerID)
		}
	}
	return nil
}
```

`bench/scenario.go` の `Prepare` で、最初の `Initialize` 直後に base を採り、呼び出しを差し替える:

```go
	lang, err := c.Initialize(ctx)
	if err != nil {
		return err
	}
	// アプリが基準時刻を採ったのは応答を受け取る直前。ここを base とし、
	// 初期化処理の所要時間ぶんのずれは endsAtTolerance が吸収する。
	base := time.Now().UTC()
	if lang == "" {
		return fmt.Errorf("POST /initialize: lang が空")
	}
```

そして `ValidateInitialAuctionList(list)` を `ValidateInitialAuctionList(list, base)` に変更する。

- [ ] **Step 4: テストが通ることを確認**

Run: `cd bench && go test ./...`
Expected: PASS

- [ ] **Step 5: 実アプリに対して Prepare を通す**

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
cd bench && go run . -target http://localhost:8080 -prepare-only
```
Expected: `PREPARE: PASS`

- [ ] **Step 6: コミット**

```bash
git add bench/validate.go bench/validate_test.go bench/scenario.go
git commit -m "feat: Prepareのends_at照合を初期化時刻基準の相対オフセット方式に変更"
```

---

### Task 3: 詳細レスポンスに `winner_id` / `winning_price` を露出

**Files:**
- Modify: `webapp/go/auctions.go`, `bench/model.go`
- Test: `webapp/go/auctions_test.go`

**Interfaces:**
- Consumes: なし
- Produces: `auctionDetail.WinnerID *int64` / `WinningPrice *int64`（webapp）、`AuctionDetail.WinnerID *int64` / `WinningPrice *int64`（bench）。Task 6 の落札照合がこれを使う

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/auctions_test.go` に追記（`auctionDetailJSON` に2フィールドを足す変更も含む）:

```go
// auctionDetailJSON に以下を追加すること:
//   WinnerID     *int64 `json:"winner_id"`
//   WinningPrice *int64 `json:"winning_price"`

func TestGetAuctionExposesWinner(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	// auction 11 は seed で closed / winner_id=12 / winning_price=12000
	var closed auctionDetailJSON
	getJSON(t, ts.URL+"/auctions/11", &closed)
	if closed.WinnerID == nil || *closed.WinnerID != 12 {
		t.Errorf("auction 11 winner_id = %v, want 12", closed.WinnerID)
	}
	if closed.WinningPrice == nil || *closed.WinningPrice != 12000 {
		t.Errorf("auction 11 winning_price = %v, want 12000", closed.WinningPrice)
	}

	// live のオークションは null
	var live auctionDetailJSON
	getJSON(t, ts.URL+"/auctions/1", &live)
	if live.WinnerID != nil || live.WinningPrice != nil {
		t.Errorf("auction 1 (live) winner = %v/%v, want null/null", live.WinnerID, live.WinningPrice)
	}
}

// getJSON は GET して JSON をデコードするテストヘルパー。
func getJSON(t *testing.T, url string, dest any) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", url, res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(dest); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd webapp/go && go test -run TestGetAuctionExposesWinner ./...`
Expected: FAIL（`winner_id = <nil>, want 12`）

- [ ] **Step 3: 実装する**

`webapp/go/auctions.go`:

```go
// auctionRow に追加
	WinnerID     sql.NullInt64 `db:"winner_id"`
	WinningPrice sql.NullInt64 `db:"winning_price"`

// auctionColumns を拡張
const auctionColumns = "id, seller_id, category_id, title, description, starting_price, starts_at, ends_at, status, winner_id, winning_price"

// auctionDetail に追加
type auctionDetail struct {
	auctionSummary
	Description   string        `json:"description"`
	StartingPrice int64         `json:"starting_price"`
	WinnerID      *int64        `json:"winner_id"`
	WinningPrice  *int64        `json:"winning_price"`
	Bids          []bidResponse `json:"bids"`
}

// nullInt64Ptr は sql.NullInt64 を JSON の null 可能な *int64 に変換する。
func nullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}
```

`getAuction` の `writeJSON` を差し替え:

```go
	writeJSON(w, http.StatusOK, auctionDetail{
		auctionSummary: *s,
		Description:    a.Description,
		StartingPrice:  a.StartingPrice,
		WinnerID:       nullInt64Ptr(a.WinnerID),
		WinningPrice:   nullInt64Ptr(a.WinningPrice),
		Bids:           bids,
	})
```

`bench/model.go` の `AuctionDetail` にも追加:

```go
type AuctionDetail struct {
	AuctionSummary
	Description   string `json:"description"`
	StartingPrice int64  `json:"starting_price"`
	WinnerID      *int64 `json:"winner_id"`
	WinningPrice  *int64 `json:"winning_price"`
	Bids          []Bid  `json:"bids"`
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd webapp/go && go test ./... && cd ../../bench && go build ./...`
Expected: PASS / ビルド成功

- [ ] **Step 5: コミット**

```bash
git add webapp/go/auctions.go webapp/go/auctions_test.go bench/model.go
git commit -m "feat: オークション詳細にwinner_id/winning_priceを露出"
```

---

### Task 4: 終了処理バッチ

**Files:**
- Create: `webapp/go/closer.go`, `webapp/go/closer_test.go`
- Modify: `webapp/go/main.go`

**Interfaces:**
- Consumes: `auctionRow` / `auctionColumns`（Task 3 で拡張済み）
- Produces: `func (h *handler) closeAuction(ctx context.Context, auctionID int64) error`、`func (h *handler) closeDueAuctions(ctx context.Context) (int, error)`、`func (h *handler) runAuctionCloser(ctx context.Context)`、`func routerFor(h *handler) http.Handler`。Task 13 が `closeAuction` に won 通知を足す

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/closer_test.go` を新規作成:

```go
package main

import (
	"context"
	"testing"
)

// newTestHandler はテスト用にDB直結の handler を返す。
func newTestHandler(t *testing.T) *handler {
	t.Helper()
	db, err := connectDB()
	if err != nil {
		t.Fatalf("connectDB: %v (dev/compose.yaml の mysql は起動していますか?)", err)
	}
	t.Cleanup(func() { db.Close() })
	return &handler{db: db}
}

func TestCloseDueAuctions(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	h := newTestHandler(t)
	ctx := context.Background()

	// auction 1: seed入札3件、最高額 1500 / user 4
	// auction 5: 入札0件
	// どちらも終了時刻を過去にする
	if _, err := h.db.ExecContext(ctx,
		"UPDATE auctions SET ends_at = DATE_SUB(NOW(6), INTERVAL 1 SECOND) WHERE id IN (1, 5)"); err != nil {
		t.Fatal(err)
	}

	n, err := h.closeDueAuctions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("closed = %d, want 2", n)
	}

	var a1 struct {
		Status       string `db:"status"`
		WinnerID     *int64 `db:"winner_id"`
		WinningPrice *int64 `db:"winning_price"`
	}
	if err := h.db.GetContext(ctx, &a1,
		"SELECT status, winner_id, winning_price FROM auctions WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if a1.Status != "closed" {
		t.Errorf("auction 1 status = %q, want closed", a1.Status)
	}
	if a1.WinnerID == nil || *a1.WinnerID != 4 {
		t.Errorf("auction 1 winner_id = %v, want 4", a1.WinnerID)
	}
	if a1.WinningPrice == nil || *a1.WinningPrice != 1500 {
		t.Errorf("auction 1 winning_price = %v, want 1500", a1.WinningPrice)
	}

	var a5 struct {
		Status   string `db:"status"`
		WinnerID *int64 `db:"winner_id"`
	}
	if err := h.db.GetContext(ctx, &a5,
		"SELECT status, winner_id FROM auctions WHERE id = 5"); err != nil {
		t.Fatal(err)
	}
	if a5.Status != "closed" {
		t.Errorf("auction 5 status = %q, want closed", a5.Status)
	}
	if a5.WinnerID != nil {
		t.Errorf("auction 5 winner_id = %v, want nil (入札0件)", a5.WinnerID)
	}

	// 冪等性: もう一度呼んでも対象が無い
	n2, err := h.closeDueAuctions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("2回目の closed = %d, want 0", n2)
	}
}

// closeAuction は既に closed のオークションに対して何もしない(冪等)。
func TestCloseAuctionIdempotent(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	h := newTestHandler(t)
	ctx := context.Background()

	// auction 11 は seed で closed / winner 12 / 12000
	if err := h.closeAuction(ctx, 11); err != nil {
		t.Fatal(err)
	}
	var a struct {
		WinnerID     *int64 `db:"winner_id"`
		WinningPrice *int64 `db:"winning_price"`
	}
	if err := h.db.GetContext(ctx, &a,
		"SELECT winner_id, winning_price FROM auctions WHERE id = 11"); err != nil {
		t.Fatal(err)
	}
	if a.WinnerID == nil || *a.WinnerID != 12 || a.WinningPrice == nil || *a.WinningPrice != 12000 {
		t.Errorf("auction 11 が書き換えられた: winner=%v price=%v, want 12/12000", a.WinnerID, a.WinningPrice)
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd webapp/go && go test -run 'TestClose' ./...`
Expected: コンパイルエラー `h.closeDueAuctions undefined`

- [ ] **Step 3: 実装する**

`webapp/go/closer.go` を新規作成:

```go
package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

// closerInterval は終了処理バッチの実行間隔。
const closerInterval = 1 * time.Second

// closeDueAuctions は終了時刻を過ぎた live オークションを closed にする。戻り値は処理した件数。
//
// 意図的に遅い実装: status/ends_at にインデックスが無いためフルスキャンになり、
// 該当を1件ずつ逐次処理する(まとめて UPDATE しない)。
func (h *handler) closeDueAuctions(ctx context.Context) (int, error) {
	var ids []int64
	if err := h.db.SelectContext(ctx, &ids,
		"SELECT id FROM auctions WHERE status = 'live' AND ends_at <= NOW(6)"); err != nil {
		return 0, err
	}
	closed := 0
	for _, id := range ids {
		if err := h.closeAuction(ctx, id); err != nil {
			// 1件の失敗でバッチ全体を止めない
			log.Printf("closeAuction(%d): %v", id, err)
			continue
		}
		closed++
	}
	return closed, nil
}

// closeAuction は1オークションの終了処理をトランザクションで行う。
// 既に closed なら何もしない(冪等)。
//
// オークション行を FOR UPDATE で保持したまま最高額を求めて確定するため、
// 入札API(同じ行ロックを取る)とは直列化される。したがって
// 「落札額 = そのオークションの入札の最大額」が厳密に成立する。
func (h *handler) closeAuction(ctx context.Context, auctionID int64) error {
	tx, err := h.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var a auctionRow
	if err := tx.GetContext(ctx, &a,
		"SELECT "+auctionColumns+" FROM auctions WHERE id = ? FOR UPDATE", auctionID); err != nil {
		return err
	}
	if a.Status != "live" {
		return nil // 既に処理済み
	}

	var top struct {
		UserID int64 `db:"user_id"`
		Amount int64 `db:"amount"`
	}
	// 意図的に遅い実装: bids に auction_id のインデックスが無い。
	// amount DESC, id ASC で最高額を一意に定める(同額はFOR UPDATE不在の兆候であり、
	// ベンチの単調増加検証が別途 critical で捕まえる)。
	err = tx.GetContext(ctx, &top,
		"SELECT user_id, amount FROM bids WHERE auction_id = ? ORDER BY amount DESC, id ASC LIMIT 1", auctionID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx,
			"UPDATE auctions SET status = 'closed' WHERE id = ?", auctionID); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if _, err := tx.ExecContext(ctx,
			"UPDATE auctions SET status = 'closed', winner_id = ?, winning_price = ? WHERE id = ?",
			top.UserID, top.Amount, auctionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// runAuctionCloser は closerInterval 間隔で closeDueAuctions を呼び続ける。
func (h *handler) runAuctionCloser(ctx context.Context) {
	t := time.NewTicker(closerInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := h.closeDueAuctions(ctx); err != nil {
				log.Printf("closeDueAuctions: %v", err)
			}
		}
	}
}
```

`webapp/go/main.go` を編集。`newRouter` を分割し、`main` でバッチを起動する:

```go
func newRouter(db *sqlx.DB) http.Handler {
	return routerFor(&handler{db: db})
}

// routerFor は handler からルーターを組み立てる。
// main はバッチ用に handler を先に作る必要があるため分離している。
func routerFor(h *handler) http.Handler {
	r := chi.NewRouter()
	r.Post("/initialize", h.postInitialize)
	r.Post("/register", h.postRegister)
	r.Post("/login", h.postLogin)
	r.Get("/auctions", h.getAuctions)
	r.Get("/auctions/{id}", h.getAuction)
	r.Post("/auctions/{id}/bids", h.postBid)
	return r
}

func main() {
	db, err := connectDB()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	h := &handler{db: db}
	// 終了処理バッチ。テスト(newRouter経由)では起動しない。
	go h.runAuctionCloser(context.Background())
	addr := ":" + getEnv("ISUBID_PORT", "8000")
	log.Printf("isubid listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, routerFor(h)))
}
```

`import` に `"context"` を追加。

- [ ] **Step 4: テストが通ることを確認**

Run: `cd webapp/go && go test ./...`
Expected: PASS

- [ ] **Step 5: 実アプリで遷移を目視確認**

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
curl -s -XPOST http://localhost:8080/initialize >/dev/null
for i in $(seq 1 12); do curl -s http://localhost:8080/auctions | grep -o '"id":[0-9]*' | wc -l; done
```
Expected: 初回は 10、+12秒以降に 9、+20秒以降に 8 と減っていく（auction 4, 2, ... が閉じる）

- [ ] **Step 6: コミット**

```bash
git add webapp/go/closer.go webapp/go/closer_test.go webapp/go/main.go
git commit -m "feat: 終了処理バッチ(毎秒ポーリングで落札者を確定)を追加"
```

---

### Task 5: 入札者シナリオが closed を正常系として扱う

**Files:**
- Modify: `bench/load.go`

**Interfaces:**
- Consumes: `AuctionDetail.Status`
- Produces: なし（挙動変更のみ）

- [ ] **Step 1: 変更内容を確認する**

`bench/load.go` の `bidderIteration` は、一覧から選んだオークションが走行中に closed になると、`400 auction is not live` を「競り負け」と誤解して5回リトライする。これは無駄なうえ、意図が読めない。詳細取得直後に status を見て抜ける。

- [ ] **Step 2: 実装する**

`bidderIteration` のリトライループ内、`step.AddScore(ScoreGETDetail)` の直後に挿入:

```go
		// 走行中に終了処理バッチが closed にした可能性がある。
		// closed への入札は 400 が正しい応答なので、エラーにせず次のイテレーションへ譲る。
		if d.Status != "live" {
			return
		}
```

- [ ] **Step 3: ビルドとベンチを通す**

Run:
```bash
cd bench && go build ./... && go run . -target http://localhost:8080 -duration 60s
```
Expected: `RESULT: PASS`。critical 0件。`ERRORS` が 0 でなくとも、`auction is not live` 由来の application エラーが出ていないこと

- [ ] **Step 4: コミット**

```bash
git add bench/load.go
git commit -m "fix: 走行中にclosedになったオークションを入札者シナリオの正常系として扱う"
```

---

### Task 6: 落札照合と「期限切れは closed」検証

**Files:**
- Modify: `bench/reconcile.go`, `bench/validate.go`, `bench/scenario.go`
- Test: `bench/reconcile_test.go`, `bench/validate_test.go`

**Interfaces:**
- Consumes: `AuctionDetail.WinnerID` / `WinningPrice`（Task 3）
- Produces: `func reconcileClosedAuction(auctionID int64, d *AuctionDetail) []error`、`func ValidateAuctionClosedIfDue(d *AuctionDetail, now time.Time, grace time.Duration) error`、`const closeGrace`

- [ ] **Step 1: 失敗するテストを書く**

`bench/reconcile_test.go` に追記:

```go
// 落札者は「そのオークションの入札の最大額の入札者」でなければならない。
// d.Bids 自体が台帳と一致していることは reconcileAuction が別途保証するので、
// ここは d のスナップショット内部の不変条件として厳密に検査できる。
func TestReconcileClosedAuction(t *testing.T) {
	t0 := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	ptr := func(v int64) *int64 { return &v }
	// created_at DESC, 金額は厳密単調減少(受理順で単調増加)
	bids := []Bid{
		{ID: 12, User: User{ID: 7}, Amount: 1800, CreatedAt: t0.Add(2 * time.Minute)},
		{ID: 11, User: User{ID: 5}, Amount: 1600, CreatedAt: t0.Add(time.Minute)},
	}

	tests := []struct {
		name       string
		d          *AuctionDetail
		wantErrLen int
	}{
		{
			name: "live は対象外",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, Status: "live"},
				Bids:           bids,
			},
			wantErrLen: 0,
		},
		{
			name: "closed / 最高額入札者が落札者",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, Status: "closed"},
				WinnerID:       ptr(7), WinningPrice: ptr(1800),
				Bids: bids,
			},
			wantErrLen: 0,
		},
		{
			name: "closed / 落札者が最高額でない",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, Status: "closed"},
				WinnerID:       ptr(5), WinningPrice: ptr(1600),
				Bids: bids,
			},
			wantErrLen: 1,
		},
		{
			name: "closed / 落札額だけずれている",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, Status: "closed"},
				WinnerID:       ptr(7), WinningPrice: ptr(1700),
				Bids: bids,
			},
			wantErrLen: 1,
		},
		{
			name: "closed / 入札があるのに落札者なし",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, Status: "closed"},
				Bids:           bids,
			},
			wantErrLen: 1,
		},
		{
			name: "closed / 入札0件なら落札者は null",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, Status: "closed"},
			},
			wantErrLen: 0,
		},
		{
			name: "closed / 入札0件なのに落札者がいる",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, Status: "closed"},
				WinnerID:       ptr(7), WinningPrice: ptr(1800),
			},
			wantErrLen: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := reconcileClosedAuction(1, tt.d)
			if len(errs) != tt.wantErrLen {
				t.Fatalf("errs = %d件 %v, want %d件", len(errs), errs, tt.wantErrLen)
			}
		})
	}
}
```

`bench/validate_test.go` に追記:

```go
// ends_at を過ぎたオークションは closed になっていなければならない
// (終了処理バッチが止まっていることの検出)。
func TestValidateAuctionClosedIfDue(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	grace := 5 * time.Second

	// 期限を大きく過ぎているのに live → 違反
	overdue := &AuctionDetail{AuctionSummary: AuctionSummary{
		ID: 1, Status: "live", EndsAt: now.Add(-30 * time.Second)}}
	if err := ValidateAuctionClosedIfDue(overdue, now, grace); err == nil {
		t.Error("期限切れ live が検出されなかった")
	}

	// 期限直後(猶予の内側)は許容
	justEnded := &AuctionDetail{AuctionSummary: AuctionSummary{
		ID: 1, Status: "live", EndsAt: now.Add(-2 * time.Second)}}
	if err := ValidateAuctionClosedIfDue(justEnded, now, grace); err != nil {
		t.Errorf("猶予内の live が拒否された: %v", err)
	}

	// 期限前の live は当然OK
	future := &AuctionDetail{AuctionSummary: AuctionSummary{
		ID: 1, Status: "live", EndsAt: now.Add(time.Hour)}}
	if err := ValidateAuctionClosedIfDue(future, now, grace); err != nil {
		t.Errorf("期限前の live が拒否された: %v", err)
	}

	// closed ならいつでもOK
	closed := &AuctionDetail{AuctionSummary: AuctionSummary{
		ID: 1, Status: "closed", EndsAt: now.Add(-30 * time.Second)}}
	if err := ValidateAuctionClosedIfDue(closed, now, grace); err != nil {
		t.Errorf("closed が拒否された: %v", err)
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd bench && go test -run 'TestReconcileClosedAuction|TestValidateAuctionClosedIfDue' ./...`
Expected: コンパイルエラー（`reconcileClosedAuction` / `ValidateAuctionClosedIfDue` 未定義）

- [ ] **Step 3: 実装する**

`bench/reconcile.go` に追記:

```go
// reconcileClosedAuction は closed になったオークションの落札結果を検証する。
//
// 落札者は「そのオークションの入札のうち最大額の入札者」でなければならない。これは
// 参照実装が入札・終了処理の双方でオークション行を FOR UPDATE で保持するため、
// 「入札がコミットされてから閉じる」か「閉じてから入札が400で弾かれる」のどちらかにしか
// ならず、厳密に成立する不変条件である(中間状態が存在しない)。
//
// d.Bids 自体がベンチの台帳と一致していることは reconcileAuction が別途保証するので、
// ここは d のスナップショット内部で完結した検査でよく、pending の許容も要らない。
func reconcileClosedAuction(auctionID int64, d *AuctionDetail) []error {
	if d.Status != "closed" {
		return nil
	}
	var errs []error
	if len(d.Bids) == 0 {
		if d.WinnerID != nil || d.WinningPrice != nil {
			errs = append(errs, fmt.Errorf("auction %d: 入札0件で closed なのに落札者がいる (winner=%v price=%v)",
				auctionID, d.WinnerID, d.WinningPrice))
		}
		return errs
	}
	top := d.Bids[0]
	for _, b := range d.Bids[1:] {
		if b.Amount > top.Amount || (b.Amount == top.Amount && b.ID < top.ID) {
			top = b
		}
	}
	if d.WinnerID == nil || d.WinningPrice == nil {
		errs = append(errs, fmt.Errorf("auction %d: closed なのに落札者が未設定 (期待: user=%d price=%d)",
			auctionID, top.User.ID, top.Amount))
		return errs
	}
	if *d.WinnerID != top.User.ID {
		errs = append(errs, fmt.Errorf("auction %d: winner_id が %d (期待: %d = 最高額 %d の入札者)",
			auctionID, *d.WinnerID, top.User.ID, top.Amount))
	}
	if *d.WinningPrice != top.Amount {
		errs = append(errs, fmt.Errorf("auction %d: winning_price が %d (期待: %d)",
			auctionID, *d.WinningPrice, top.Amount))
	}
	return errs
}
```

`bench/validate.go` に追記:

```go
// closeGrace は終了処理の猶予。バッチは1秒間隔で回るため、ends_at 直後の短い
// あいだ live のままなのは正常。ベンチとアプリは同一ホストで動く前提で、
// 時計ずれは考慮しない。
const closeGrace = 5 * time.Second

// ValidateAuctionClosedIfDue は ends_at を過ぎたオークションが closed に
// なっていることを検証する。終了処理バッチが動いていないことを検出する。
func ValidateAuctionClosedIfDue(d *AuctionDetail, now time.Time, grace time.Duration) error {
	if d.Status == "closed" {
		return nil
	}
	if d.EndsAt.Add(grace).Before(now) {
		return fmt.Errorf("auction %d: ends_at (%s) を過ぎているのに status が %q (期待: closed)",
			d.ID, d.EndsAt.Format(time.RFC3339), d.Status)
	}
	return nil
}
```

`bench/scenario.go` の `Validation` のループ内、`reconcileAuction` の呼び出し直後に追加:

```go
		for _, e := range reconcileClosedAuction(auctionID, d) {
			step.AddError(failure.NewError(ErrCritical, e))
		}
		if err := ValidateAuctionClosedIfDue(d, time.Now().UTC(), closeGrace); err != nil {
			step.AddError(failure.NewError(ErrCritical, err))
		}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd bench && go test ./...`
Expected: PASS

- [ ] **Step 5: 通しで走らせる**

Run: `cd bench && go run . -target http://localhost:8080 -duration 60s`
Expected: `RESULT: PASS`、critical 0件

- [ ] **Step 6: コミット**

```bash
git add bench/reconcile.go bench/reconcile_test.go bench/validate.go bench/validate_test.go bench/scenario.go
git commit -m "feat: 落札結果の照合と期限切れauctionのclosed検証をValidationに追加"
```

---

### Task 7: 3-1 のベンチのベンチ

**Files:**
- Create: `docs/phase3-notes.md`
- Modify: なし（一時的な破壊は必ず戻す）

**Interfaces:**
- Consumes: Task 4 の `runAuctionCloser`、Task 6 の `ValidateAuctionClosedIfDue`
- Produces: `docs/phase3-notes.md`（以降のタスクが実測を追記していく）

- [ ] **Step 1: 終了処理バッチを止めて FAIL することを確認**

`webapp/go/main.go` の `go h.runAuctionCloser(context.Background())` を一時的にコメントアウトし、再ビルドして走らせる:

```bash
docker compose -f dev/compose.yaml up -d --build
cd bench && go run . -target http://localhost:8080 -duration 60s
```
Expected: `RESULT: FAIL`。`ends_at (...) を過ぎているのに status が "live"` の critical が複数件

- [ ] **Step 2: 落札者を取り違える壊し方でも FAIL することを確認**

`main.go` を元に戻し、代わりに `webapp/go/closer.go` の最高額クエリを一時的に `ORDER BY amount ASC, id ASC`（最低額が落札）に変え、再ビルドして走らせる。

Expected: `RESULT: FAIL`。`winner_id が ... (期待: ... = 最高額 ... の入札者)` の critical

- [ ] **Step 3: 破壊を戻して PASS を再確認**

```bash
git checkout webapp/go/main.go webapp/go/closer.go
docker compose -f dev/compose.yaml up -d --build
cd bench && go run . -target http://localhost:8080 -duration 60s
```
Expected: `RESULT: PASS`、critical 0件

- [ ] **Step 4: `docs/phase3-notes.md` に実測を記録**

以下の構成で作成する。`<...>` は Step 1〜3 の実際の出力で埋める（プレースホルダのまま残さない）。

```markdown
# Phase 3 実装ノート

## 3-1 時間軸 + 終了処理 + 落札Validation

### 設計判断

- **落札照合はスナップショット内部の不変条件として検査する**: 「winner = argmax(bids)」を
  詳細レスポンス1件の中で閉じて検証する。入札APIと終了処理が同じオークション行の
  FOR UPDATE で直列化されるため中間状態が存在せず、pending の許容が要らない。
  台帳との突合(bids 自体が本物か)は既存の reconcileAuction が担う
- **「ends_at を過ぎたら closed」を独立した検証項目にした**: これが無いと、終了処理バッチを
  止めても closed が1件も無いだけで落札照合は素通りしてしまい、バッチ停止を検出できない

### ベンチのベンチ実測(2026-08-29)

| 壊し方 | 結果 |
|---|---|
| `runAuctionCloser` の起動をコメントアウト | `RESULT: FAIL` / critical <N>件 / `ends_at を過ぎているのに status が "live"` |
| 落札者選択を `ORDER BY amount ASC` に変更 | `RESULT: FAIL` / critical <N>件 / `winner_id が ... (期待: 最高額の入札者)` |
| 復元後 | `RESULT: PASS` / critical 0件 / `SCORE: <点>` |
```

- [ ] **Step 5: コミット**

```bash
git add docs/phase3-notes.md
git commit -m "docs: 3-1のベンチのベンチ実測とValidation設計判断を記録"
```

---

## 3-2: 入札フィード

### Task 8: `GET /auctions/:id/bids?since=` の実装

**Files:**
- Create: `webapp/go/feed.go`, `webapp/go/feed_test.go`
- Modify: `webapp/go/main.go`

**Interfaces:**
- Consumes: `userResponse`, `writeJSON`, `writeError`
- Produces: エンドポイント `GET /auctions/{id}/bids`。レスポンスは `{"bids": [{"id","user":{"id","name"},"amount","created_at"}]}` を `id ASC` 順

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/feed_test.go` を新規作成:

```go
package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

type feedResponseJSON struct {
	Bids []bidJSON `json:"bids"`
}

func TestGetAuctionBids(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	// auction 1 の seed 入札は id 1,2,3 (amount 1000/1200/1500)
	var all feedResponseJSON
	getJSON(t, ts.URL+"/auctions/1/bids", &all)
	if len(all.Bids) != 3 {
		t.Fatalf("since 無し: %d件, want 3", len(all.Bids))
	}
	// id ASC / 金額は受理順なので単調増加
	for i := 1; i < len(all.Bids); i++ {
		if all.Bids[i].ID <= all.Bids[i-1].ID {
			t.Errorf("id ASC でない: [%d].ID=%d, [%d].ID=%d", i-1, all.Bids[i-1].ID, i, all.Bids[i].ID)
		}
		if all.Bids[i].Amount <= all.Bids[i-1].Amount {
			t.Errorf("金額が単調増加でない: %d -> %d", all.Bids[i-1].Amount, all.Bids[i].Amount)
		}
	}
	if all.Bids[0].User.Name != "seed_user_02" {
		t.Errorf("bids[0].user.name = %q, want seed_user_02", all.Bids[0].User.Name)
	}

	// since で絞り込む
	var after feedResponseJSON
	getJSON(t, ts.URL+"/auctions/1/bids?since=2", &after)
	if len(after.Bids) != 1 {
		t.Fatalf("since=2: %d件, want 1", len(after.Bids))
	}
	if after.Bids[0].ID != 3 {
		t.Errorf("since=2 の先頭 id = %d, want 3", after.Bids[0].ID)
	}

	// 全部読み切った後は空配列(null ではない)
	var none feedResponseJSON
	getJSON(t, ts.URL+"/auctions/1/bids?since=3", &none)
	if none.Bids == nil {
		t.Error("bids が null (期待: 空配列)")
	}
	if len(none.Bids) != 0 {
		t.Errorf("since=3: %d件, want 0", len(none.Bids))
	}
}

func TestGetAuctionBidsErrors(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	for _, tt := range []struct {
		path string
		want int
	}{
		{"/auctions/1/bids?since=abc", http.StatusBadRequest},
		{"/auctions/1/bids?since=-1", http.StatusBadRequest},
		{"/auctions/abc/bids", http.StatusBadRequest},
		{"/auctions/99999/bids", http.StatusNotFound},
	} {
		res, err := http.Get(ts.URL + tt.path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tt.want {
			t.Errorf("GET %s status = %d, want %d", tt.path, res.StatusCode, tt.want)
		}
	}
}

// エラーボディが {"error": "..."} 形式であること
func TestGetAuctionBidsErrorBody(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	res, err := http.Get(ts.URL + "/auctions/1/bids?since=abc")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error == "" {
		t.Error("error フィールドが空")
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd webapp/go && go test -run TestGetAuctionBids ./...`
Expected: FAIL（`status = 404, want 200` — ルート未登録）

- [ ] **Step 3: 実装する**

`webapp/go/feed.go` を新規作成:

```go
package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// getAuctionBids は入札フィード。since より大きい id の入札を id 昇順で返す。
//
// bids.id は AUTO_INCREMENT であり、入札APIはオークション行を FOR UPDATE で
// 保持したまま INSERT するため、同一オークション内では id 順 = コミット順になる。
// したがって「id > since」のカーソルは取りこぼしも重複も起こさない。
//
// 意図的に遅い実装: bids に auction_id のインデックスが無くフルスキャンになり、
// さらに入札ごとにユーザーを引く(N+1)。
func (h *handler) getAuctionBids(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid auction id")
		return
	}
	var since int64
	if s := r.URL.Query().Get("since"); s != "" {
		since, err = strconv.ParseInt(s, 10, 64)
		if err != nil || since < 0 {
			writeError(w, http.StatusBadRequest, "invalid since")
			return
		}
	}

	var exists int
	err = h.db.GetContext(r.Context(), &exists, "SELECT 1 FROM auctions WHERE id = ?", id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "auction not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var rows []struct {
		ID        int64     `db:"id"`
		UserID    int64     `db:"user_id"`
		Amount    int64     `db:"amount"`
		CreatedAt time.Time `db:"created_at"`
	}
	if err := h.db.SelectContext(r.Context(), &rows,
		"SELECT id, user_id, amount, created_at FROM bids WHERE auction_id = ? AND id > ? ORDER BY id ASC",
		id, since); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bids := make([]bidResponse, 0, len(rows))
	for _, b := range rows {
		var u userResponse
		// 意図的に遅い実装(N+1): 入札ごとにユーザーを引く
		if err := h.db.GetContext(r.Context(), &u,
			"SELECT id, name FROM users WHERE id = ?", b.UserID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		bids = append(bids, bidResponse{ID: b.ID, User: u, Amount: b.Amount, CreatedAt: b.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"bids": bids})
}
```

`webapp/go/main.go` の `routerFor` にルートを追加:

```go
	r.Get("/auctions/{id}/bids", h.getAuctionBids)
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd webapp/go && go test ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add webapp/go/feed.go webapp/go/feed_test.go webapp/go/main.go
git commit -m "feat: 入札フィードAPI(GET /auctions/:id/bids?since=)を追加"
```

---

### Task 9: ベンチのフィードクライアントと純粋検証関数

**Files:**
- Modify: `bench/client.go`, `bench/validate.go`
- Test: `bench/validate_test.go`

**Interfaces:**
- Consumes: `Bid`（`bench/model.go`）
- Produces: `func (c *Client) GetBidFeed(ctx context.Context, auctionID, since int64) ([]Bid, error)`、`func ValidateFeedPage(bids []Bid, since int64) error`

- [ ] **Step 1: 失敗するテストを書く**

`bench/validate_test.go` に追記:

```go
// フィードは id ASC で、since より大きい id のみを含み、金額が厳密単調増加になる。
// (受理順 = id 昇順であり、入札は現在最高額を必ず上回るため)
func TestValidateFeedPage(t *testing.T) {
	t0 := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	ok := []Bid{
		{ID: 11, User: User{ID: 2}, Amount: 1000, CreatedAt: t0},
		{ID: 12, User: User{ID: 3}, Amount: 1200, CreatedAt: t0.Add(time.Second)},
		{ID: 13, User: User{ID: 4}, Amount: 1500, CreatedAt: t0.Add(2 * time.Second)},
	}
	if err := ValidateFeedPage(ok, 10); err != nil {
		t.Fatalf("正しいフィードが拒否された: %v", err)
	}
	if err := ValidateFeedPage(nil, 10); err != nil {
		t.Errorf("空フィードが拒否された: %v", err)
	}

	// since 以下の id が混ざっている
	withOld := append([]Bid{{ID: 9, User: User{ID: 2}, Amount: 900, CreatedAt: t0}}, ok...)
	if err := ValidateFeedPage(withOld, 10); err == nil {
		t.Error("since 以下の id が検出されなかった")
	}

	// id が降順
	desc := []Bid{ok[2], ok[1], ok[0]}
	if err := ValidateFeedPage(desc, 10); err == nil {
		t.Error("id 降順が検出されなかった")
	}

	// 金額が単調増加でない(同額) = FOR UPDATE 不在の兆候
	sameAmount := []Bid{
		{ID: 11, User: User{ID: 2}, Amount: 1000, CreatedAt: t0},
		{ID: 12, User: User{ID: 3}, Amount: 1000, CreatedAt: t0.Add(time.Second)},
	}
	if err := ValidateFeedPage(sameAmount, 10); err == nil {
		t.Error("同額(単調増加違反)が検出されなかった")
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd bench && go test -run TestValidateFeedPage ./...`
Expected: コンパイルエラー `undefined: ValidateFeedPage`

- [ ] **Step 3: 実装する**

`bench/validate.go` に追記:

```go
// ValidateFeedPage はフィード1ページ分の不変条件を検証する。
//   (1) すべての id が since より大きい
//   (2) id 昇順
//   (3) 金額が厳密単調増加
// (3)は詳細APIの ValidateBidAmountsMonotonic と同じ根拠(受理順で単調増加)を
// 昇順の並びに対して見たもの。同額や逆転は FOR UPDATE 不在の兆候である。
func ValidateFeedPage(bids []Bid, since int64) error {
	for i, b := range bids {
		if b.ID <= since {
			return fmt.Errorf("フィードに since(%d) 以下の入札が含まれる (index %d: id=%d)", since, i, b.ID)
		}
		if i == 0 {
			continue
		}
		prev := bids[i-1]
		if b.ID <= prev.ID {
			return fmt.Errorf("フィードが id 昇順でない (index %d: id=%d の前が id=%d)", i, b.ID, prev.ID)
		}
		if b.Amount <= prev.Amount {
			return fmt.Errorf("フィードの金額が単調増加でない (id=%d(amount=%d) の次に id=%d(amount=%d))",
				prev.ID, prev.Amount, b.ID, b.Amount)
		}
	}
	return nil
}
```

`bench/client.go` に追記:

```go
// GetBidFeed は入札フィードを取得する。since より大きい id の入札が id 昇順で返る。
func (c *Client) GetBidFeed(ctx context.Context, auctionID, since int64) ([]Bid, error) {
	path := fmt.Sprintf("/auctions/%d/bids?since=%d", auctionID, since)
	code, b, err := c.doJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d (body: %s)", path, code, b)
	}
	var body struct {
		Bids []Bid `json:"bids"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, fmt.Errorf("GET %s: 不正なJSON: %w", path, err)
	}
	return body.Bids, nil
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd bench && go test ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add bench/client.go bench/validate.go bench/validate_test.go
git commit -m "feat: ベンチにフィード取得クライアントとページ不変条件の検証を追加"
```

---

### Task 10: 入札者シナリオにフィード反映2秒検証を追加

**Files:**
- Modify: `bench/load.go`, `bench/score.go`, `bench/main.go`

**Interfaces:**
- Consumes: `GetBidFeed` / `ValidateFeedPage`（Task 9）
- Produces: `const ScoreGETFeed score.ScoreTag`、`const feedReflectDeadline`、`func (s *Scenario) awaitFeedReflection(...) `

- [ ] **Step 1: スコアタグを足す**

`bench/score.go`:

```go
	ScoreGETFeed   score.ScoreTag = "GET /auctions/:id/bids"
```

`scoreTable` に `ScoreGETFeed: 1,` を追加。

`bench/main.go` の breakdown 出力リストに追加:

```go
		{ScoreGETFeed, "GET /auctions/:id/bids"},
```

- [ ] **Step 2: 反映待ちヘルパーを実装する**

`bench/load.go` に追記:

```go
const (
	// feedReflectDeadline は自分の入札がフィードに現れるまでの許容時間。
	// レギュレーションのリアルタイム性要件(ポーリング間引きによるズルの防止)。
	feedReflectDeadline = 2 * time.Second
	// feedPollInterval はフィードのポーリング間隔。
	feedPollInterval = 100 * time.Millisecond
)

// awaitFeedReflection は自分の入札 bidID が feedReflectDeadline 以内に
// フィードへ現れることを検証する。違反は step に直接記録する。
//
// ctx がキャンセルされた場合(Load終了)は違反として扱わない。走行終了間際の
// ポーリング打ち切りを critical にすると false-FAIL になるため。
func (s *Scenario) awaitFeedReflection(ctx context.Context, step *isucandar.BenchmarkStep,
	c *Client, auctionID, since, bidID int64) {
	deadline := time.Now().Add(feedReflectDeadline)
	for {
		feed, err := c.GetBidFeed(ctx, auctionID, since)
		if err != nil {
			addErr(ctx, step, ErrApplication, err)
			return
		}
		step.AddScore(ScoreGETFeed)
		if err := ValidateFeedPage(feed, since); err != nil {
			addErr(ctx, step, ErrCritical, fmt.Errorf("auction %d: %w", auctionID, err))
			return
		}
		for _, b := range feed {
			if b.ID == bidID {
				return // 反映を確認できた
			}
		}
		if time.Now().After(deadline) {
			if ctx.Err() != nil {
				return // Load終了に伴う打ち切り。違反ではない
			}
			addErr(ctx, step, ErrCritical,
				fmt.Errorf("auction %d: 入札 id=%d が %v 以内にフィードへ反映されない",
					auctionID, bidID, feedReflectDeadline))
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(feedPollInterval):
		}
	}
}
```

`import` に `"time"` を追加（`isucandar` / `failure` / `fmt` は既にある）。

- [ ] **Step 3: 入札者シナリオから呼ぶ**

`bidderIteration` のリトライループ内で、詳細取得の直後にカーソル起点を控える:

```go
		// フィードのカーソル起点。詳細は created_at DESC, id DESC なので先頭が最大 id。
		var sinceID int64
		if len(d.Bids) > 0 {
			sinceID = d.Bids[0].ID
		}
```

`case 201:` の `step.AddScore(ScorePOSTBid)` の直後、`return` の前に挿入:

```go
			s.awaitFeedReflection(ctx, step, c, target.ID, sinceID, bid.ID)
			return
```

- [ ] **Step 4: ビルドしてベンチを通す**

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
cd bench && go build ./... && go run . -target http://localhost:8080 -duration 60s
```
Expected: `RESULT: PASS`。breakdown に `GET /auctions/:id/bids` の行が出る

- [ ] **Step 5: コミット**

```bash
git add bench/load.go bench/score.go bench/main.go
git commit -m "feat: 入札者シナリオにフィード反映2秒検証とフィードスコアを追加"
```

---

### Task 11: 3-2 のベンチのベンチ

**Files:**
- Modify: `docs/phase3-notes.md`

**Interfaces:**
- Consumes: Task 10 の `awaitFeedReflection`
- Produces: `docs/phase3-notes.md` への追記

- [ ] **Step 1: フィードを遅延させて FAIL することを確認**

`webapp/go/feed.go` の `getAuctionBids` 冒頭に一時的に挿入:

```go
	time.Sleep(3 * time.Second) // ベンチのベンチ用の一時的な遅延
```

再ビルドして走らせる:
```bash
docker compose -f dev/compose.yaml up -d --build
cd bench && go run . -target http://localhost:8080 -duration 30s
```
Expected: `RESULT: FAIL`。`入札 id=... が 2s 以内にフィードへ反映されない` の critical

- [ ] **Step 2: フィードの順序を壊しても FAIL することを確認**

Step 1 の `time.Sleep` を消し、代わりにクエリを `ORDER BY id DESC` に変えて再ビルド・走行。

Expected: `RESULT: FAIL`。`フィードが id 昇順でない` の critical

- [ ] **Step 3: 破壊を戻して PASS を再確認**

```bash
git checkout webapp/go/feed.go
docker compose -f dev/compose.yaml up -d --build
cd bench && go run . -target http://localhost:8080 -duration 60s
```
Expected: `RESULT: PASS`、critical 0件

- [ ] **Step 4: `docs/phase3-notes.md` に追記**

`<...>` は Step 1〜3 の実際の出力で埋める（プレースホルダのまま残さない）。

```markdown
## 3-2 入札フィード

### 設計判断

- **カーソルは `since=<bid_id>`**: 入札APIがオークション行を FOR UPDATE で保持したまま
  INSERT するため、同一オークション内では id 順 = コミット順が保証される。timestamp
  カーソルだと同一マイクロ秒の複数入札で重複か欠落が起き、反映検証が確率的になる
- **反映待ちの打ち切りは違反にしない**: Load 終了に伴う ctx キャンセルで
  ポーリングを打ち切った場合、`ctx.Err() != nil` を見て critical を上げない

### ベンチのベンチ実測(2026-08-29)

| 壊し方 | 結果 |
|---|---|
| フィード応答を3秒遅延 | `RESULT: FAIL` / critical <N>件 / `2s 以内にフィードへ反映されない` |
| フィードを `ORDER BY id DESC` に変更 | `RESULT: FAIL` / critical <N>件 / `フィードが id 昇順でない` |
| 復元後 | `RESULT: PASS` / critical 0件 / `SCORE: <点>` |
```

- [ ] **Step 5: コミット**

```bash
git add docs/phase3-notes.md
git commit -m "docs: 3-2のベンチのベンチ実測を記録"
```

---

## 3-3: 通知

### Task 12: 入札時の outbid ファンアウト

**Files:**
- Modify: `webapp/go/bids.go`
- Test: `webapp/go/bids_test.go`

**Interfaces:**
- Consumes: `auctionRow`（`a.Title` を通知文面に使う）
- Produces: `notifications` への `type='outbid'` 行。Task 15 のベンチ期待値がこのファンアウト範囲に依存する

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/bids_test.go` に追記:

```go
// 高値更新のたび、そのオークションに入札済みの全ユーザー(今回の入札者を除く)へ
// outbid 通知が1行ずつ入る。
func TestPostBidFansOutOutbidNotifications(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	h := newTestHandler(t)
	ctx := context.Background()

	// auction 1 の既存入札者は user 2, 3, 4 (seed)。user 5 が入札すると 3件入るはず。
	c := loginAs(t, ts, "seed_user_05")
	postBid(t, c, ts, 1, 1600, http.StatusCreated)

	var n int64
	if err := h.db.GetContext(ctx, &n,
		"SELECT COUNT(*) FROM notifications WHERE auction_id = 1 AND type = 'outbid'"); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("outbid 通知が %d件, want 3 (user 2,3,4)", n)
	}
	// 入札者自身には飛ばない
	var self int64
	if err := h.db.GetContext(ctx, &self,
		"SELECT COUNT(*) FROM notifications WHERE auction_id = 1 AND user_id = 5"); err != nil {
		t.Fatal(err)
	}
	if self != 0 {
		t.Errorf("入札者自身に %d件の通知, want 0", self)
	}

	// 同じ user 5 が再入札すると、今度は user 2,3,4 に加えて…user 5 は除外のまま 3件追加
	postBid(t, c, ts, 1, 1700, http.StatusCreated)
	if err := h.db.GetContext(ctx, &n,
		"SELECT COUNT(*) FROM notifications WHERE auction_id = 1 AND type = 'outbid'"); err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("2回目の入札後 outbid 通知が %d件, want 6", n)
	}
}
```

既存の `bids_test.go` にログイン/入札ヘルパーが無ければ以下を追加する（既にある場合は既存のものを使い、この Step の追加は不要）:

```go
// loginAs はシードユーザーでログインした Cookie 付きクライアントを返す。
func loginAs(t *testing.T, ts *httptest.Server, name string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar}
	res, err := c.Post(ts.URL+"/login", "application/json",
		strings.NewReader(`{"name":"`+name+`","password":"password"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login %s status = %d, want 200", name, res.StatusCode)
	}
	return c
}

// postBid は入札し、ステータスコードを検証する。
func postBid(t *testing.T, c *http.Client, ts *httptest.Server, auctionID, amount int64, want int) {
	t.Helper()
	body := fmt.Sprintf(`{"amount":%d}`, amount)
	res, err := c.Post(fmt.Sprintf("%s/auctions/%d/bids", ts.URL, auctionID), "application/json",
		strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		t.Fatalf("POST bid status = %d, want %d", res.StatusCode, want)
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd webapp/go && go test -run TestPostBidFansOut ./...`
Expected: FAIL（`outbid 通知が 0件, want 3`）

- [ ] **Step 3: 実装する**

`webapp/go/bids.go` の `postBid` 内、`INSERT INTO bids` の後・`created_at` 取得の前に挿入:

```go
	// 意図的に遅い実装: 高値更新のたび、そのオークションに入札済みの全ユーザーへ
	// 1行ずつ INSERT する(バルクINSERTにしない)。入札APIが重い主因。
	var targets []int64
	if err := tx.SelectContext(r.Context(), &targets,
		"SELECT DISTINCT user_id FROM bids WHERE auction_id = ? AND user_id <> ?",
		auctionID, userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	message := "「" + a.Title + "」で他のユーザーに競り負けました"
	for _, uid := range targets {
		if _, err := tx.ExecContext(r.Context(),
			"INSERT INTO notifications (user_id, type, auction_id, message) VALUES (?, 'outbid', ?, ?)",
			uid, auctionID, message); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd webapp/go && go test ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add webapp/go/bids.go webapp/go/bids_test.go
git commit -m "feat: 入札受理時に入札済み全ユーザーへoutbid通知をファンアウト"
```

---

### Task 13: 落札時の won 通知

**Files:**
- Modify: `webapp/go/closer.go`
- Test: `webapp/go/closer_test.go`

**Interfaces:**
- Consumes: `closeAuction`（Task 4）
- Produces: `notifications` への `type='won'` 行

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/closer_test.go` に追記:

```go
// 落札確定時、落札者に won 通知が1件入る。
func TestCloseAuctionNotifiesWinner(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	h := newTestHandler(t)
	ctx := context.Background()

	if _, err := h.db.ExecContext(ctx,
		"UPDATE auctions SET ends_at = DATE_SUB(NOW(6), INTERVAL 1 SECOND) WHERE id IN (1, 5)"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.closeDueAuctions(ctx); err != nil {
		t.Fatal(err)
	}

	// auction 1 の落札者は user 4
	var n int64
	if err := h.db.GetContext(ctx, &n,
		"SELECT COUNT(*) FROM notifications WHERE user_id = 4 AND auction_id = 1 AND type = 'won'"); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("won 通知が %d件, want 1", n)
	}

	// auction 5 は入札0件なので won 通知は出ない
	if err := h.db.GetContext(ctx, &n,
		"SELECT COUNT(*) FROM notifications WHERE auction_id = 5 AND type = 'won'"); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("入札0件の auction 5 に won 通知が %d件, want 0", n)
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd webapp/go && go test -run TestCloseAuctionNotifiesWinner ./...`
Expected: FAIL（`won 通知が 0件, want 1`）

- [ ] **Step 3: 実装する**

`webapp/go/closer.go` の `closeAuction` の `default:` ブランチ、`UPDATE auctions ...` の直後に挿入:

```go
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO notifications (user_id, type, auction_id, message) VALUES (?, 'won', ?, ?)",
			top.UserID, auctionID, "「"+a.Title+"」を落札しました"); err != nil {
			return err
		}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd webapp/go && go test ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add webapp/go/closer.go webapp/go/closer_test.go
git commit -m "feat: 落札確定時に落札者へwon通知を送る"
```

---

### Task 14: `GET /notifications` の実装

**Files:**
- Create: `webapp/go/notifications.go`, `webapp/go/notifications_test.go`
- Modify: `webapp/go/main.go`

**Interfaces:**
- Consumes: `currentUserID`
- Produces: エンドポイント `GET /notifications`。レスポンスは `{"notifications": [{"id","type","auction_id","message","is_read","created_at"}]}` を `id DESC` 順

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/notifications_test.go` を新規作成:

```go
package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type notificationJSON struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	AuctionID int64     `json:"auction_id"`
	Message   string    `json:"message"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
}

type notificationsResponseJSON struct {
	Notifications []notificationJSON `json:"notifications"`
}

func getNotificationsAs(t *testing.T, c *http.Client, ts *httptest.Server) notificationsResponseJSON {
	t.Helper()
	res, err := c.Get(ts.URL + "/notifications")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /notifications status = %d, want 200", res.StatusCode)
	}
	var body notificationsResponseJSON
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestGetNotifications(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	// user 5 が auction 1 に2回入札 → user 2,3,4 に各2件の outbid 通知
	bidder := loginAs(t, ts, "seed_user_05")
	postBid(t, bidder, ts, 1, 1600, http.StatusCreated)
	postBid(t, bidder, ts, 1, 1700, http.StatusCreated)

	// user 2 は自分宛の2件だけを id DESC で受け取る
	c2 := loginAs(t, ts, "seed_user_02")
	body := getNotificationsAs(t, c2, ts)
	if len(body.Notifications) != 2 {
		t.Fatalf("user 2 の通知が %d件, want 2", len(body.Notifications))
	}
	for i := 1; i < len(body.Notifications); i++ {
		if body.Notifications[i].ID >= body.Notifications[i-1].ID {
			t.Errorf("id DESC でない: [%d].ID=%d, [%d].ID=%d",
				i-1, body.Notifications[i-1].ID, i, body.Notifications[i].ID)
		}
	}
	for _, n := range body.Notifications {
		if n.Type != "outbid" {
			t.Errorf("type = %q, want outbid", n.Type)
		}
		if n.AuctionID != 1 {
			t.Errorf("auction_id = %d, want 1", n.AuctionID)
		}
		if n.Message == "" {
			t.Error("message が空")
		}
		if n.IsRead {
			t.Error("is_read = true, want false")
		}
	}

	// 入札していない user 10 には通知が来ない(空配列、null ではない)
	c10 := loginAs(t, ts, "seed_user_10")
	empty := getNotificationsAs(t, c10, ts)
	if empty.Notifications == nil {
		t.Error("notifications が null (期待: 空配列)")
	}
	if len(empty.Notifications) != 0 {
		t.Errorf("user 10 の通知が %d件, want 0", len(empty.Notifications))
	}
}

func TestGetNotificationsRequiresLogin(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	res, err := http.Get(ts.URL + "/notifications")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.StatusCode)
	}
}
```

`import` に `"net/http/httptest"` を追加。

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd webapp/go && go test -run TestGetNotifications ./...`
Expected: FAIL（`status = 404, want 200`）

- [ ] **Step 3: 実装する**

`webapp/go/notifications.go` を新規作成:

```go
package main

import (
	"net/http"
	"time"
)

type notificationResponse struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	AuctionID int64     `json:"auction_id"`
	Message   string    `json:"message"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
}

// getNotifications は自分宛の通知を id 降順で返す。
// 既読化APIは用意していない(is_read は常に false)。
//
// 意図的に遅い実装: notifications に user_id のインデックスが無くフルスキャンになる。
func (h *handler) getNotifications(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	var rows []struct {
		ID        int64     `db:"id"`
		Type      string    `db:"type"`
		AuctionID int64     `db:"auction_id"`
		Message   string    `db:"message"`
		IsRead    bool      `db:"is_read"`
		CreatedAt time.Time `db:"created_at"`
	}
	if err := h.db.SelectContext(r.Context(), &rows,
		"SELECT id, type, auction_id, message, is_read, created_at FROM notifications WHERE user_id = ? ORDER BY id DESC",
		userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]notificationResponse, 0, len(rows))
	for _, n := range rows {
		out = append(out, notificationResponse{
			ID: n.ID, Type: n.Type, AuctionID: n.AuctionID,
			Message: n.Message, IsRead: n.IsRead, CreatedAt: n.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out})
}
```

`webapp/go/main.go` の `routerFor` にルートを追加:

```go
	r.Get("/notifications", h.getNotifications)
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd webapp/go && go test ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add webapp/go/notifications.go webapp/go/notifications_test.go webapp/go/main.go
git commit -m "feat: 通知一覧API(GET /notifications)を追加"
```

---

### Task 15: 通知期待値の純粋関数

**Files:**
- Create: `bench/notify.go`, `bench/notify_test.go`
- Modify: `bench/model.go`, `bench/client.go`, `bench/validate.go`

**Interfaces:**
- Consumes: `AcceptedBid`（`bench/ledger.go`）
- Produces: `func ExpectedOutbidCounts(byAuction map[int64][]AcceptedBid) map[int64]int64`、`func CountByType(ns []Notification, typ string) int64`、`func HasWonNotification(ns []Notification, auctionID int64) bool`、`type Notification`、`func (c *Client) GetNotifications(ctx context.Context) ([]Notification, error)`、`func ValidateNotificationsOrdered(ns []Notification) error`

- [ ] **Step 1: 失敗するテストを書く**

`bench/notify_test.go` を新規作成:

```go
package main

import "testing"

// ExpectedOutbidCounts はファンアウト範囲(そのオークションに入札済みの全ユーザー)に
// 対応した「受け取るべき outbid 通知数の下限」を台帳から計算する。
func TestExpectedOutbidCounts(t *testing.T) {
	tests := []struct {
		name      string
		byAuction map[int64][]AcceptedBid
		want      map[int64]int64
	}{
		{
			name:      "入札なし",
			byAuction: map[int64][]AcceptedBid{},
			want:      map[int64]int64{},
		},
		{
			name: "単独入札者は誰にも抜かれないので0件",
			byAuction: map[int64][]AcceptedBid{
				1: {{BidID: 10, AuctionID: 1, UserID: 5, Amount: 1600}},
			},
			want: map[int64]int64{},
		},
		{
			name: "2人が交互に入札: 先行者は後続2件、後続者は先行者の2回目1件",
			byAuction: map[int64][]AcceptedBid{
				1: {
					{BidID: 10, AuctionID: 1, UserID: 5, Amount: 1600},
					{BidID: 11, AuctionID: 1, UserID: 6, Amount: 1700},
					{BidID: 12, AuctionID: 1, UserID: 5, Amount: 1800},
					{BidID: 13, AuctionID: 1, UserID: 6, Amount: 1900},
				},
			},
			// user 5 の最初は id=10。それより後の他ユーザー入札は id=11,13 の2件
			// user 6 の最初は id=11。それより後の他ユーザー入札は id=12 の1件
			want: map[int64]int64{5: 2, 6: 1},
		},
		{
			name: "台帳の並びが受理順でなくても BidID で正しく順序づけられる",
			byAuction: map[int64][]AcceptedBid{
				1: {
					{BidID: 13, AuctionID: 1, UserID: 6, Amount: 1900},
					{BidID: 10, AuctionID: 1, UserID: 5, Amount: 1600},
					{BidID: 12, AuctionID: 1, UserID: 5, Amount: 1800},
					{BidID: 11, AuctionID: 1, UserID: 6, Amount: 1700},
				},
			},
			want: map[int64]int64{5: 2, 6: 1},
		},
		{
			name: "複数オークションは合算される",
			byAuction: map[int64][]AcceptedBid{
				1: {
					{BidID: 10, AuctionID: 1, UserID: 5, Amount: 1600},
					{BidID: 11, AuctionID: 1, UserID: 6, Amount: 1700},
				},
				2: {
					{BidID: 20, AuctionID: 2, UserID: 5, Amount: 2600},
					{BidID: 21, AuctionID: 2, UserID: 7, Amount: 2700},
				},
			},
			want: map[int64]int64{5: 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpectedOutbidCounts(tt.byAuction)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for uid, want := range tt.want {
				if got[uid] != want {
					t.Errorf("user %d: got %d, want %d (全体: %v)", uid, got[uid], want, got)
				}
			}
		})
	}
}

func TestNotificationHelpers(t *testing.T) {
	ns := []Notification{
		{ID: 3, Type: "won", AuctionID: 4},
		{ID: 2, Type: "outbid", AuctionID: 1},
		{ID: 1, Type: "outbid", AuctionID: 1},
	}
	if got := CountByType(ns, "outbid"); got != 2 {
		t.Errorf("CountByType(outbid) = %d, want 2", got)
	}
	if got := CountByType(ns, "won"); got != 1 {
		t.Errorf("CountByType(won) = %d, want 1", got)
	}
	if !HasWonNotification(ns, 4) {
		t.Error("auction 4 の won 通知が見つからない")
	}
	if HasWonNotification(ns, 1) {
		t.Error("auction 1 に won 通知は無いはず")
	}

	if err := ValidateNotificationsOrdered(ns); err != nil {
		t.Errorf("id DESC の通知列が拒否された: %v", err)
	}
	asc := []Notification{{ID: 1}, {ID: 2}}
	if err := ValidateNotificationsOrdered(asc); err == nil {
		t.Error("id ASC が検出されなかった")
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd bench && go test -run 'TestExpectedOutbidCounts|TestNotificationHelpers' ./...`
Expected: コンパイルエラー（`ExpectedOutbidCounts` / `Notification` 未定義）

- [ ] **Step 3: 実装する**

`bench/model.go` に追記:

```go
type Notification struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	AuctionID int64     `json:"auction_id"`
	Message   string    `json:"message"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
}
```

`bench/notify.go` を新規作成:

```go
package main

import "sort"

// ExpectedOutbidCounts は台帳の確定受理入札から、各ユーザーが受け取るべき
// outbid 通知数の「下限」を計算する。
//
// 参照実装のファンアウトは「そのオークションに入札済みの全ユーザー(今回の入札者を除く)」
// 宛なので、ユーザー U が受け取るべき件数は
//
//	U が入札した各オークション A について、A 上で U の最初の入札より後に
//	受理された他ユーザーの入札の件数。その総和。
//
// で決まる。受理順は BidID の昇順(入札APIがオークション行を FOR UPDATE で保持したまま
// INSERT するため、同一オークション内では id 順 = 受理順)で定める。
//
// これが「下限」なのは2点による:
//   - pending(結果不明)入札は数えていない
//   - ベンチはシードユーザーとしてログインするため、そのユーザーにはシード入札
//     (走行前の入札)がある場合があり、実際の期待件数はこれ以上になりうる
//
// したがって検証は「受信数 < 下限なら違反」とする。
func ExpectedOutbidCounts(byAuction map[int64][]AcceptedBid) map[int64]int64 {
	out := map[int64]int64{}
	for _, bids := range byAuction {
		sorted := append([]AcceptedBid(nil), bids...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].BidID < sorted[j].BidID })

		firstBidID := map[int64]int64{}
		for _, b := range sorted {
			if _, ok := firstBidID[b.UserID]; !ok {
				firstBidID[b.UserID] = b.BidID
			}
		}
		for uid, first := range firstBidID {
			var n int64
			for _, b := range sorted {
				if b.UserID != uid && b.BidID > first {
					n++
				}
			}
			if n > 0 {
				out[uid] += n
			}
		}
	}
	return out
}

// CountByType は指定 type の通知件数を返す。
func CountByType(ns []Notification, typ string) int64 {
	var n int64
	for _, x := range ns {
		if x.Type == typ {
			n++
		}
	}
	return n
}

// HasWonNotification は指定オークションの落札通知があるかを返す。
func HasWonNotification(ns []Notification, auctionID int64) bool {
	for _, x := range ns {
		if x.Type == "won" && x.AuctionID == auctionID {
			return true
		}
	}
	return false
}
```

`bench/validate.go` に追記:

```go
// ValidateNotificationsOrdered は通知一覧が id 降順であることを検証する。
func ValidateNotificationsOrdered(ns []Notification) error {
	for i := 1; i < len(ns); i++ {
		if ns[i].ID >= ns[i-1].ID {
			return fmt.Errorf("GET /notifications: id 降順でない (index %d: id=%d の前が id=%d)",
				i, ns[i].ID, ns[i-1].ID)
		}
	}
	return nil
}
```

`bench/client.go` に追記:

```go
// GetNotifications は自分宛の通知一覧を取得する。
func (c *Client) GetNotifications(ctx context.Context) ([]Notification, error) {
	code, b, err := c.doJSON(ctx, http.MethodGet, "/notifications", nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET /notifications: status %d (body: %s)", code, b)
	}
	var body struct {
		Notifications []Notification `json:"notifications"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, fmt.Errorf("GET /notifications: 不正なJSON: %w", err)
	}
	return body.Notifications, nil
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd bench && go test ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add bench/notify.go bench/notify_test.go bench/model.go bench/client.go bench/validate.go
git commit -m "feat: 通知期待値の純粋関数と通知取得クライアントを追加"
```

---

### Task 16: 通知確認シナリオと Validation の欠落照合

**Files:**
- Modify: `bench/load.go`, `bench/scenario.go`, `bench/score.go`, `bench/main.go`

**Interfaces:**
- Consumes: `ExpectedOutbidCounts` / `CountByType` / `HasWonNotification` / `ValidateNotificationsOrdered`（Task 15）
- Produces: `const ScoreGETNotifications score.ScoreTag`、`Scenario.Notifiers int`、`func (s *Scenario) notifierIteration(...)`、`func (s *Scenario) validateNotifications(...)`

- [ ] **Step 1: スコアタグとフラグを足す**

`bench/score.go`:

```go
	ScoreGETNotifications score.ScoreTag = "GET /notifications"
```

`scoreTable` に `ScoreGETNotifications: 2,` を追加。

`bench/main.go`:
- フラグを追加: `notifiers := flag.Int("notifiers", 2, "通知確認worker数")`
- `Scenario` の初期化に `Notifiers: *notifiers,` を追加
- breakdown 出力リストに `{ScoreGETNotifications, "GET /notifications"},` を追加

`bench/scenario.go` の `Scenario` 構造体に `Notifiers int` を追加。

- [ ] **Step 2: 通知確認シナリオを実装する**

`bench/load.go` に追記:

```go
// notifierIteration は「ログイン→通知一覧」の回遊。Load中のスコア源であり、
// 一覧の順序(id DESC)を検証する。欠落そのものは Validation フェーズで照合する。
func (s *Scenario) notifierIteration(ctx context.Context, step *isucandar.BenchmarkStep) {
	c, err := NewClient(s.Target)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	if _, err := c.Login(ctx, seedUserName(), "password"); err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	ns, err := c.GetNotifications(ctx)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(ScoreGETNotifications)
	if err := ValidateNotificationsOrdered(ns); err != nil {
		addErr(ctx, step, ErrCritical, err)
	}
}
```

`bench/scenario.go` の `Load` にワーカーを追加（既存の bidder/watcher と同じ形）:

```go
	notifier, err := worker.NewWorker(func(ctx context.Context, _ int) {
		s.notifierIteration(ctx, step)
	}, worker.WithInfinityLoop(), worker.WithMaxParallelism(int32(s.Notifiers)))
	if err != nil {
		return err
	}
```

`wg.Add(2)` を `wg.Add(3)` に変え、`go func() { defer wg.Done(); notifier.Process(ctx) }()` を追加する。

- [ ] **Step 3: Validation の欠落照合を実装する**

`bench/scenario.go` に追記:

```go
// notifyExpectation は1ユーザーぶんの通知期待値。
type notifyExpectation struct {
	MinOutbid   int64
	WonAuctions []int64
}

// validateNotifications は通知の欠落を照合する。
//   - 各入札ユーザーの outbid 通知数が台帳から導いた下限を下回らないこと
//   - 落札者に該当オークションの won 通知が届いていること
//   - 一度も入札していない新規ユーザーの通知が0件であること(他人宛の混入検出)
//
// ベンチの入札者はシードユーザーのみなので、user id から seed_user_%02d でログイン名を
// 逆引きできる(Global Constraints 参照)。
func (s *Scenario) validateNotifications(ctx context.Context, step *isucandar.BenchmarkStep,
	acceptedByAuction map[int64][]AcceptedBid, winners map[int64]int64) {

	want := map[int64]*notifyExpectation{}
	for uid, n := range ExpectedOutbidCounts(acceptedByAuction) {
		want[uid] = &notifyExpectation{MinOutbid: n}
	}
	for auctionID, winnerID := range winners {
		e, ok := want[winnerID]
		if !ok {
			e = &notifyExpectation{}
			want[winnerID] = e
		}
		e.WonAuctions = append(e.WonAuctions, auctionID)
	}

	for uid, e := range want {
		if uid < 1 || uid > 20 {
			// シードユーザー以外はログイン名を逆引きできないため検証対象外
			continue
		}
		uc, err := NewClient(s.Target)
		if err != nil {
			step.AddError(failure.NewError(ErrApplication, err))
			continue
		}
		if _, err := uc.Login(ctx, fmt.Sprintf("seed_user_%02d", uid), "password"); err != nil {
			step.AddError(failure.NewError(ErrApplication, err))
			continue
		}
		ns, err := uc.GetNotifications(ctx)
		if err != nil {
			step.AddError(failure.NewError(ErrApplication, err))
			continue
		}
		if err := ValidateNotificationsOrdered(ns); err != nil {
			step.AddError(failure.NewError(ErrCritical, err))
		}
		if got := CountByType(ns, "outbid"); got < e.MinOutbid {
			step.AddError(failure.NewError(ErrCritical,
				fmt.Errorf("user %d: outbid通知が %d件 (期待: %d件以上、欠落の疑い)", uid, got, e.MinOutbid)))
		}
		for _, auctionID := range e.WonAuctions {
			if !HasWonNotification(ns, auctionID) {
				step.AddError(failure.NewError(ErrCritical,
					fmt.Errorf("user %d: auction %d を落札したのに won通知が無い", uid, auctionID)))
			}
		}
	}

	// 一度も入札していない新規ユーザーの通知は0件でなければならない。
	// (user_id で絞らず全件返す実装を検出する)
	fresh, err := NewClient(s.Target)
	if err != nil {
		step.AddError(failure.NewError(ErrApplication, err))
		return
	}
	if _, err := fresh.Register(ctx, randomName("bench_notify_"), "benchpassword"); err != nil {
		step.AddError(failure.NewError(ErrApplication, err))
		return
	}
	ns, err := fresh.GetNotifications(ctx)
	if err != nil {
		step.AddError(failure.NewError(ErrApplication, err))
		return
	}
	if len(ns) != 0 {
		step.AddError(failure.NewError(ErrCritical,
			fmt.Errorf("入札していない新規ユーザーに通知が %d件 (期待: 0件、他人宛の混入)", len(ns))))
	}
}
```

`Validation` 内で落札者を集めて呼び出す。既存のループを次のように変更する（`winners` の収集を追加し、ループ後に `validateNotifications` を呼ぶ）:

```go
	winners := map[int64]int64{} // auctionID -> winnerID
	for auctionID, want := range expectedInitialAuctions {
		d, err := c.GetAuctionRetry(ctx, auctionID, 3, 100*time.Millisecond)
		if err != nil {
			step.AddError(failure.NewError(ErrCritical, fmt.Errorf("auction %d: %w", auctionID, err)))
			continue
		}
		for _, e := range reconcileAuction(auctionID, d, want.BidCount, want.CurrentPrice,
			acceptedByAuction[auctionID], pendingByAuction[auctionID]) {
			step.AddError(failure.NewError(ErrCritical, e))
		}
		for _, e := range reconcileClosedAuction(auctionID, d) {
			step.AddError(failure.NewError(ErrCritical, e))
		}
		if err := ValidateAuctionClosedIfDue(d, time.Now().UTC(), closeGrace); err != nil {
			step.AddError(failure.NewError(ErrCritical, err))
		}
		if d.Status == "closed" && d.WinnerID != nil {
			winners[auctionID] = *d.WinnerID
		}
	}
	s.validateNotifications(ctx, step, acceptedByAuction, winners)
```

- [ ] **Step 4: ビルドしてベンチを通す**

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
cd bench && go test ./... && go run . -target http://localhost:8080 -duration 60s
```
Expected: `RESULT: PASS`。breakdown に `GET /notifications` の行が出る

- [ ] **Step 5: コミット**

```bash
git add bench/load.go bench/scenario.go bench/score.go bench/main.go
git commit -m "feat: 通知確認シナリオとValidationの通知欠落照合を追加"
```

---

### Task 17: 3-3 のベンチのベンチ

**Files:**
- Modify: `docs/phase3-notes.md`

**Interfaces:**
- Consumes: Task 16 の `validateNotifications`
- Produces: `docs/phase3-notes.md` への追記

- [ ] **Step 1: outbid ファンアウトを削って FAIL することを確認**

`webapp/go/bids.go` のファンアウトのループ本体（`INSERT INTO notifications`）を一時的にコメントアウトして再ビルド・走行。

```bash
docker compose -f dev/compose.yaml up -d --build
cd bench && go run . -target http://localhost:8080 -duration 60s
```
Expected: `RESULT: FAIL`。`outbid通知が N件 (期待: M件以上、欠落の疑い)` の critical

- [ ] **Step 2: won 通知を削って FAIL することを確認**

`bids.go` を戻し、`webapp/go/closer.go` の won 通知 INSERT を一時的にコメントアウトして再ビルド・走行。

Expected: `RESULT: FAIL`。`auction N を落札したのに won通知が無い` の critical

- [ ] **Step 3: 他人宛の通知を混ぜて FAIL することを確認**

`closer.go` を戻し、`webapp/go/notifications.go` のクエリから `WHERE user_id = ?` を外す（引数も外す）よう一時的に変更して再ビルド・走行。

Expected: `RESULT: FAIL`。`入札していない新規ユーザーに通知が N件` の critical

- [ ] **Step 4: 破壊を戻して PASS を再確認**

```bash
git checkout webapp/go/bids.go webapp/go/closer.go webapp/go/notifications.go
docker compose -f dev/compose.yaml up -d --build
cd bench && go run . -target http://localhost:8080 -duration 60s
```
Expected: `RESULT: PASS`、critical 0件

- [ ] **Step 5: `docs/phase3-notes.md` に追記**

`<...>` は Step 1〜4 の実際の出力で埋める（プレースホルダのまま残さない）。

```markdown
## 3-3 通知

### 設計判断

- **outbid の期待値は「抜かれた回数」ではない**: ファンアウトは入札済み全員宛なので、
  期待件数は「自分が入札した各オークションで、自分の最初の入札より後に受理された
  他ユーザーの入札の総数」。両者は一致しないため、混同すると検証がずれる
- **下限比較にとどめる**: pending 入札を数えず、かつベンチはシードユーザーとして
  ログインするためシード入札ぶんの通知が上乗せされうる。「受信数 < 下限なら違反」とする
- **「自分宛のみ」は新規ユーザーの0件で検出する**: レスポンスに user_id を含めずに
  他人宛の混入を検出するため、一度も入札していない新規ユーザーの通知が0件であることを
  Validation で確認する

### ベンチのベンチ実測(2026-08-29)

| 壊し方 | 結果 |
|---|---|
| outbid ファンアウトを削除 | `RESULT: FAIL` / critical <N>件 / `outbid通知が ... (期待: ...件以上)` |
| won 通知を削除 | `RESULT: FAIL` / critical <N>件 / `落札したのに won通知が無い` |
| 通知一覧から `WHERE user_id = ?` を削除 | `RESULT: FAIL` / critical <N>件 / `入札していない新規ユーザーに通知が ...件` |
| 復元後 | `RESULT: PASS` / critical 0件 / `SCORE: <点>` |
```

- [ ] **Step 6: コミット**

```bash
git add docs/phase3-notes.md
git commit -m "docs: 3-3のベンチのベンチ実測を記録"
```

---

## 3-4: 出品

### Task 18: `POST /auctions`（出品）

**Files:**
- Modify: `webapp/go/auctions.go`, `webapp/go/main.go`
- Test: `webapp/go/auctions_test.go`

**Interfaces:**
- Consumes: `currentUserID`
- Produces: エンドポイント `POST /auctions`。201 応答は `{"id","title","starting_price","ends_at","status"}`

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/auctions_test.go` に追記:

```go
type auctionCreatedJSON struct {
	ID            int64     `json:"id"`
	Title         string    `json:"title"`
	StartingPrice int64     `json:"starting_price"`
	EndsAt        time.Time `json:"ends_at"`
	Status        string    `json:"status"`
}

func TestPostAuction(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	c := loginAs(t, ts, "seed_user_03")

	before := time.Now().UTC()
	res, err := c.Post(ts.URL+"/auctions", "application/json", strings.NewReader(
		`{"title":"テスト椅子","description":"説明","category_id":1,"starting_price":5000,"duration_seconds":30}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", res.StatusCode)
	}
	var created auctionCreatedJSON
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 {
		t.Error("id が 0")
	}
	if created.Status != "live" {
		t.Errorf("status = %q, want live", created.Status)
	}
	if created.StartingPrice != 5000 {
		t.Errorf("starting_price = %d, want 5000", created.StartingPrice)
	}
	// ends_at は now + 30秒 のはず
	lo, hi := before.Add(29*time.Second), time.Now().UTC().Add(31*time.Second)
	if created.EndsAt.Before(lo) || created.EndsAt.After(hi) {
		t.Errorf("ends_at = %v, want in [%v, %v]", created.EndsAt, lo, hi)
	}

	// 一覧に live として現れ、詳細も引ける
	var d auctionDetailJSON
	getJSON(t, fmt.Sprintf("%s/auctions/%d", ts.URL, created.ID), &d)
	if d.Status != "live" || d.CurrentPrice != 5000 || len(d.Bids) != 0 {
		t.Errorf("詳細が不正: status=%q current_price=%d bids=%d", d.Status, d.CurrentPrice, len(d.Bids))
	}
	if d.Seller.Name != "seed_user_03" {
		t.Errorf("seller = %q, want seed_user_03", d.Seller.Name)
	}
}

func TestPostAuctionValidation(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	c := loginAs(t, ts, "seed_user_03")

	for _, tt := range []struct {
		name string
		body string
		want int
	}{
		{"title が空", `{"title":"","description":"d","category_id":1,"starting_price":5000,"duration_seconds":30}`, http.StatusBadRequest},
		{"starting_price が0", `{"title":"t","description":"d","category_id":1,"starting_price":0,"duration_seconds":30}`, http.StatusBadRequest},
		{"duration が短すぎる", `{"title":"t","description":"d","category_id":1,"starting_price":5000,"duration_seconds":5}`, http.StatusBadRequest},
		{"duration が長すぎる", `{"title":"t","description":"d","category_id":1,"starting_price":5000,"duration_seconds":301}`, http.StatusBadRequest},
		{"存在しないカテゴリ", `{"title":"t","description":"d","category_id":999,"starting_price":5000,"duration_seconds":30}`, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res, err := c.Post(ts.URL+"/auctions", "application/json", strings.NewReader(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", res.StatusCode, tt.want)
			}
		})
	}

	// 未ログインは 401
	res, err := http.Post(ts.URL+"/auctions", "application/json", strings.NewReader(
		`{"title":"t","description":"d","category_id":1,"starting_price":5000,"duration_seconds":30}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("未ログイン status = %d, want 401", res.StatusCode)
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd webapp/go && go test -run TestPostAuction ./...`
Expected: FAIL（`status = 405, want 201` — ルート未登録）

- [ ] **Step 3: 実装する**

`webapp/go/auctions.go` に追記:

```go
// postAuction は新規出品。出品と同時に live になる(upcoming を経由しない)。
func (h *handler) postAuction(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	var req struct {
		Title           string `json:"title"`
		Description     string `json:"description"`
		CategoryID      int64  `json:"category_id"`
		StartingPrice   int64  `json:"starting_price"`
		DurationSeconds int64  `json:"duration_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" || len([]rune(req.Title)) > 255 {
		writeError(w, http.StatusBadRequest, "invalid title")
		return
	}
	if req.StartingPrice < 1 {
		writeError(w, http.StatusBadRequest, "invalid starting_price")
		return
	}
	if req.DurationSeconds < 10 || req.DurationSeconds > 300 {
		writeError(w, http.StatusBadRequest, "invalid duration_seconds")
		return
	}
	var exists int
	err := h.db.GetContext(r.Context(), &exists, "SELECT 1 FROM categories WHERE id = ?", req.CategoryID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusBadRequest, "invalid category_id")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	now := time.Now().UTC()
	endsAt := now.Add(time.Duration(req.DurationSeconds) * time.Second)
	res, err := h.db.ExecContext(r.Context(),
		"INSERT INTO auctions (seller_id, category_id, title, description, starting_price, starts_at, ends_at, status) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, 'live')",
		userID, req.CategoryID, req.Title, req.Description, req.StartingPrice, now, endsAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":             id,
		"title":          req.Title,
		"starting_price": req.StartingPrice,
		"ends_at":        endsAt,
		"status":         "live",
	})
}
```

`import` に `"encoding/json"` を追加。

`webapp/go/main.go` の `routerFor` にルートを追加:

```go
	r.Post("/auctions", h.postAuction)
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd webapp/go && go test ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add webapp/go/auctions.go webapp/go/auctions_test.go webapp/go/main.go
git commit -m "feat: 出品API(POST /auctions)を追加"
```

---

### Task 19: `GET /stats/me`（売上ダッシュボード）

**Files:**
- Create: `webapp/go/stats.go`, `webapp/go/stats_test.go`
- Modify: `webapp/go/main.go`

**Interfaces:**
- Consumes: `currentUserID`
- Produces: エンドポイント `GET /stats/me`。レスポンスは `{"listed_count","sold_count","total_sales","live_count"}`

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/stats_test.go` を新規作成:

```go
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

type statsJSON struct {
	ListedCount int64 `json:"listed_count"`
	SoldCount   int64 `json:"sold_count"`
	TotalSales  int64 `json:"total_sales"`
	LiveCount   int64 `json:"live_count"`
}

func TestGetStatsMe(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	h := newTestHandler(t)

	// seed: user 11 は auction 11 のみを出品しており、closed / winner 12 / 12000
	c11 := loginAs(t, ts, "seed_user_11")
	res, err := c11.Get(ts.URL + "/stats/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var s statsJSON
	if err := json.NewDecoder(res.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.ListedCount != 1 {
		t.Errorf("listed_count = %d, want 1", s.ListedCount)
	}
	if s.SoldCount != 1 {
		t.Errorf("sold_count = %d, want 1", s.SoldCount)
	}
	if s.TotalSales != 12000 {
		t.Errorf("total_sales = %d, want 12000", s.TotalSales)
	}
	if s.LiveCount != 0 {
		t.Errorf("live_count = %d, want 0", s.LiveCount)
	}

	// seed: user 1 は auction 1 のみ出品、live で未落札
	c1 := loginAs(t, ts, "seed_user_01")
	res2, err := c1.Get(ts.URL + "/stats/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	var s1 statsJSON
	if err := json.NewDecoder(res2.Body).Decode(&s1); err != nil {
		t.Fatal(err)
	}
	if s1.ListedCount != 1 || s1.SoldCount != 0 || s1.TotalSales != 0 || s1.LiveCount != 1 {
		t.Errorf("user 1 stats = %+v, want listed=1 sold=0 total=0 live=1", s1)
	}

	// auction 1 を閉じると sold と total_sales が動く(落札額 1500 / user 4)
	if _, err := h.db.ExecContext(context.Background(),
		"UPDATE auctions SET ends_at = DATE_SUB(NOW(6), INTERVAL 1 SECOND) WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.closeDueAuctions(context.Background()); err != nil {
		t.Fatal(err)
	}
	res3, err := c1.Get(ts.URL + "/stats/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res3.Body.Close()
	var s2 statsJSON
	if err := json.NewDecoder(res3.Body).Decode(&s2); err != nil {
		t.Fatal(err)
	}
	if s2.SoldCount != 1 || s2.TotalSales != 1500 || s2.LiveCount != 0 {
		t.Errorf("落札後 stats = %+v, want sold=1 total=1500 live=0", s2)
	}
}

func TestGetStatsMeRequiresLogin(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	res, err := http.Get(ts.URL + "/stats/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.StatusCode)
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd webapp/go && go test -run TestGetStatsMe ./...`
Expected: FAIL（`status = 404, want 200`）

- [ ] **Step 3: 実装する**

`webapp/go/stats.go` を新規作成:

```go
package main

import (
	"database/sql"
	"net/http"
)

// getStatsMe は出品者の売上サマリを返す。
//
// 意図的に遅い実装: auctions に seller_id のインデックスが無いうえ、
// 1本のクエリにまとめず4回に分けて全走査する。
func (h *handler) getStatsMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	ctx := r.Context()

	var listed int64
	if err := h.db.GetContext(ctx, &listed,
		"SELECT COUNT(*) FROM auctions WHERE seller_id = ?", userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var sold int64
	if err := h.db.GetContext(ctx, &sold,
		"SELECT COUNT(*) FROM auctions WHERE seller_id = ? AND status = 'closed' AND winner_id IS NOT NULL",
		userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var total sql.NullInt64
	if err := h.db.GetContext(ctx, &total,
		"SELECT SUM(winning_price) FROM auctions WHERE seller_id = ? AND status = 'closed' AND winner_id IS NOT NULL",
		userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var live int64
	if err := h.db.GetContext(ctx, &live,
		"SELECT COUNT(*) FROM auctions WHERE seller_id = ? AND status = 'live'", userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]int64{
		"listed_count": listed,
		"sold_count":   sold,
		"total_sales":  total.Int64,
		"live_count":   live,
	})
}
```

`webapp/go/main.go` の `routerFor` にルートを追加:

```go
	r.Get("/stats/me", h.getStatsMe)
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd webapp/go && go test ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add webapp/go/stats.go webapp/go/stats_test.go webapp/go/main.go
git commit -m "feat: 売上ダッシュボードAPI(GET /stats/me)を追加"
```

---

### Task 20: 出品者シナリオと pubsub 連携

**Files:**
- Modify: `bench/model.go`, `bench/client.go`, `bench/ledger.go`, `bench/load.go`, `bench/scenario.go`, `bench/score.go`, `bench/main.go`
- Test: `bench/ledger_test.go`

**Interfaces:**
- Consumes: `POST /auctions` / `GET /stats/me`（Task 18・19）
- Produces: `type Listing`、`func (l *Ledger) RecordListing(li Listing)`、`func (l *Ledger) Listings() []Listing`、`type listingBoard`、`Scenario.Sellers int` / `Scenario.Listings *pubsub.PubSub` / `Scenario.Board *listingBoard`、`const ScorePOSTAuction score.ScoreTag`

- [ ] **Step 1: 台帳の出品記録に失敗するテストを書く**

`bench/ledger_test.go` に追記:

```go
// 出品台帳は Validation が新規オークションを検証対象に含めるために使う。
func TestLedgerListings(t *testing.T) {
	l := NewLedger()
	if got := l.Listings(); len(got) != 0 {
		t.Fatalf("初期状態で %d件, want 0", len(got))
	}
	l.RecordListing(Listing{AuctionID: 13, SellerID: 3, StartingPrice: 5000})
	l.RecordListing(Listing{AuctionID: 14, SellerID: 4, StartingPrice: 6000})

	got := l.Listings()
	if len(got) != 2 {
		t.Fatalf("%d件, want 2", len(got))
	}
	byID := map[int64]Listing{}
	for _, li := range got {
		byID[li.AuctionID] = li
	}
	if byID[13].StartingPrice != 5000 || byID[13].SellerID != 3 {
		t.Errorf("auction 13 = %+v", byID[13])
	}
	if byID[14].StartingPrice != 6000 {
		t.Errorf("auction 14 = %+v", byID[14])
	}

	// 返り値はコピーであること(以降の RecordListing に影響されない)
	l.RecordListing(Listing{AuctionID: 15, SellerID: 5, StartingPrice: 7000})
	if len(got) != 2 {
		t.Errorf("返り値が共有されている: %d件", len(got))
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd bench && go test -run TestLedgerListings ./...`
Expected: コンパイルエラー `undefined: Listing`

- [ ] **Step 3: 台帳を実装する**

`bench/ledger.go` に追記:

```go
// Listing はベンチが出品したオークションの記録。Validation の検証対象に含めるために使う。
type Listing struct {
	AuctionID     int64
	SellerID      int64
	StartingPrice int64
}
```

`Ledger` 構造体に `listings []Listing` を追加し、以下を追記:

```go
func (l *Ledger) RecordListing(li Listing) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.listings = append(l.listings, li)
}

// Listings は出品記録のコピーを返す。
func (l *Ledger) Listings() []Listing {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Listing, len(l.listings))
	copy(out, l.listings)
	return out
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd bench && go test -run TestLedgerListings ./...`
Expected: PASS

- [ ] **Step 5: クライアントとモデルを足す**

`bench/model.go` に追記:

```go
type AuctionCreated struct {
	ID            int64     `json:"id"`
	Title         string    `json:"title"`
	StartingPrice int64     `json:"starting_price"`
	EndsAt        time.Time `json:"ends_at"`
	Status        string    `json:"status"`
}

type Stats struct {
	ListedCount int64 `json:"listed_count"`
	SoldCount   int64 `json:"sold_count"`
	TotalSales  int64 `json:"total_sales"`
	LiveCount   int64 `json:"live_count"`
}
```

`bench/client.go` に追記:

```go
// PostAuction は出品する。
func (c *Client) PostAuction(ctx context.Context, title, description string,
	categoryID, startingPrice, durationSeconds int64) (*AuctionCreated, error) {
	code, b, err := c.doJSON(ctx, http.MethodPost, "/auctions", map[string]any{
		"title": title, "description": description, "category_id": categoryID,
		"starting_price": startingPrice, "duration_seconds": durationSeconds,
	})
	if err != nil {
		return nil, err
	}
	if code != http.StatusCreated {
		return nil, fmt.Errorf("POST /auctions: status %d (期待: 201, body: %s)", code, b)
	}
	var a AuctionCreated
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("POST /auctions: 不正なJSON: %w", err)
	}
	return &a, nil
}

// GetStatsMe は出品者の売上サマリを取得する。
func (c *Client) GetStatsMe(ctx context.Context) (*Stats, error) {
	code, b, err := c.doJSON(ctx, http.MethodGet, "/stats/me", nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET /stats/me: status %d (body: %s)", code, b)
	}
	var s Stats
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("GET /stats/me: 不正なJSON: %w", err)
	}
	return &s, nil
}
```

- [ ] **Step 6: 出品者シナリオと配信板を実装する**

`bench/load.go` に追記:

```go
// listingBoard は pubsub 経由で配信された新規出品IDを保持する。
//
// isucandar の pubsub.Publish は購読チャネルが満杯だとブロックするため、
// 購読ハンドラは必ず即座に返らなければならない(ここではスライスへの追記のみ)。
type listingBoard struct {
	mu  sync.Mutex
	ids []int64
}

func (b *listingBoard) add(id int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ids = append(b.ids, id)
}

// random は配信済みの出品からランダムに1件返す。1件も無ければ ok=false。
func (b *listingBoard) random() (int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.ids) == 0 {
		return 0, false
	}
	return b.ids[rand.Intn(len(b.ids))], true
}

var sellerTitles = []string{
	"ラピッドチェア", "オークリーフ・スツール", "ミニマルワークシート",
	"ベルベット・オットマン", "スカンジ・ダイニング",
}

// sellerIteration は「ログイン→出品→売上確認」の1セッション。
// 出品したオークションは pubsub で入札者シナリオへ配信され、入札が集まる。
// duration は20〜40秒なので、走行中に closed へ遷移して落札 Validation の対象になる。
func (s *Scenario) sellerIteration(ctx context.Context, step *isucandar.BenchmarkStep) {
	c, err := NewClient(s.Target)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	user, err := c.Login(ctx, seedUserName(), "password")
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	title := sellerTitles[rand.Intn(len(sellerTitles))]
	startingPrice := int64(1000 + rand.Intn(9)*500)
	duration := int64(20 + rand.Intn(21)) // 20〜40秒
	created, err := c.PostAuction(ctx, title, "ベンチが出品した椅子",
		int64(1+rand.Intn(3)), startingPrice, duration)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(ScorePOSTAuction)
	if created.Status != "live" || created.StartingPrice != startingPrice {
		addErr(ctx, step, ErrCritical,
			fmt.Errorf("POST /auctions: 応答が不一致 (status=%q starting_price=%d, 期待: live/%d)",
				created.Status, created.StartingPrice, startingPrice))
		return
	}
	s.Ledger.RecordListing(Listing{
		AuctionID: created.ID, SellerID: user.ID, StartingPrice: created.StartingPrice,
	})
	s.Listings.Publish(created.ID)

	stats, err := c.GetStatsMe(ctx)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	// 出品直後なので、出品数も live 数も最低1件はあるはず。
	if stats.ListedCount < 1 || stats.LiveCount < 1 {
		addErr(ctx, step, ErrCritical,
			fmt.Errorf("GET /stats/me: 出品直後なのに listed_count=%d live_count=%d",
				stats.ListedCount, stats.LiveCount))
	}
}
```

`import` に `"sync"` を追加。

- [ ] **Step 7: 入札者が新規出品にも入札するようにする**

`bidderIteration` の `target := list[rand.Intn(len(list))]` を差し替える:

```go
	targetID := list[rand.Intn(len(list))].ID
	// 新規出品には pubsub 経由で人が集まる(終了間際に競りが起きる挙動の再現)。
	// 既に closed になっていた場合は詳細取得後の status チェックで抜ける。
	if id, ok := s.Board.random(); ok && rand.Intn(2) == 0 {
		targetID = id
	}
```

以降のループ本体の `target.ID` をすべて `targetID` に置換する。

- [ ] **Step 8: シナリオに配線する**

`bench/scenario.go` の `Scenario` 構造体に追加:

```go
	Sellers   int
	Listings  *pubsub.PubSub
	Board     *listingBoard
```

`Load` の冒頭（`if s.PrepareOnly` の直後）に購読を張る:

```go
	// 購読は worker 起動前に張る。ハンドラはスライス追記だけで即座に返るため
	// Publish 側がブロックしない。Capacity にも十分な余裕を持たせておく。
	s.Board = &listingBoard{}
	s.Listings.Subscribe(ctx, func(v interface{}) {
		if id, ok := v.(int64); ok {
			s.Board.add(id)
		}
	})
```

出品者 worker を追加し（bidder/watcher/notifier と同じ形）、`wg.Add(4)` にして `go func() { defer wg.Done(); seller.Process(ctx) }()` を足す。

`bench/main.go`:
- `sellers := flag.Int("sellers", 2, "出品者worker数")`
- `Scenario` 初期化に `Sellers: *sellers,` と `Listings: newListingPubSub(),` を追加

`bench/scenario.go` に補助関数を追加:

```go
// newListingPubSub は出品配信用の PubSub を作る。
// pubsub.Publish は購読チャネルが満杯だとブロックし、その状態で購読側が
// ctx キャンセルで閉じようとすると相互にロック待ちになりうる。
// 購読ハンドラは即座に返る実装だが、念のため十分な容量を確保しておく。
func newListingPubSub() *pubsub.PubSub {
	ps := pubsub.NewPubSub()
	ps.Capacity = 1000
	return ps
}
```

`import` に `"github.com/isucon/isucandar/pubsub"` を追加。

`bench/score.go`:

```go
	ScorePOSTAuction score.ScoreTag = "POST /auctions"
```

`scoreTable` に `ScorePOSTAuction: 5,` を追加。`bench/main.go` の breakdown 出力リストに `{ScorePOSTAuction, "POST /auctions"},` を追加。

- [ ] **Step 9: Validation を新規出品にも広げる**

`bench/scenario.go` の `Validation` を変更する。「想定外のauction」判定を出品台帳込みにし、出品ぶんも突合ループに含める:

```go
	acceptedByAuction := s.Ledger.ByAuction()
	pendingByAuction := s.Ledger.PendingByAuction()
	listings := s.Ledger.Listings()

	// ベンチが知っているオークション = 初期データ ∪ ベンチが出品したもの
	known := map[int64]bool{}
	for id := range expectedInitialAuctions {
		known[id] = true
	}
	listingByID := map[int64]Listing{}
	for _, li := range listings {
		known[li.AuctionID] = true
		listingByID[li.AuctionID] = li
	}
	for auctionID := range acceptedByAuction {
		if !known[auctionID] {
			step.AddError(failure.NewError(ErrCritical,
				fmt.Errorf("想定外のauctionに入札が受理された (auction %d)", auctionID)))
		}
	}
	for auctionID := range pendingByAuction {
		if !known[auctionID] {
			step.AddError(failure.NewError(ErrCritical,
				fmt.Errorf("想定外のauctionに未確定入札(pending)が存在 (auction %d)", auctionID)))
		}
	}
```

初期データのループの後ろに、出品ぶんのループを追加する（シード入札は無いので `seedCount=0` / `seedCurrent=出品時のstarting_price`）:

```go
	for _, li := range listings {
		d, err := c.GetAuctionRetry(ctx, li.AuctionID, 3, 100*time.Millisecond)
		if err != nil {
			step.AddError(failure.NewError(ErrCritical, fmt.Errorf("auction %d: %w", li.AuctionID, err)))
			continue
		}
		for _, e := range reconcileAuction(li.AuctionID, d, 0, li.StartingPrice,
			acceptedByAuction[li.AuctionID], pendingByAuction[li.AuctionID]) {
			step.AddError(failure.NewError(ErrCritical, e))
		}
		for _, e := range reconcileClosedAuction(li.AuctionID, d) {
			step.AddError(failure.NewError(ErrCritical, e))
		}
		if err := ValidateAuctionClosedIfDue(d, time.Now().UTC(), closeGrace); err != nil {
			step.AddError(failure.NewError(ErrCritical, err))
		}
		if d.Status == "closed" && d.WinnerID != nil {
			winners[li.AuctionID] = *d.WinnerID
		}
	}
```

（`winners` は Task 16 で導入済み。`validateNotifications` の呼び出しはこのループの後ろに移す。）

- [ ] **Step 10: ビルドしてベンチを通す**

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
cd bench && go test ./... && go run . -target http://localhost:8080 -duration 60s
```
Expected: `RESULT: PASS`。breakdown に `POST /auctions` の行が出る。critical 0件

- [ ] **Step 11: コミット**

```bash
git add bench/model.go bench/client.go bench/ledger.go bench/ledger_test.go bench/load.go bench/scenario.go bench/score.go bench/main.go
git commit -m "feat: 出品者シナリオとpubsubによるシナリオ間連携を追加"
```

---

### Task 21: Phase 3 の仕上げ（通し検証とドキュメント）

**Files:**
- Modify: `README.md`, `docs/phase2-notes.md`, `docs/phase3-notes.md`

**Interfaces:**
- Consumes: Task 1〜20 のすべて
- Produces: なし（ドキュメントのみ）

- [ ] **Step 1: 全テストを通す**

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
cd webapp/go && go test ./...
cd ../../bench && go test ./...
```
Expected: 両方 PASS

- [ ] **Step 2: 3-4 のベンチのベンチを実施する**

`webapp/go/auctions.go` の `postAuction` で `status` を `'upcoming'` にして INSERT するよう一時的に変え、再ビルド・走行。

Expected: `RESULT: FAIL`。`POST /auctions: 応答が不一致 (status=...)` か、出品したオークションが live 一覧に出ないことによる critical

戻して PASS を再確認する:
```bash
git checkout webapp/go/auctions.go
docker compose -f dev/compose.yaml up -d --build
cd bench && go run . -target http://localhost:8080 -duration 60s
```

- [ ] **Step 3: 通し走行を3回行い、スコアの再現性を確認する**

Run: `cd bench && for i in 1 2 3; do go run . -target http://localhost:8080 -duration 60s | grep -E 'SCORE|RESULT'; done`
Expected: 3回とも `RESULT: PASS`。スコアのばらつきが極端でないこと（±20%程度に収まること）

- [ ] **Step 4: 仕込みインベントリを更新する**

`docs/phase2-notes.md` の「仕込み(意図的な遅さ)のインベントリ」セクションに追記:

```markdown
- 入札フィード: `auction_id` インデックス無しのフルスキャン + 入札ごとの user 名 N+1(feed.go)
- 通知一覧: `user_id` インデックス無しのフルスキャン(notifications.go)
- 通知ファンアウト: 入札トランザクション内で入札者ごとに1行ずつ INSERT(bids.go)
- 終了処理バッチ: 毎秒フルスキャン + 1件ずつ逐次処理 + `bids` の非インデックス走査(closer.go)
- `GET /stats/me`: `seller_id` インデックス無しの全走査を4クエリに分けて実行(stats.go)
```

- [ ] **Step 5: `docs/phase3-notes.md` に 3-4 と総括を追記**

```markdown
## 3-4 出品

### 設計判断

- **出品は即 live**: upcoming を経由させると60秒走行のうち待ち時間が無駄になる。
  `duration_seconds` は 20〜40秒で、走行中に closed へ遷移して落札 Validation の対象になる
- **pubsub の購読ハンドラは即座に返す**: isucandar の `pubsub.Publish` は購読チャネルが
  満杯だとブロックするため、ハンドラはスライスへの追記のみとし、Capacity にも余裕(1000)を持たせた

### ベンチのベンチ実測(2026-08-29)

| 壊し方 | 結果 |
|---|---|
| 出品を `status='upcoming'` で INSERT | `RESULT: FAIL` / critical <N>件 |
| 復元後 | `RESULT: PASS` / critical 0件 |

## Phase 3 総括

### 最終スコア(2026-08-29、初期実装、60秒走行×3回)

| 回 | スコア | 結果 |
|---|---|---|
| 1 | <点> | PASS |
| 2 | <点> | PASS |
| 3 | <点> | PASS |

内訳(1回目): <breakdown 出力を貼る>

### Phase 4 への持ち越し

- 一覧 `GET /auctions` の非トランザクショナルなレース(検索実装時に一貫化が必要)
- 終了処理バッチの複数台での二重実行(Phase 5 の IaC でレギュレーション化)
- `go.mod` の go ディレクティブ不揃い(webapp 1.26.1 / bench 1.26.4)
- compose の nginx readiness 未設定(起動直後の502ウィンドウ)
```

`<...>` は実際の出力で埋める。

- [ ] **Step 6: README を更新する**

`README.md` の「ステータス」セクションを差し替える:

```markdown
## ステータス

Phase 3(pub/sub要素)まで完了。入札フィード・通知ファンアウト・終了処理と落札確定・
出品者シナリオが動き、ベンチがそれぞれの整合性を検証する。
フロントエンド・検索・アイコンBLOB・初期データジェネレータ・レギュレーション文書は Phase 4。
```

「クイックスタート」のベンチ実行例に worker 数のフラグを追記する:

```markdown
# ベンチ実行(60秒の負荷走行+整合性検証)
cd bench && go run . -target http://localhost:8080

# worker 数を変える(既定: bidders 8 / watchers 4 / notifiers 2 / sellers 2)
go run . -target http://localhost:8080 -bidders 16 -sellers 4
```

- [ ] **Step 7: コミット**

```bash
git add README.md docs/phase2-notes.md docs/phase3-notes.md
git commit -m "docs: Phase 3の完了状態をREADME・ノート・仕込みインベントリに反映"
```

- [ ] **Step 8: ブランチをマージ可能な状態にする**

Run:
```bash
git log --oneline main..phase-3-pubsub
cd webapp/go && go test ./... && cd ../../bench && go test ./...
cd .. && go run ./bench -target http://localhost:8080 -duration 60s
```
Expected: 全テスト PASS、`RESULT: PASS`。この状態で `superpowers:finishing-a-development-branch` に進む
