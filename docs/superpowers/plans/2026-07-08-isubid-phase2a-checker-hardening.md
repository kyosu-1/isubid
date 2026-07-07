# ISUBID Phase 2a(チェッカー強化バッチ)実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Phase 1最終レビューで特定されたベンチマーカーの false-PASS 穴(順序未検証・詳細未検証・statusフィルタ未実証・5xx混同)とアプリの誤ステータス(register一律409)を塞ぎ、ベンチを「スモークテスト」から「レフェリー」に近づける。

**Architecture:** シードに closed/upcoming オークションと ends_at の階段値を追加して「フィルタ・順序が壊れたら初期照合で落ちる」構造にする。ベンチは一覧の順序・シード詳細・入札列の順序を検証する関数を追加し、PostBid が5xxをエラーとして扱うようにする。負荷走行(Load)は本計画の範囲外(Phase 2b)。

**Tech Stack:** 既存と同じ(Go, chi, sqlx, MySQL 8, isucandar)

## Global Constraints

- リポジトリルート: `/Users/abe/ghq/github.com/kyosu-1/isubid`。作業ブランチ: `phase-2a`
- **意図的なボトルネック(`// 意図的に遅い実装` コメント付き)は変更禁止**(bcrypt 12 / N+1 / MAX全件走査 / FOR UPDATE / インデックスなし / nginx設定)
- APIのエラーレスポンスは常に `{"error": "<message>"}`(入札too-lowのみ `current_price` が追加)
- アプリのテストは compose の mysql 起動が前提: `docker compose -f dev/compose.yaml up -d mysql`
- シードの正解値はこの計画のTask 1で更新される。**ベンチ(bench/validate.go)の期待値は `webapp/sql/90_seed_phase1.sql` が正**
- live オークション(id 1〜10)の title / current_price / bid_count / seller は Phase 1 から変更しない
- コミットメッセージ末尾: `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: シード拡張(ends_at階段化 + closed/upcoming追加)とアプリテスト強化

**Files:**
- Modify: `webapp/sql/90_seed_phase1.sql`(auctionsブロック差し替え+追記)
- Modify: `webapp/go/auctions_test.go`(順序assertion、closed詳細テスト、invalid-idテスト追加)

**Interfaces:**
- Produces(後続タスクとベンチが依存する新シード仕様):
  - live auction id N (1〜10) の `ends_at` = `2030-01-01 0N:00:00`(id昇順=ends_at昇順)。title/price/入札はPhase 1と同一
  - auction 11: closed。seller 11, category 3, title `初代ISUCONチェア`, starting_price 10000, starts_at `2025-12-01 00:00:00`, ends_at `2026-01-15 00:00:00`, winner_id 12, winning_price 12000。入札2件: (id 7, user 13, 10500, `2026-01-10 00:00:00`), (id 8, user 12, 12000, `2026-01-14 00:00:00`)
  - auction 12: upcoming。seller 12, category 1, title `ISUリラックス Pro`, starting_price 8000, starts_at `2030-06-01 00:00:00`, ends_at `2030-06-02 00:00:00`, 入札なし
  - `GET /auctions` は従来どおり live の10件のみ返す(11,12が返らないことがフィルタの実証)

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/auctions_test.go` に以下の3テストを追加し、`TestGetAuctions` の `len(list) != 10` チェックの直後(`byID` 構築の前)に順序assertionを追加する:

追加テスト:

```go
func TestGetAuctionsOrderedByEndsAt(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var list []auctionSummaryJSON
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	// シードは id昇順 = ends_at昇順 になるよう階段配置されている
	for i, a := range list {
		if a.ID != int64(i+1) {
			t.Fatalf("list[%d].ID = %d, want %d (ends_at ASC order)", i, a.ID, i+1)
		}
		if i > 0 && a.EndsAt.Before(list[i-1].EndsAt) {
			t.Fatalf("list[%d].EndsAt %v < list[%d].EndsAt %v", i, a.EndsAt, i-1, list[i-1].EndsAt)
		}
	}
}

func TestGetAuctionClosedDetail(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions/11")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var d auctionDetailJSON
	if err := json.NewDecoder(res.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Status != "closed" {
		t.Errorf("status = %q, want closed", d.Status)
	}
	if d.CurrentPrice != 12000 || d.BidCount != 2 {
		t.Errorf("current_price = %d / bid_count = %d, want 12000 / 2", d.CurrentPrice, d.BidCount)
	}
}

func TestGetAuctionInvalidID(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions/notanumber")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd webapp/go && go test ./... -run 'TestGetAuctionsOrderedByEndsAt|TestGetAuctionClosedDetail|TestGetAuctionInvalidID' -v`
Expected: `TestGetAuctionClosedDetail` が404でFAIL(auction 11未存在)。他2つはPASSしうる(ends_at同値・実装済み400)が、この時点ではシード未変更なのでOrdered版は全件同時刻でID順依存のためPASSする可能性がある — FAILしなくても進んでよい(シード変更後に意味を持つ)。

- [ ] **Step 3: シードSQLを更新**

`webapp/sql/90_seed_phase1.sql` の auctions INSERT ブロック(`INSERT INTO auctions ... (10, ...) ;`)を以下で**差し替え**る(ends_atを階段化。他の値はPhase 1と同一):

```sql
-- liveオークション10件。ends_at は id 昇順の階段配置(一覧の ends_at ASC 順検証のため)
INSERT INTO auctions (id, seller_id, category_id, title, description, starting_price, starts_at, ends_at, status) VALUES
  (1,  1, 3, 'ヘリテージ・ウィングチェア',       '英国アンティークの本革ウィングチェア', 1000, '2026-01-01 00:00:00', '2030-01-01 01:00:00', 'live'),
  (2,  2, 1, 'エルゴホスト Model E',             '長時間作業向けエルゴノミクスチェア',   2000, '2026-01-01 00:00:00', '2030-01-01 02:00:00', 'live'),
  (3,  3, 2, 'ISUレーサー GT',                   'フルバケット型ゲーミングチェア',       3000, '2026-01-01 00:00:00', '2030-01-01 03:00:00', 'live'),
  (4,  4, 1, 'メッシュフロー 40',                '通気性メッシュのタスクチェア',         4000, '2026-01-01 00:00:00', '2030-01-01 04:00:00', 'live'),
  (5,  5, 3, 'ミッドセンチュリー・ラウンジ',     '1960年代のラウンジチェア',             2500, '2026-01-01 00:00:00', '2030-01-01 05:00:00', 'live'),
  (6,  6, 2, 'ネオンストライク Z',               'RGBライト内蔵ゲーミングチェア',        3000, '2026-01-01 00:00:00', '2030-01-01 06:00:00', 'live'),
  (7,  7, 1, 'スタンドフレックス',               '昇降デスク対応ハイチェア',             3500, '2026-01-01 00:00:00', '2030-01-01 07:00:00', 'live'),
  (8,  8, 3, 'チャーチチェア 1920',              '教会で使われていた木製チェア',         4000, '2026-01-01 00:00:00', '2030-01-01 08:00:00', 'live'),
  (9,  9, 2, 'プロシート・エディション',         'eスポーツチーム監修モデル',            4500, '2026-01-01 00:00:00', '2030-01-01 09:00:00', 'live'),
  (10, 10, 1, 'コンパクトワーク 01',             '省スペース設計のワークチェア',         5000, '2026-01-01 00:00:00', '2030-01-01 10:00:00', 'live');

-- 一覧のstatusフィルタ実証用: closed(落札済み)とupcoming
INSERT INTO auctions (id, seller_id, category_id, title, description, starting_price, starts_at, ends_at, status, winner_id, winning_price) VALUES
  (11, 11, 3, '初代ISUCONチェア', '記念すべき初代モデル(終了済み)', 10000, '2025-12-01 00:00:00', '2026-01-15 00:00:00', 'closed', 12, 12000);

INSERT INTO auctions (id, seller_id, category_id, title, description, starting_price, starts_at, ends_at, status) VALUES
  (12, 12, 1, 'ISUリラックス Pro', '開始前のリクライニングチェア', 8000, '2030-06-01 00:00:00', '2030-06-02 00:00:00', 'upcoming');
```

さらに bids INSERT ブロックの末尾(`(6, 4, 7, 4100, '2026-07-01 05:00:00');` の行)を次のように変更して auction 11 の入札2件を追加する:

```sql
  (6, 4, 7, 4100, '2026-07-01 05:00:00'),
  (7, 11, 13, 10500, '2026-01-10 00:00:00'),
  (8, 11, 12, 12000, '2026-01-14 00:00:00');
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd webapp/go && go test ./... -v`
Expected: 全PASS(既存の `TestGetAuctions` は依然 len=10 で通る = フィルタが11,12を除外していることの実証)。

- [ ] **Step 5: Commit**

```bash
git add webapp/sql/90_seed_phase1.sql webapp/go/auctions_test.go
git commit -m "feat: シードにclosed/upcomingオークションとends_at階段値を追加し順序・フィルタを実証"
```

---

### Task 2: register の 1062 判定と入札 not-live テスト

**Files:**
- Modify: `webapp/go/auth.go:33-40`(INSERT エラーの判別)
- Modify: `webapp/go/bids_test.go`(not-live テスト追加)
- Test: `webapp/go/auth_test.go`(既存の duplicate テストが引き続き409を保証)

**Interfaces:**
- Consumes: Task 1のシード(auction 11=closed, 12=upcoming)
- Produces: `POST /register` は重複名(MySQLエラー1062)のみ409、他のDBエラーは500

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/bids_test.go` に追加:

```go
func TestPostBidNotLive(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := loginSeedUser(t, ts.URL, "seed_user_05")

	// 11=closed, 12=upcoming — どちらも入札不可
	for _, id := range []string{"11", "12"} {
		res := postJSON(t, client, ts.URL+"/auctions/"+id+"/bids", `{"amount":999999}`)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("auction %s: status = %d, want 400", id, res.StatusCode)
		}
	}
}
```

- [ ] **Step 2: テストが失敗しないことを確認し、実行して結果を見る**

Run: `cd webapp/go && go test ./... -run TestPostBidNotLive -v`
Expected: PASS(ハンドラの not-live 分岐は実装済み。Task 1のシードで初めて到達可能になったテスト)。FAILした場合は実装バグなので原因を調べること。

- [ ] **Step 3: register の 1062 判定を実装**

`webapp/go/auth.go` の以下の箇所:

```go
	res, err := h.db.ExecContext(r.Context(),
		"INSERT INTO users (name, password_hash) VALUES (?, ?)", req.Name, string(hash))
	if err != nil {
		writeError(w, http.StatusConflict, "name already taken")
		return
	}
```

を次に差し替える:

```go
	res, err := h.db.ExecContext(r.Context(),
		"INSERT INTO users (name, password_hash) VALUES (?, ?)", req.Name, string(hash))
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			writeError(w, http.StatusConflict, "name already taken")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
```

import ブロックに `"github.com/go-sql-driver/mysql"` を追加する(既存の `database/sql` / `errors` はそのまま)。

- [ ] **Step 4: 全テストが通ることを確認**

Run: `cd webapp/go && go test ./... -v && go vet ./...`
Expected: 全PASS(`TestRegisterDuplicateName` が1062経路の409を保証)。DB障害→500の経路は自動テスト対象外(DBを落とす必要があるため)。

- [ ] **Step 5: Commit**

```bash
git add webapp/go/auth.go webapp/go/bids_test.go
git commit -m "fix: registerの409をMySQL 1062(重複)のみに限定し、他のDBエラーは500に"
```

---

### Task 3: ベンチ検証ロジック強化(順序・シード詳細・未テスト分岐)

**Files:**
- Modify: `bench/validate.go`(順序検証、`ValidateInitialAuctionDetail`、`ValidateBidsOrdered` 追加)
- Modify: `bench/validate_test.go`(新検証のテスト+既存未テスト分岐のテスト)

**Interfaces:**
- Consumes: Task 1のシード仕様(ends_at階段、auction 1詳細: description `英国アンティークの本革ウィングチェア`, starting_price 1000, bids DESC順 [1500/u4, 1200/u3, 1000/u2])
- Produces(Task 4が依存):
  - `ValidateInitialAuctionDetail(d *AuctionDetail) error`
  - `ValidateBidsOrdered(bids []Bid) error`(created_at DESC, id DESC の検証)
  - `ValidateInitialAuctionList` は従来の照合に加え「id昇順(=ends_at昇順)」「ends_at非減少」を検証する

- [ ] **Step 1: 失敗するテストを書く**

`bench/validate_test.go` の `seedList` 内 `mk` 関数を、ends_at階段値を反映する形に差し替える。既存の:

```go
	mk := func(id int64, title string, price, count int64, seller int64) AuctionSummary {
		a := base
		a.ID = id
		a.Title = title
		a.CurrentPrice = price
		a.BidCount = count
		a.Seller = User{ID: seller, Name: "seed_user_" + pad2(seller)}
		return a
	}
```

を次に差し替える:

```go
	mk := func(id int64, title string, price, count int64, seller int64) AuctionSummary {
		a := base
		a.ID = id
		a.Title = title
		a.CurrentPrice = price
		a.BidCount = count
		a.Seller = User{ID: seller, Name: "seed_user_" + pad2(seller)}
		a.EndsAt = time.Date(2030, 1, 1, int(id), 0, 0, 0, time.UTC)
		return a
	}
```

さらに以下のテスト群を追加する:

```go
func TestValidateInitialAuctionListWrongOrder(t *testing.T) {
	list := seedList()
	list[0], list[1] = list[1], list[0]
	err := ValidateInitialAuctionList(list)
	if err == nil {
		t.Error("want order error, got nil")
	}
}

func TestValidateInitialAuctionListNotLive(t *testing.T) {
	list := seedList()
	list[3].Status = "closed"
	err := ValidateInitialAuctionList(list)
	if err == nil || !strings.Contains(err.Error(), "status") {
		t.Errorf("want status error, got %v", err)
	}
}

func TestValidateInitialAuctionListWrongSeller(t *testing.T) {
	list := seedList()
	list[0].Seller.Name = "hacker"
	err := ValidateInitialAuctionList(list)
	if err == nil || !strings.Contains(err.Error(), "seller") {
		t.Errorf("want seller error, got %v", err)
	}
}

func seedDetail() *AuctionDetail {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	return &AuctionDetail{
		AuctionSummary: AuctionSummary{ID: 1, Title: "ヘリテージ・ウィングチェア", CurrentPrice: 1500, BidCount: 3, Status: "live"},
		Description:    "英国アンティークの本革ウィングチェア",
		StartingPrice:  1000,
		Bids: []Bid{
			{ID: 3, User: User{ID: 4, Name: "seed_user_04"}, Amount: 1500, CreatedAt: t0.Add(2 * time.Hour)},
			{ID: 2, User: User{ID: 3, Name: "seed_user_03"}, Amount: 1200, CreatedAt: t0.Add(time.Hour)},
			{ID: 1, User: User{ID: 2, Name: "seed_user_02"}, Amount: 1000, CreatedAt: t0},
		},
	}
}

func TestValidateInitialAuctionDetailOK(t *testing.T) {
	if err := ValidateInitialAuctionDetail(seedDetail()); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestValidateInitialAuctionDetailWrongDescription(t *testing.T) {
	d := seedDetail()
	d.Description = "changed"
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want description error, got nil")
	}
}

func TestValidateInitialAuctionDetailWrongBidOrder(t *testing.T) {
	d := seedDetail()
	d.Bids[0], d.Bids[2] = d.Bids[2], d.Bids[0] // ASC順に崩す
	if err := ValidateInitialAuctionDetail(d); err == nil {
		t.Error("want bid order error, got nil")
	}
}

func TestValidateBidsOrdered(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	ok := []Bid{
		{ID: 5, CreatedAt: t0.Add(time.Hour)},
		{ID: 4, CreatedAt: t0},
		{ID: 2, CreatedAt: t0}, // 同時刻はid降順
	}
	if err := ValidateBidsOrdered(ok); err != nil {
		t.Errorf("want nil, got %v", err)
	}
	ng := []Bid{
		{ID: 4, CreatedAt: t0},
		{ID: 5, CreatedAt: t0}, // 同時刻でid昇順は違反
	}
	if err := ValidateBidsOrdered(ng); err == nil {
		t.Error("want order error, got nil")
	}
}

func TestValidateBidReflectedWrongContent(t *testing.T) {
	d := &AuctionDetail{
		AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1600},
		Bids:           []Bid{{ID: 100, User: User{ID: 5}, Amount: 1600}},
	}
	// 金額不一致
	if err := ValidateBidReflected(d, &BidCreated{ID: 100, UserID: 5, Amount: 1700, AuctionID: 1}); err == nil {
		t.Error("want mismatch error, got nil")
	}
	// current_price が入札額より小さい
	d2 := &AuctionDetail{
		AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1500},
		Bids:           []Bid{{ID: 100, User: User{ID: 5}, Amount: 1600}},
	}
	if err := ValidateBidReflected(d2, &BidCreated{ID: 100, UserID: 5, Amount: 1600, AuctionID: 1}); err == nil {
		t.Error("want current_price error, got nil")
	}
}
```

`bench/validate_test.go` の import に `"time"` が既にあることを確認(なければ追加)。`strings` は既存。

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd bench && go test ./... -v`
Expected: コンパイルエラー(`ValidateInitialAuctionDetail` / `ValidateBidsOrdered` 未定義)。

- [ ] **Step 3: 実装を書く**

`bench/validate.go` を以下の全文に差し替える:

```go
package main

import (
	"fmt"
	"time"
)

// expectedAuction は webapp/sql/90_seed_phase1.sql と一致させること(あちらが正)。
type expectedAuction struct {
	Title        string
	CurrentPrice int64
	BidCount     int64
	SellerID     int64
}

var expectedInitialAuctions = map[int64]expectedAuction{
	1:  {"ヘリテージ・ウィングチェア", 1500, 3, 1},
	2:  {"エルゴホスト Model E", 2100, 1, 2},
	3:  {"ISUレーサー GT", 3100, 1, 3},
	4:  {"メッシュフロー 40", 4100, 1, 4},
	5:  {"ミッドセンチュリー・ラウンジ", 2500, 0, 5},
	6:  {"ネオンストライク Z", 3000, 0, 6},
	7:  {"スタンドフレックス", 3500, 0, 7},
	8:  {"チャーチチェア 1920", 4000, 0, 8},
	9:  {"プロシート・エディション", 4500, 0, 9},
	10: {"コンパクトワーク 01", 5000, 0, 10},
}

type expectedBid struct {
	Amount   int64
	UserID   int64
	UserName string
}

// auction 1 の初期入札列(created_at DESC順)。90_seed_phase1.sql が正。
var expectedAuction1Bids = []expectedBid{
	{1500, 4, "seed_user_04"},
	{1200, 3, "seed_user_03"},
	{1000, 2, "seed_user_02"},
}

func pad2(n int64) string {
	return fmt.Sprintf("%02d", n)
}

func ValidateInitialAuctionList(list []AuctionSummary) error {
	if len(list) != len(expectedInitialAuctions) {
		return fmt.Errorf("GET /auctions: 件数が %d (期待: %d)", len(list), len(expectedInitialAuctions))
	}
	var prevEndsAt time.Time
	for i, a := range list {
		// シードは id昇順 = ends_at昇順 の階段配置
		if a.ID != int64(i+1) {
			return fmt.Errorf("GET /auctions: %d番目が id=%d (期待: id=%d / ends_at ASC順)", i, a.ID, i+1)
		}
		if a.EndsAt.Before(prevEndsAt) {
			return fmt.Errorf("GET /auctions: ends_at が昇順でない (id=%d)", a.ID)
		}
		prevEndsAt = a.EndsAt
		want := expectedInitialAuctions[a.ID]
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
		if a.Seller.ID != want.SellerID || a.Seller.Name != "seed_user_"+pad2(want.SellerID) {
			return fmt.Errorf("auction %d: seller が %+v (期待: id=%d)", a.ID, a.Seller, want.SellerID)
		}
	}
	return nil
}

// ValidateInitialAuctionDetail は初期状態の auction 1 詳細を照合する(入札で汚す前に呼ぶこと)。
func ValidateInitialAuctionDetail(d *AuctionDetail) error {
	if d.ID != 1 {
		return fmt.Errorf("auction detail: id が %d (期待: 1)", d.ID)
	}
	if d.Description != "英国アンティークの本革ウィングチェア" {
		return fmt.Errorf("auction 1: description が %q", d.Description)
	}
	if d.StartingPrice != 1000 {
		return fmt.Errorf("auction 1: starting_price が %d (期待: 1000)", d.StartingPrice)
	}
	if d.CurrentPrice != 1500 {
		return fmt.Errorf("auction 1: current_price が %d (期待: 1500)", d.CurrentPrice)
	}
	if len(d.Bids) != len(expectedAuction1Bids) {
		return fmt.Errorf("auction 1: bids が %d件 (期待: %d件)", len(d.Bids), len(expectedAuction1Bids))
	}
	for i, want := range expectedAuction1Bids {
		b := d.Bids[i]
		if b.Amount != want.Amount || b.User.ID != want.UserID || b.User.Name != want.UserName {
			return fmt.Errorf("auction 1: bids[%d] が amount=%d user=%d/%q (期待: %d/%d/%q)",
				i, b.Amount, b.User.ID, b.User.Name, want.Amount, want.UserID, want.UserName)
		}
	}
	return ValidateBidsOrdered(d.Bids)
}

// ValidateBidsOrdered は入札列が created_at DESC, id DESC で並んでいることを検証する。
func ValidateBidsOrdered(bids []Bid) error {
	for i := 1; i < len(bids); i++ {
		prev, cur := bids[i-1], bids[i]
		if cur.CreatedAt.After(prev.CreatedAt) ||
			(cur.CreatedAt.Equal(prev.CreatedAt) && cur.ID > prev.ID) {
			return fmt.Errorf("bids の順序が created_at DESC, id DESC でない (index %d: id=%d)", i, cur.ID)
		}
	}
	return nil
}

func ValidateBidReflected(d *AuctionDetail, bid *BidCreated) error {
	if d.CurrentPrice < bid.Amount {
		return fmt.Errorf("auction %d: current_price %d が入札額 %d より小さい", d.ID, d.CurrentPrice, bid.Amount)
	}
	for _, b := range d.Bids {
		if b.ID == bid.ID {
			if b.Amount != bid.Amount || b.User.ID != bid.UserID {
				return fmt.Errorf("auction %d: 入札 id=%d の内容が不一致 (got amount=%d user=%d)", d.ID, bid.ID, b.Amount, b.User.ID)
			}
			return nil
		}
	}
	return fmt.Errorf("auction %d: 入札 id=%d が bids に見つからない", d.ID, bid.ID)
}
```

(変更点: 順序検証の追加、`ValidateInitialAuctionDetail`/`ValidateBidsOrdered` の追加、到達不能になった「未知のオークションid」分岐の削除)

- [ ] **Step 4: テストが通ることを確認**

Run: `cd bench && go test ./... -v && go vet ./...`
Expected: 全PASS。

- [ ] **Step 5: Commit**

```bash
git add bench/validate.go bench/validate_test.go
git commit -m "feat: ベンチに一覧順序・シード詳細・入札列順序の検証を追加"
```

---

### Task 4: ベンチクライアントの5xx判別とPrepare強化

**Files:**
- Modify: `bench/client.go`(PostBidの5xx、GetAuctions/GetAuctionのエラーメッセージにbody追加)
- Modify: `bench/scenario.go`(no-opスタブ削除、詳細検証の追加)

**Interfaces:**
- Consumes: `ValidateInitialAuctionDetail` / `ValidateBidsOrdered`(Task 3)
- Produces: `PostBid` は5xxのとき `(nil, code, error)` を返す(4xxは従来どおり `err=nil`)。`Scenario` は `Prepare` のみを持つ

- [ ] **Step 1: client.go を修正**

`bench/client.go` の `PostBid` 内:

```go
	if code != http.StatusCreated {
		return nil, code, nil
	}
```

を次に差し替える:

```go	
	if code >= http.StatusInternalServerError {
		return nil, code, fmt.Errorf("POST %s: status %d (body: %s)", path, code, b)
	}
	if code != http.StatusCreated {
		return nil, code, nil
	}
```

`GetAuctions` の `return nil, fmt.Errorf("GET /auctions: status %d", code)` を
`return nil, fmt.Errorf("GET /auctions: status %d (body: %s)", code, b)` に、
`GetAuction` の `return nil, fmt.Errorf("GET %s: status %d", path, code)` を
`return nil, fmt.Errorf("GET %s: status %d (body: %s)", path, code, b)` に差し替える。

- [ ] **Step 2: scenario.go を修正**

`bench/scenario.go` から no-op の `Load` / `Validation` メソッド(2つとも `return nil` のみ)を削除する(isucandarは実装されたインターフェースだけを実行するため不要。Phase 2bで本実装を追加する)。

`Prepare` 内の「2. 初期データの検証」ブロックの直後(「3. 新規ユーザー登録…」の前)に追加:

```go
	// 2b. シード詳細の検証(入札で汚す前に照合する)
	initialDetail, err := c.GetAuction(ctx, 1)
	if err != nil {
		return err
	}
	if err := ValidateInitialAuctionDetail(initialDetail); err != nil {
		return err
	}
```

`ValidateBidReflected(after, bid)` の直後に追加:

```go
	if err := ValidateBidsOrdered(after.Bids); err != nil {
		return err
	}
```

- [ ] **Step 3: ビルドとテスト**

Run: `cd bench && go build ./... && go test ./... && go vet ./...`
Expected: 全部クリーン。

- [ ] **Step 4: Commit**

```bash
git add bench/client.go bench/scenario.go
git commit -m "feat: PostBidの5xxをエラー扱いにし、Prepareにシード詳細検証を追加"
```

---

### Task 5: 統合確認とドキュメント更新

**Files:**
- Modify: `docs/phase2-notes.md`(完了項目に取り消し線または「済」マーク)

**Interfaces:**
- Consumes: Task 1〜4のすべて

- [ ] **Step 1: フルスタック再ビルドとベンチ実行**

```bash
docker compose -f dev/compose.yaml up -d --build
sleep 5
cd bench && go run . -target http://localhost:8080
```

Expected: `PREPARE: PASS`、exit 0(アプリ再ビルドでauth.go変更を反映。シード変更は /initialize がマウント済みSQLを読むため再ビルド不要だがappイメージ更新のため --build)。

- [ ] **Step 2: 壊れた実装でFAILすることを確認(順序検証のベンチのベンチ)**

一時的に `webapp/go/auctions.go` の一覧クエリの `ORDER BY ends_at ASC, id ASC` を `ORDER BY id DESC` に変えてビルド・起動し、ベンチが順序違反でFAILすることを確認したら、**変更を元に戻して**再ビルドする:

```bash
# 変更後
docker compose -f dev/compose.yaml up -d --build && sleep 5
cd bench && go run . -target http://localhost:8080   # → PREPARE: FAIL(順序エラー)を確認
# 元に戻す(git checkout webapp/go/auctions.go)→ 再ビルド → PASS を再確認
```

Expected: 変更時FAIL、復元後PASS。working treeがクリーンであること(`git status`)。

- [ ] **Step 3: docs/phase2-notes.md を更新**

「チェッカー強化バッチ」セクションの項目1〜7の行頭に `[済]` を付ける(項目4のLoadでの5xx分類は「クライアント側は済、LoadシナリオはPhase 2b」と注記)。

- [ ] **Step 4: 最終確認とCommit**

```bash
cd webapp/go && go test -count=1 ./... && cd ../../bench && go test -count=1 ./...
git add docs/phase2-notes.md
git commit -m "docs: チェッカー強化バッチの完了をphase2-notesに反映"
```

---

## 完了条件(Phase 2a)

- `webapp/go` / `bench` の `go test -count=1 ./...` 全PASS
- 実機スタックで `PREPARE: PASS`
- 一覧の ORDER BY を壊した実装に対してベンチがFAILする(Step 2で実証済み)
- 意図的なボトルネックのコメント・実装が無傷
