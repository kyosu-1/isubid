# Phase 4-B 実装計画: 一覧の一貫性・ページネーション・検索

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `GET /auctions` に読み取り一貫性・ページネーション・検索を入れ、`full` 規模でも一覧が完走しうる土俵を作りつつ、意図的に遅い実装(N+1・LIKEフルスキャン・インデックス欠如)を維持・強化する。

**Architecture:** 参照実装は `getAuctions` を単一トランザクションに包み、その中で `COUNT(*)` → `SELECT ... LIMIT/OFFSET` → 1件ごとの `summarize`(N+1)を走らせる。ベンチは Prepare で全ページを走査して厳密照合し、Load では単一レスポンス内で完結する不変条件だけを見る(走行中はページ境界が動くため)。

**Tech Stack:** Go 1.22 / chi v5 / sqlx / MySQL 8 / isucandar

**Spec:** `docs/superpowers/specs/2026-08-30-isubid-phase4b-list-search-design.md`

## Global Constraints

- 1ページの件数は **20 件固定**。参照実装の定数名は `auctionsPerPage`、ベンチ側の同名定数と手で揃える(モジュールが別なので機械的な照合手段は無い)
- レスポンスは `{"auctions": [...], "total_count": N, "has_next": bool}`。`auctions` は該当0件でも `null` ではなく `[]`
- `page` は 1-origin。非数値 / パース不能 / `< 1` は **400**
- `q` は 255 rune 超で **400**(`title` の上限に揃える)
- `category` は非数値で **400**。存在しない id は **200 + 空結果**
- **意図的に遅い実装のコメントは消さない。** 新しく仕込む箇所には同じ体裁(`// 意図的に遅い実装: ...`)でコメントを付ける
- ベンチの検証は **false-FAIL を出さないことが最優先**。走行中(Load)に複数レスポンスをまたぐ不変条件を検査してはならない
- 検証コード・コメント・ドキュメントは日本語で書く(既存コードベースの慣習)
- 実装言語のコメントに Go 以外の言語の単語を混ぜない

## 前提環境

- MySQL は `docker compose -f dev/compose.yaml up -d mysql` で起動している必要がある(`webapp/go` のテストが接続する)
- アプリは `docker compose -f dev/compose.yaml up -d` で `http://localhost:8080` に立つ
- ベンチの実行: `cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json`
- Prepare のみ: 上記に `-prepare-only` を足す

---

### Task 1: スナップショットに description を追加する

**Files:**
- Modify: `initial-data/snapshot.go:20-35`(`SnapshotAuction`)、`initial-data/snapshot.go:67-83`(`toSnapshot`)
- Modify: `initial-data/snapshot_test.go`
- Modify: `bench/snapshot.go:19-33`(`SnapshotAuction`)
- Regenerate: `initial-data/out/snapshot.json`

**Interfaces:**
- Consumes: なし(最初のタスク)
- Produces: `SnapshotAuction.Description string`(JSON キー `description`)を `initial-data` と `bench` の両方で提供する。Task 5・6 がこれを読む

**理由:** 検索の期待集合を「たまたま description に出ない語をプローブに選んだ」という暗黙の前提抜きに計算するため。あわせて、生成オークションの `description` が現状どこからも検証されていない穴が塞がる。

- [ ] **Step 1: 失敗するテストを書く**

`initial-data/snapshot_test.go` に追記:

```go
func TestSnapshotCarriesDescription(t *testing.T) {
	ds := Generate(Scales["small"])
	snap := BuildSnapshot(ds)

	descByID := map[int64]string{}
	for _, a := range ds.Auctions {
		descByID[a.ID] = a.Description
	}
	for _, sa := range snap.Auctions {
		want := descByID[sa.ID]
		if want == "" {
			t.Fatalf("auction %d: 生成データ側の description が空", sa.ID)
		}
		if sa.Description != want {
			t.Errorf("auction %d: snapshot の description が %q (期待: %q)", sa.ID, sa.Description, want)
		}
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

Run: `cd initial-data && go test -run TestSnapshotCarriesDescription ./...`
Expected: FAIL(`sa.Description` が未定義でコンパイルエラー)

- [ ] **Step 3: `initial-data/snapshot.go` にフィールドを足す**

`SnapshotAuction` の `Title` の直後に追加:

```go
	Title       string `json:"title"`
	Description string `json:"description"`
```

`toSnapshot` の戻り値に追加:

```go
		return SnapshotAuction{
			ID: a.ID, Title: a.Title, Description: a.Description, CategoryID: a.CategoryID,
			SellerID: a.SellerID, SellerName: userName[a.SellerID],
			StartingPrice: a.StartingPrice, CurrentPrice: price,
			BidCount: bidCount[a.ID], Status: a.Status,
			EndsAtOffset: off, WinnerID: a.WinnerID, WinningPrice: a.WinningPrice,
		}
```

- [ ] **Step 4: テストが通ることを確認する**

Run: `cd initial-data && go test ./...`
Expected: PASS

- [ ] **Step 5: ベンチ側の読み取り型にも足す**

`bench/snapshot.go` の `SnapshotAuction` に、`Title` の直後へ:

```go
	Title       string `json:"title"`
	Description string `json:"description"`
```

Run: `cd bench && go build ./... && go test ./...`
Expected: PASS(既存テストは description を使わないので影響なし)

- [ ] **Step 6: 生成物を作り直す**

```bash
cd initial-data
git status --short out/          # 変更が無いことを確認
go run . -scale small -out out
git status --short out/
```

Expected: `out/snapshot.json` **のみ** が変更されている。`out/91_users.sql` 〜 `out/94_notifications.sql` は**変更されていないこと**(生成が決定論的であることの確認になる)。SQL ダンプに差分が出た場合は生成が非決定的になっているので、原因を突き止めるまで先へ進まないこと。

- [ ] **Step 7: 差分が description の追加だけであることを確認する**

```bash
cd /Users/abe/ghq/github.com/kyosu-1/isubid
git diff --stat initial-data/out/
git diff initial-data/out/snapshot.json | grep '^[-+]' | grep -v '"description"' | grep -v '^[-+][-+][-+]' | head -20
```

Expected: 最後のコマンドの出力が**空**(description 行以外の増減が無い)。

- [ ] **Step 8: コミット**

```bash
git add initial-data/snapshot.go initial-data/snapshot_test.go bench/snapshot.go initial-data/out/snapshot.json
git commit -m "feat: スナップショットに description を追加し検索の期待集合を計算可能にする"
```

---

### Task 2: getAuctions をトランザクション化しページネーションを入れる

**Files:**
- Modify: `webapp/go/auctions.go:105-122`(`getAuctions`)
- Modify: `webapp/go/auctions_test.go:45-87`(`TestGetAuctions`)、`:134-160`(`TestGetAuctionsOrderedByEndsAt`)

**Interfaces:**
- Consumes: `h.summarize(ctx, queryer, *auctionRow)`(既存。`*sqlx.Tx` を受け取れる)
- Produces:
  - `const auctionsPerPage = 20`
  - `type auctionListResponse struct { Auctions []auctionSummary; TotalCount int64; HasNext bool }`(JSON: `auctions` / `total_count` / `has_next`)
  - Task 3 が `getAuctions` に `q` / `category` を足す

**注意:** テストでは終了処理バッチが動かない(`main.go:45-46` のコメントの通り、`newRouter` 経由では起動しない)。したがってシードの live 10件はテスト中ずっと安定している。

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/auctions_test.go` に型とテストを追加:

```go
type auctionListJSON struct {
	Auctions   []auctionSummaryJSON `json:"auctions"`
	TotalCount int64                `json:"total_count"`
	HasNext    bool                 `json:"has_next"`
}

// getAuctionList は一覧を取得して 200 とデコード結果を返すヘルパー。
func getAuctionList(t *testing.T, tsURL, query string) auctionListJSON {
	t.Helper()
	var l auctionListJSON
	getJSON(t, tsURL+"/auctions"+query, &l)
	return l
}

func TestGetAuctionsResponseShape(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	l := getAuctionList(t, ts.URL, "")
	if len(l.Auctions) != 10 {
		t.Fatalf("auctions len = %d, want 10", len(l.Auctions))
	}
	if l.TotalCount != 10 {
		t.Errorf("total_count = %d, want 10", l.TotalCount)
	}
	if l.HasNext {
		t.Errorf("has_next = true, want false (10件は1ページに収まる)")
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

Run: `cd webapp/go && go test -run TestGetAuctionsResponseShape ./...`
Expected: FAIL(現状は裸の配列を返すので `auctions len = 0`)

- [ ] **Step 3: `getAuctions` を書き換える**

`webapp/go/auctions.go` の `getAuctions` を丸ごと置き換え、必要な import (`net/url`, `strings`) を足す:

```go
// auctionsPerPage は一覧1ページあたりの件数。
// bench/validate.go の同名定数と手で揃えること(モジュールが別なので
// コンパイル時に照合する手段が無い)。
const auctionsPerPage = 20

type auctionListResponse struct {
	Auctions   []auctionSummary `json:"auctions"`
	TotalCount int64            `json:"total_count"`
	HasNext    bool             `json:"has_next"`
}

// auctionListQuery は GET /auctions のクエリパラメータ。
type auctionListQuery struct {
	Page     int64
	Q        string
	Category sql.NullInt64
}

// parseAuctionListQuery はクエリ文字列を解釈する。エラーを返した場合は 400 にする。
// 値が空文字のパラメータは「未指定」として扱う(?page= と page 省略を同一視する)。
func parseAuctionListQuery(v url.Values) (auctionListQuery, error) {
	q := auctionListQuery{Page: 1}
	if s := v.Get("page"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 1 {
			return q, errors.New("invalid page")
		}
		q.Page = n
	}
	q.Q = v.Get("q")
	if len([]rune(q.Q)) > 255 {
		return q, errors.New("invalid q")
	}
	if s := v.Get("category"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return q, errors.New("invalid category")
		}
		q.Category = sql.NullInt64{Int64: n, Valid: true}
	}
	return q, nil
}

// where は WHERE 句とバインド引数を組み立てる。COUNT と SELECT の両方が同じものを使う。
func (q auctionListQuery) where() (string, []any) {
	cond := "status = 'live'"
	args := []any{}
	if q.Q != "" {
		// 意図的に遅い実装: 先頭ワイルドカードの LIKE は B-tree インデックスが
		// 原理的に使えず、必ず全行スキャンになる。title と description の両方を
		// 対象にすることで1行あたりの比較コストも上げている。
		like := "%" + escapeLike(q.Q) + "%"
		cond += " AND (title LIKE ? OR description LIKE ?)"
		args = append(args, like, like)
	}
	if q.Category.Valid {
		// 意図的に遅い実装: category_id にインデックスが無い。
		cond += " AND category_id = ?"
		args = append(args, q.Category.Int64)
	}
	return cond, args
}

// escapeLike は LIKE パターン中で特別な意味を持つ文字をエスケープする。
// バックスラッシュを最初に置換しないと、後から足したエスケープ文字を
// 二重にエスケープしてしまう。
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (h *handler) getAuctions(w http.ResponseWriter, r *http.Request) {
	q, err := parseAuctionListQuery(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cond, args := q.where()

	// total_count と auctions を単一トランザクション(MySQLデフォルトの REPEATABLE READ)の
	// スナップショットから読む。別々に読むと、COUNT と SELECT のあいだに入札や終了処理が
	// commit された場合に「total_count は 137 なのに全ページ合計は 138 件」が
	// 正しい実装でも起きてしまう。意図的なN+1構成はそのまま維持し、読み取り一貫性のみ確保する。
	tx, err := h.db.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	var total int64
	// 意図的に遅い実装: 検索条件つきの COUNT が SELECT と同じ WHERE をもう一度走る
	// (LIKE 検索時はフルスキャンが2回になる)。
	if err := tx.GetContext(r.Context(), &total,
		"SELECT COUNT(*) FROM auctions WHERE "+cond, args...); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 意図的に遅い実装: status / ends_at にインデックスが無いため全スキャン + filesort。
	// OFFSET が深いほど読み捨てる行が増える。
	pageArgs := append(append([]any{}, args...), auctionsPerPage, (q.Page-1)*auctionsPerPage)
	var rows []auctionRow
	if err := tx.SelectContext(r.Context(), &rows,
		"SELECT "+auctionColumns+" FROM auctions WHERE "+cond+
			" ORDER BY ends_at ASC, id ASC LIMIT ? OFFSET ?", pageArgs...); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	summaries := make([]auctionSummary, 0, len(rows))
	for i := range rows {
		s, err := h.summarize(r.Context(), tx, &rows[i]) // 意図的に遅い実装(N+1)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		summaries = append(summaries, *s)
	}
	writeJSON(w, http.StatusOK, auctionListResponse{
		Auctions:   summaries,
		TotalCount: total,
		HasNext:    q.Page*auctionsPerPage < total,
	})
}
```

- [ ] **Step 4: テストが通ることを確認する**

Run: `cd webapp/go && go test -run TestGetAuctionsResponseShape ./...`
Expected: PASS

- [ ] **Step 5: 既存テストを新レスポンス形へ直す**

`TestGetAuctions`(`auctions_test.go:45`)の本体を差し替える:

```go
func TestGetAuctions(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	l := getAuctionList(t, ts.URL, "")
	if len(l.Auctions) != 10 {
		t.Fatalf("len = %d, want 10", len(l.Auctions))
	}
	byID := map[int64]auctionSummaryJSON{}
	for _, a := range l.Auctions {
		if a.Status != "live" {
			t.Errorf("auction %d status = %q, want live", a.ID, a.Status)
		}
		byID[a.ID] = a
	}
	a1 := byID[1]
	if a1.Title != "ヘリテージ・ウィングチェア" {
		t.Errorf("auction 1 title = %q", a1.Title)
	}
	if a1.CurrentPrice != 1500 {
		t.Errorf("auction 1 current_price = %d, want 1500", a1.CurrentPrice)
	}
	if a1.BidCount != 3 {
		t.Errorf("auction 1 bid_count = %d, want 3", a1.BidCount)
	}
	if a1.Seller.ID != 1 || a1.Seller.Name != "seed_user_01" {
		t.Errorf("auction 1 seller = %+v", a1.Seller)
	}
	if a5 := byID[5]; a5.CurrentPrice != 2500 || a5.BidCount != 0 {
		t.Errorf("auction 5 = %+v, want current_price 2500 / bid_count 0", a5)
	}
}
```

`TestGetAuctionsOrderedByEndsAt`(`auctions_test.go:134`)の本体を差し替える:

```go
func TestGetAuctionsOrderedByEndsAt(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	list := getAuctionList(t, ts.URL, "").Auctions
	// 初期化時にends_atが相対値へ書き換わり、ends_at昇順がid昇順と一致しないことが保証される。
	// これはORDER BY ends_at ASCをORDER BY id ASCに誤って書き換えるバグを検出するため。
	wantOrder := []int64{4, 2, 8, 6, 10, 1, 3, 5, 7, 9}
	for i, want := range wantOrder {
		if list[i].ID != want {
			t.Errorf("list[%d].ID = %d, want %d (ends_at ASC の期待順序)", i, list[i].ID, want)
		}
	}
	for i := 1; i < len(list); i++ {
		if list[i].EndsAt.Before(list[i-1].EndsAt) {
			t.Fatalf("list[%d].EndsAt %v < list[%d].EndsAt %v (ends_at order violation)", i, list[i].EndsAt, i-1, list[i-1].EndsAt)
		}
	}
}
```

Run: `cd webapp/go && go test ./...`
Expected: PASS

- [ ] **Step 6: ページネーションのテストを書く**

`webapp/go/auctions_test.go` に追加:

```go
// createLiveAuctions は duration_seconds を揃えて n 件出品する。
// シードの live は ends_at が +12/20/28/36/44 秒と +3600 秒以降に分かれているため、
// duration=100 で作った分はその中間にまとまって並ぶ。結果として一覧の順序は
// 「シードの短い5件 → 作成した n 件(id昇順) → シードの長い5件」で決定的になる。
func createLiveAuctions(t *testing.T, ts *httptest.Server, n int) []int64 {
	t.Helper()
	c := loginSeedUser(t, ts.URL, "seed_user_03")
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		body := fmt.Sprintf(
			`{"title":"ページング用 %02d","description":"説明","category_id":1,"starting_price":5000,"duration_seconds":100}`, i)
		res, err := c.Post(ts.URL+"/auctions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var created auctionCreatedJSON
		if res.StatusCode != http.StatusCreated {
			res.Body.Close()
			t.Fatalf("POST /auctions status = %d, want 201", res.StatusCode)
		}
		if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
			res.Body.Close()
			t.Fatal(err)
		}
		res.Body.Close()
		ids = append(ids, created.ID)
	}
	return ids
}

func TestGetAuctionsPagination(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	createLiveAuctions(t, ts, 15) // live は 10 + 15 = 25 件

	p1 := getAuctionList(t, ts.URL, "?page=1")
	if len(p1.Auctions) != 20 {
		t.Fatalf("page 1 の件数 = %d, want 20", len(p1.Auctions))
	}
	if p1.TotalCount != 25 {
		t.Errorf("page 1 の total_count = %d, want 25", p1.TotalCount)
	}
	if !p1.HasNext {
		t.Error("page 1 の has_next = false, want true")
	}

	p2 := getAuctionList(t, ts.URL, "?page=2")
	if len(p2.Auctions) != 5 {
		t.Fatalf("page 2 の件数 = %d, want 5", len(p2.Auctions))
	}
	if p2.TotalCount != 25 {
		t.Errorf("page 2 の total_count = %d, want 25", p2.TotalCount)
	}
	if p2.HasNext {
		t.Error("page 2 の has_next = true, want false")
	}

	// page 未指定は page=1 と同じ
	p0 := getAuctionList(t, ts.URL, "")
	if len(p0.Auctions) != 20 || p0.Auctions[0].ID != p1.Auctions[0].ID {
		t.Errorf("page 未指定が page=1 と一致しない")
	}

	// ページを跨いで id が重複せず、ends_at が非減少であること
	seen := map[int64]bool{}
	all := append(append([]auctionSummaryJSON{}, p1.Auctions...), p2.Auctions...)
	for _, a := range all {
		if seen[a.ID] {
			t.Errorf("id %d がページを跨いで重複している", a.ID)
		}
		seen[a.ID] = true
	}
	for i := 1; i < len(all); i++ {
		if all[i].EndsAt.Before(all[i-1].EndsAt) {
			t.Errorf("連結後の ends_at が昇順でない (index %d)", i)
		}
	}
	// 25件すべてが現れる = LIMIT/OFFSET が正しく歩けている
	if len(seen) != 25 {
		t.Errorf("全ページの合計が %d件, want 25", len(seen))
	}
}

func TestGetAuctionsPageOutOfRange(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	l := getAuctionList(t, ts.URL, "?page=99")
	if len(l.Auctions) != 0 {
		t.Errorf("範囲外ページの件数 = %d, want 0", len(l.Auctions))
	}
	if l.TotalCount != 10 {
		t.Errorf("範囲外ページの total_count = %d, want 10", l.TotalCount)
	}
	if l.HasNext {
		t.Error("範囲外ページの has_next = true, want false")
	}
}

func TestGetAuctionsEmptyArrayNotNull(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions?page=99")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"auctions":[]`) {
		t.Errorf("空結果が [] でない: %s", b)
	}
}

func TestGetAuctionsInvalidPage(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	for _, q := range []string{"?page=0", "?page=-1", "?page=abc", "?page=1.5", "?page=99999999999999999999"} {
		res, err := http.Get(ts.URL + "/auctions" + q)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("GET /auctions%s status = %d, want 400", q, res.StatusCode)
		}
	}
}
```

`auctions_test.go` の import に `io` と `net/http/httptest` を足すこと。

- [ ] **Step 7: テストが通ることを確認する**

Run: `cd webapp/go && go test ./...`
Expected: PASS

- [ ] **Step 8: コミット**

```bash
git add webapp/go/auctions.go webapp/go/auctions_test.go
git commit -m "feat: 一覧をトランザクション化しページネーション(20件/ページ)を導入する"
```

---

### Task 3: 検索(?q=)とカテゴリ絞り込み(?category=)

**Files:**
- Modify: `webapp/go/auctions_test.go`(テスト追加のみ。実装は Task 2 の `where()` で完了済み)

**Interfaces:**
- Consumes: Task 2 の `auctionListQuery.where()` と `escapeLike`
- Produces: なし(参照実装側の API はここで確定する)

**注意:** Task 2 の `where()` は既に `q` と `category` を実装している。このタスクは**その振る舞いをテストで固定する**ことが目的であり、実装を後追いで足すのではない。テストが最初から通る場合は、その事実をレポートに書くこと(TDD の建前より、実装と検証の対応が明示されることを優先する)。

シードの live オークションの内訳(`webapp/sql/90_seed_phase1.sql`):

| id | title | description | category_id |
|---|---|---|---|
| 1 | ヘリテージ・ウィングチェア | 英国アンティークの本革ウィングチェア | 3 |
| 2 | エルゴホスト Model E | 長時間作業向けエルゴノミクスチェア | 1 |
| 3 | ISUレーサー GT | フルバケット型ゲーミングチェア | 2 |
| 4 | メッシュフロー 40 | 通気性メッシュのタスクチェア | 1 |
| 5 | ミッドセンチュリー・ラウンジ | 1960年代のラウンジチェア | 3 |
| 6 | ネオンストライク Z | RGBライト内蔵ゲーミングチェア | 2 |
| 7 | スタンドフレックス | 昇降デスク対応ハイチェア | 1 |
| 8 | チャーチチェア 1920 | 教会で使われていた木製チェア | 3 |
| 9 | プロシート・エディション | eスポーツチーム監修モデル | 2 |
| 10 | コンパクトワーク 01 | 省スペース設計のワークチェア | 1 |

live 以外: 11 = closed(`初代ISUCONチェア`)、12 = upcoming(`ISUリラックス Pro`)。

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/auctions_test.go` に追加:

```go
// idsOf は一覧レスポンスから id を昇順で取り出す。
func idsOf(l auctionListJSON) []int64 {
	ids := make([]int64, 0, len(l.Auctions))
	for _, a := range l.Auctions {
		ids = append(ids, a.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func TestGetAuctionsSearch(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	for _, tt := range []struct {
		name  string
		query string
		want  []int64
	}{
		// title にだけ出る語。description 側の条件を落としても通ってしまうので、
		// 下の「description にだけ出る語」とセットで初めて OR の両辺を固定できる。
		{"title にだけ出る語", "?q=ヘリテージ", []int64{1}},
		// description にだけ出る語(3 と 6 の description に「ゲーミング」がある)。
		{"description にだけ出る語", "?q=ゲーミング", []int64{3, 6}},
		// live 以外は除外される(11=closed の 初代ISUCONチェア、12=upcoming の ISUリラックス Pro)。
		{"live 以外を含まない", "?q=ISU", []int64{3}},
		{"どこにも無い語", "?q=ズンドコベロンチョ", []int64{}},
		{"カテゴリ絞り込み", "?category=2", []int64{3, 6, 9}},
		// AND であること。OR だと {1} ∪ {3,6,9} = 4件になる。
		{"q と category は AND", "?q=ヘリテージ&category=2", []int64{}},
		{"存在しないカテゴリは空結果", "?category=999", []int64{}},
		// LIKE のワイルドカードがエスケープされていること。
		// エスケープしていないと '%' が「全件」を意味し 10件返る。
		{"% はリテラルとして扱う", "?q=%25", []int64{}},
		{"_ はリテラルとして扱う", "?q=_", []int64{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			l := getAuctionList(t, ts.URL, tt.query)
			got := idsOf(l)
			if len(got) != len(tt.want) {
				t.Fatalf("ids = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("ids = %v, want %v", got, tt.want)
				}
			}
			if l.TotalCount != int64(len(tt.want)) {
				t.Errorf("total_count = %d, want %d", l.TotalCount, len(tt.want))
			}
			if l.HasNext {
				t.Errorf("has_next = true, want false")
			}
		})
	}
}

func TestGetAuctionsSearchPaginates(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	createLiveAuctions(t, ts, 25) // title は「ページング用 NN」

	p1 := getAuctionList(t, ts.URL, "?q=ページング用&page=1")
	if len(p1.Auctions) != 20 || p1.TotalCount != 25 || !p1.HasNext {
		t.Fatalf("検索の page 1 が %d件 / total_count %d / has_next %v (期待: 20 / 25 / true)",
			len(p1.Auctions), p1.TotalCount, p1.HasNext)
	}
	p2 := getAuctionList(t, ts.URL, "?q=ページング用&page=2")
	if len(p2.Auctions) != 5 || p2.TotalCount != 25 || p2.HasNext {
		t.Fatalf("検索の page 2 が %d件 / total_count %d / has_next %v (期待: 5 / 25 / false)",
			len(p2.Auctions), p2.TotalCount, p2.HasNext)
	}
}

func TestGetAuctionsInvalidSearchParams(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	long := "?q=" + url.QueryEscape(strings.Repeat("あ", 256))
	for _, q := range []string{"?category=abc", "?category=1.5", long} {
		res, err := http.Get(ts.URL + "/auctions" + q)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("GET /auctions%s status = %d, want 400", q, res.StatusCode)
		}
	}

	// ちょうど 255 rune は通る(境界)
	ok := "?q=" + url.QueryEscape(strings.Repeat("あ", 255))
	res, err := http.Get(ts.URL + "/auctions" + ok)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("255 rune の q が status = %d, want 200", res.StatusCode)
	}
}
```

`auctions_test.go` の import に `net/url` と `sort` を足すこと。

- [ ] **Step 2: テストを実行する**

Run: `cd webapp/go && go test -run 'TestGetAuctionsSearch|TestGetAuctionsInvalidSearchParams' ./...`
Expected: PASS(Task 2 で実装済みのため)。**FAIL した場合は Task 2 の `where()` / `parseAuctionListQuery` にバグがあるので、そこを直すこと。** 特に `?q=%25`(= `%`)が 10件返る場合は `escapeLike` が効いていない。

- [ ] **Step 3: 全テストを実行する**

Run: `cd webapp/go && go test ./... && go vet ./... && gofmt -l .`
Expected: PASS / vet クリーン / gofmt 出力なし

- [ ] **Step 4: コミット**

```bash
git add webapp/go/auctions_test.go
git commit -m "test: 検索とカテゴリ絞り込みの振る舞いをテストで固定する"
```

---

### Task 4: ベンチのクライアントを新レスポンス形へ移行する

**Files:**
- Modify: `bench/model.go`(`AuctionList` 追加)
- Modify: `bench/client.go:126-139`(`GetAuctions`)
- Modify: `bench/load.go:98`、`bench/load.go:182`(呼び出し側)
- Modify: `bench/scenario.go:368`(呼び出し側)
- Modify: `bench/client_test.go`(既存テストがあれば追随)

**Interfaces:**
- Consumes: Task 2 の API 契約
- Produces:
  - `type AuctionList struct { Auctions []AuctionSummary; TotalCount int64; HasNext bool }`
  - `type AuctionListParams struct { Page int; Q string; Category int64 }`(ゼロ値 = page 未指定・絞り込み無し)
  - `func (c *Client) GetAuctions(ctx context.Context, p AuctionListParams) (*AuctionList, error)`
  - `func (c *Client) GetAuctionsRaw(ctx context.Context, rawQuery string) (int, error)`(不正値検証用。ステータスコードのみ返す)
  - Task 5〜8 がこれらを使う

- [ ] **Step 1: 失敗するテストを書く**

`bench/client_test.go` に追加:

```go
func TestAuctionListParamsQuery(t *testing.T) {
	for _, tt := range []struct {
		name string
		p    AuctionListParams
		want string
	}{
		{"ゼロ値は空", AuctionListParams{}, ""},
		{"page のみ", AuctionListParams{Page: 2}, "page=2"},
		{"q のみ", AuctionListParams{Q: "エルゴフロー"}, "q=%E3%82%A8%E3%83%AB%E3%82%B4%E3%83%95%E3%83%AD%E3%83%BC"},
		{"category のみ", AuctionListParams{Category: 3}, "category=3"},
		{"全部", AuctionListParams{Page: 2, Q: "椅子", Category: 1}, "category=1&page=2&q=%E6%A4%85%E5%AD%90"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.query(); got != tt.want {
				t.Errorf("query() = %q, want %q", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

Run: `cd bench && go test -run TestAuctionListParamsQuery ./...`
Expected: FAIL(`AuctionListParams` が未定義でコンパイルエラー)

- [ ] **Step 3: モデルとクライアントを実装する**

`bench/model.go` に追加:

```go
// AuctionList は GET /auctions のレスポンス。
type AuctionList struct {
	Auctions   []AuctionSummary `json:"auctions"`
	TotalCount int64            `json:"total_count"`
	HasNext    bool             `json:"has_next"`
}
```

`bench/client.go` の `GetAuctions` を置き換え(import に `net/url` を追加):

```go
// AuctionListParams は GET /auctions のクエリ。ゼロ値は「page 未指定・絞り込み無し」。
type AuctionListParams struct {
	Page     int    // 0 なら page を送らない
	Q        string // 空なら q を送らない
	Category int64  // 0 なら category を送らない
}

// query はクエリ文字列を組み立てる(値はキー名の辞書順に並ぶ)。
func (p AuctionListParams) query() string {
	v := url.Values{}
	if p.Page != 0 {
		v.Set("page", strconv.Itoa(p.Page))
	}
	if p.Q != "" {
		v.Set("q", p.Q)
	}
	if p.Category != 0 {
		v.Set("category", strconv.FormatInt(p.Category, 10))
	}
	return v.Encode()
}

func (c *Client) GetAuctions(ctx context.Context, p AuctionListParams) (*AuctionList, error) {
	path := "/auctions"
	if q := p.query(); q != "" {
		path += "?" + q
	}
	code, b, err := c.doJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d (body: %s)", path, code, b)
	}
	var l AuctionList
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("GET %s: 不正なJSON: %w", path, err)
	}
	if l.Auctions == nil {
		return nil, fmt.Errorf("GET %s: auctions が null (期待: 空配列でも [])", path)
	}
	return &l, nil
}

// GetAuctionsRaw は生のクエリ文字列を送り、ステータスコードだけを返す。
// 不正値が 400 になることの検証に使う(ボディの形は問わない)。
func (c *Client) GetAuctionsRaw(ctx context.Context, rawQuery string) (int, error) {
	code, _, err := c.doJSON(ctx, http.MethodGet, "/auctions?"+rawQuery, nil)
	return code, err
}
```

`bench/client.go` の import に `strconv` が無ければ足すこと。

- [ ] **Step 4: 呼び出し側を直す**

`bench/load.go:98`(bidderIteration):

```go
	l, err := c.GetAuctions(ctx, AuctionListParams{})
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	list := l.Auctions
```

`bench/load.go:182`(watcherIteration)も同じ形に直す。

`bench/scenario.go:368`(Prepare):

```go
	l, err := c.GetAuctions(ctx, AuctionListParams{})
	if err != nil {
		return err
	}
	list := l.Auctions
```

- [ ] **Step 5: テストとビルドが通ることを確認する**

Run: `cd bench && go build ./... && go test ./... && go vet ./...`
Expected: PASS

- [ ] **Step 6: コミット**

```bash
git add bench/model.go bench/client.go bench/client_test.go bench/load.go bench/scenario.go
git commit -m "feat: ベンチのGetAuctionsをページング対応のレスポンス形へ移行する"
```

---

### Task 5: Prepare の全ページ走査と単一レスポンス不変条件

**Files:**
- Modify: `bench/validate.go:13-33`(`expectedAuction` に `Description`)、`:116-159`(`ValidateAuctionListWithSnapshot`)
- Modify: `bench/validate_test.go`
- Modify: `bench/scenario.go:367-394`(Prepare の一覧検証)

**Interfaces:**
- Consumes: Task 4 の `AuctionList` / `AuctionListParams` / `GetAuctions`
- Produces:
  - `const auctionsPerPage = 20`(bench 側)
  - `func ValidatePagedListShape(page int, l *AuctionList) error`(Task 8 が Load で使う)
  - `func fetchAllAuctionPages(ctx context.Context, c *Client, p AuctionListParams) ([]AuctionSummary, int64, error)`(Task 6 が使う)
  - `expectedAuction.Description string`(Task 6 が使う)
  - `ValidateAuctionListWithSnapshot(all []AuctionSummary, totalCount int64, snap *Snapshot, base time.Time) error`(引数が2つ増える)

- [ ] **Step 1: 失敗するテストを書く**

`bench/validate_test.go` に追加:

```go
func summaryAt(id int64, endsAt time.Time) AuctionSummary {
	return AuctionSummary{ID: id, Status: "live", EndsAt: endsAt}
}

func TestValidatePagedListShape(t *testing.T) {
	base := time.Now().UTC()
	full := make([]AuctionSummary, 0, auctionsPerPage)
	for i := 0; i < auctionsPerPage; i++ {
		full = append(full, summaryAt(int64(i+1), base.Add(time.Duration(i)*time.Second)))
	}

	for _, tt := range []struct {
		name    string
		page    int
		list    AuctionList
		wantErr bool
	}{
		{"正常な1ページ目", 1, AuctionList{Auctions: full, TotalCount: 25, HasNext: true}, false},
		{"正常な最終ページ", 2, AuctionList{Auctions: full[:5], TotalCount: 25, HasNext: false}, false},
		{"auctions が null", 1, AuctionList{Auctions: nil, TotalCount: 0, HasNext: false}, true},
		{"件数が上限超過", 1, AuctionList{Auctions: append(append([]AuctionSummary{}, full...), summaryAt(99, base)), TotalCount: 25, HasNext: true}, true},
		{"total_count が件数未満", 1, AuctionList{Auctions: full, TotalCount: 3, HasNext: false}, true},
		{"has_next が不整合", 1, AuctionList{Auctions: full, TotalCount: 25, HasNext: false}, true},
		{"ends_at が降順", 1, AuctionList{Auctions: []AuctionSummary{
			summaryAt(1, base.Add(time.Minute)), summaryAt(2, base),
		}, TotalCount: 2, HasNext: false}, true},
		{"live 以外が混入", 1, AuctionList{Auctions: []AuctionSummary{
			{ID: 1, Status: "closed", EndsAt: base},
		}, TotalCount: 1, HasNext: false}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePagedListShape(tt.page, &tt.list)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

Run: `cd bench && go test -run TestValidatePagedListShape ./...`
Expected: FAIL(`ValidatePagedListShape` と `auctionsPerPage` が未定義)

- [ ] **Step 3: `ValidatePagedListShape` を実装する**

`bench/validate.go` に追加:

```go
// auctionsPerPage は参照実装の1ページ件数。
// webapp/go/auctions.go の同名定数と手で揃えること(モジュールが別なので
// コンパイル時に照合する手段が無い)。
const auctionsPerPage = 20

// ValidatePagedListShape は一覧レスポンス1件だけで完結する不変条件を検証する。
//
// Load 中は終了処理バッチが live を減らし、出品ワーカーが増やすため、オフセット
// ページネーションのページ境界は足元で動く。したがって複数レスポンスにまたがる
// 検査(ページ間で id が重複しない・全ページの和が total_count と一致する 等)は
// ここでは決して行わない。それらは静穏期である Prepare の仕事。
func ValidatePagedListShape(page int, l *AuctionList) error {
	if l.Auctions == nil {
		return fmt.Errorf("GET /auctions?page=%d: auctions が null (期待: 空でも [])", page)
	}
	if len(l.Auctions) > auctionsPerPage {
		return fmt.Errorf("GET /auctions?page=%d: %d件 (期待: %d件以下)",
			page, len(l.Auctions), auctionsPerPage)
	}
	if l.TotalCount < int64(len(l.Auctions)) {
		return fmt.Errorf("GET /auctions?page=%d: total_count %d が返却件数 %d を下回る",
			page, l.TotalCount, len(l.Auctions))
	}
	if want := int64(page)*auctionsPerPage < l.TotalCount; l.HasNext != want {
		return fmt.Errorf("GET /auctions?page=%d: has_next が %v (期待: %v, total_count=%d)",
			page, l.HasNext, want, l.TotalCount)
	}
	for i := 1; i < len(l.Auctions); i++ {
		if l.Auctions[i].EndsAt.Before(l.Auctions[i-1].EndsAt) {
			return fmt.Errorf("GET /auctions?page=%d: ends_at が昇順でない (index %d: id=%d %v の前が id=%d %v)",
				page, i, l.Auctions[i].ID, l.Auctions[i].EndsAt,
				l.Auctions[i-1].ID, l.Auctions[i-1].EndsAt)
		}
	}
	for _, a := range l.Auctions {
		if a.Status != "live" {
			return fmt.Errorf("GET /auctions?page=%d: live以外が混入 (id=%d status=%q)",
				page, a.ID, a.Status)
		}
	}
	return nil
}
```

- [ ] **Step 4: テストが通ることを確認する**

Run: `cd bench && go test -run TestValidatePagedListShape ./...`
Expected: PASS

- [ ] **Step 5: `expectedAuction` に Description を足す**

`bench/validate.go:13-33` を差し替える(フィールド順に注意。既存のリテラルは位置指定なので**全行を書き換える**):

```go
type expectedAuction struct {
	Title        string
	Description  string
	CurrentPrice int64
	BidCount     int64
	SellerID     int64
	CategoryID   int64
	EndsAtOffset int // ends_at = initialize時刻 + このオフセット(秒)
}

var expectedInitialAuctions = map[int64]expectedAuction{
	1:  {"ヘリテージ・ウィングチェア", "英国アンティークの本革ウィングチェア", 1500, 3, 1, 3, 3600},
	2:  {"エルゴホスト Model E", "長時間作業向けエルゴノミクスチェア", 2100, 1, 2, 1, 20},
	3:  {"ISUレーサー GT", "フルバケット型ゲーミングチェア", 3100, 1, 3, 2, 3660},
	4:  {"メッシュフロー 40", "通気性メッシュのタスクチェア", 4100, 1, 4, 1, 12},
	5:  {"ミッドセンチュリー・ラウンジ", "1960年代のラウンジチェア", 2500, 0, 5, 3, 3720},
	6:  {"ネオンストライク Z", "RGBライト内蔵ゲーミングチェア", 3000, 0, 6, 2, 36},
	7:  {"スタンドフレックス", "昇降デスク対応ハイチェア", 3500, 0, 7, 1, 3780},
	8:  {"チャーチチェア 1920", "教会で使われていた木製チェア", 4000, 0, 8, 3, 28},
	9:  {"プロシート・エディション", "eスポーツチーム監修モデル", 4500, 0, 9, 2, 3840},
	10: {"コンパクトワーク 01", "省スペース設計のワークチェア", 5000, 0, 10, 1, 44},
}
```

値は `webapp/sql/90_seed_phase1.sql:31-40` と1文字も違わないこと。

- [ ] **Step 6: `ValidateAuctionListWithSnapshot` を全ページ走査対応にする**

`bench/validate.go` の `ValidateAuctionListWithSnapshot` のシグネチャと冒頭を差し替える。`ends_at` 非減少ループと各行照合ループはそのまま残す:

```go
func ValidateAuctionListWithSnapshot(all []AuctionSummary, totalCount int64, snap *Snapshot, base time.Time) error {
	want := int64(len(expectedInitialAuctions)) + snap.Counts.LiveAuctions
	if int64(len(all)) != want {
		return fmt.Errorf("GET /auctions: 全ページ合計が %d件 (期待: %d = シード %d + 生成 %d)。"+
			"Prepare が初期データの期限切れ窓(最短でシード auction 4 の +12秒)に入っている可能性もある",
			len(all), want, len(expectedInitialAuctions), snap.Counts.LiveAuctions)
	}
	if totalCount != int64(len(all)) {
		return fmt.Errorf("GET /auctions: total_count が %d、全ページ合計が %d件で不一致",
			totalCount, len(all))
	}
	seen := make(map[int64]bool, len(all))
	for _, a := range all {
		if seen[a.ID] {
			return fmt.Errorf("GET /auctions: id=%d がページを跨いで重複している", a.ID)
		}
		seen[a.ID] = true
	}

	// (以降は既存のまま: ends_at 非減少ループ、各行の内容照合ループ)
```

以降のループ内の変数 `list` を `all` に置換すること。

- [ ] **Step 7: `fetchAllAuctionPages` を実装する**

`bench/scenario.go` に追加:

```go
// maxAuctionPages は全ページ走査の安全上限。has_next が常に true を返す実装に
// 当たってもベンチが止まらないようにする。small の live 60件で3ページ、
// full の 210件でも11ページなので十分な余裕がある。
const maxAuctionPages = 100

// fetchAllAuctionPages は has_next が false になるまで全ページを辿り、
// 連結した列と最後に観測した total_count を返す。
//
// total_count はページ間で減ることを許容する(Prepare 中に終了処理バッチが
// live を closed にしうるため)。増えることは許容しない。
func fetchAllAuctionPages(ctx context.Context, c *Client, p AuctionListParams) ([]AuctionSummary, int64, error) {
	var all []AuctionSummary
	var totalCount int64
	for page := 1; ; page++ {
		if page > maxAuctionPages {
			return nil, 0, fmt.Errorf("GET /auctions: has_next が %dページ辿っても false にならない", maxAuctionPages)
		}
		p.Page = page
		l, err := c.GetAuctions(ctx, p)
		if err != nil {
			return nil, 0, err
		}
		if err := ValidatePagedListShape(page, l); err != nil {
			return nil, 0, err
		}
		if page > 1 && l.TotalCount > totalCount {
			return nil, 0, fmt.Errorf("GET /auctions: total_count がページを進めて増えた (page %d: %d → page %d: %d)",
				page-1, totalCount, page, l.TotalCount)
		}
		totalCount = l.TotalCount
		if l.HasNext && len(l.Auctions) != auctionsPerPage {
			return nil, 0, fmt.Errorf("GET /auctions?page=%d: has_next が true なのに %d件 (期待: %d件)",
				page, len(l.Auctions), auctionsPerPage)
		}
		all = append(all, l.Auctions...)
		if !l.HasNext {
			return all, totalCount, nil
		}
	}
}
```

- [ ] **Step 8: Prepare を全ページ走査に切り替える**

`bench/scenario.go:367-394` の `// 2. 初期データの検証` ブロックを差し替える:

```go
	// 2. 初期データの検証
	if s.Snapshot != nil {
		all, totalCount, err := fetchAllAuctionPages(ctx, c, AuctionListParams{})
		if err != nil {
			return err
		}
		if err := ValidateAuctionListWithSnapshot(all, totalCount, s.Snapshot, base); err != nil {
			return err
		}
		// 範囲外ページ: 200 / 空配列 / has_next=false
		lastPage := int((totalCount + auctionsPerPage - 1) / auctionsPerPage)
		beyond, err := c.GetAuctions(ctx, AuctionListParams{Page: lastPage + 1})
		if err != nil {
			return err
		}
		if len(beyond.Auctions) != 0 || beyond.HasNext {
			return fmt.Errorf("GET /auctions?page=%d (範囲外): %d件 / has_next=%v (期待: 0件 / false)",
				lastPage+1, len(beyond.Auctions), beyond.HasNext)
		}
		// 代表サンプルの詳細を照合する(全件は Prepare の時間予算に収まらない)
		for _, id := range s.Snapshot.SampleAuctionIDs {
			sa, ok := s.Snapshot.ByID(id)
			if !ok {
				return fmt.Errorf("スナップショットの sample_auction_ids に載っている %d が auctions に無い", id)
			}
			d, err := c.GetAuction(ctx, id)
			if err != nil {
				return err
			}
			if err := ValidateSnapshotAuctionDetail(d, sa, base); err != nil {
				return err
			}
		}
	} else {
		l, err := c.GetAuctions(ctx, AuctionListParams{})
		if err != nil {
			return err
		}
		if err := ValidateInitialAuctionList(l.Auctions, base); err != nil {
			return err
		}
	}
```

- [ ] **Step 9: 既存のベンチテストを直す**

`bench/validate_test.go` で `ValidateAuctionListWithSnapshot` を呼んでいる箇所に、第2引数として `int64(len(list))` を渡す(既存テストは「全ページ合計 == total_count」が成立する前提で書かれている)。`total_count` 不一致を検出するテストも1本足す:

```go
func TestValidateAuctionListWithSnapshotRejectsTotalCountMismatch(t *testing.T) {
	// 既存テストの正常系と同じ list / snap / base を組み立てたうえで、
	// total_count だけをずらす。
	list, snap, base := validListFixture(t) // 既存テストのフィクスチャ組み立てを関数に切り出して使う
	if err := ValidateAuctionListWithSnapshot(list, int64(len(list)), snap, base); err != nil {
		t.Fatalf("正常系が失敗した: %v", err)
	}
	if err := ValidateAuctionListWithSnapshot(list, int64(len(list))+1, snap, base); err == nil {
		t.Error("total_count が全ページ合計と食い違っているのにエラーにならない")
	}
}
```

既存テストにフィクスチャ組み立ての共通部分が無い場合は `validListFixture` を切り出すこと。

- [ ] **Step 10: テストとビルドが通ることを確認する**

Run: `cd bench && go build ./... && go test ./... && go vet ./... && gofmt -l .`
Expected: PASS / vet クリーン / gofmt 出力なし

- [ ] **Step 11: コミット**

```bash
git add bench/validate.go bench/validate_test.go bench/scenario.go
git commit -m "feat: Prepareを全ページ走査にし単一レスポンス不変条件を切り出す"
```

---

### Task 6: 検索・カテゴリ・不正値の Prepare 検証

**Files:**
- Modify: `bench/validate.go`(プローブ定数、`expectedLiveMatches`、`ValidateSearchResult`)
- Modify: `bench/validate_test.go`
- Modify: `bench/scenario.go`(Prepare への組み込み)

**Interfaces:**
- Consumes: Task 1 の `SnapshotAuction.Description`、Task 5 の `fetchAllAuctionPages` / `expectedAuction.Description`
- Produces:
  - `const probeTitleOnly = "ワークス"` / `probeDescriptionOnly = "手作業"` / `probeNoMatch = "ズンドコベロンチョ"`
  - `func expectedLiveMatches(probe string, categoryID int64, snap *Snapshot, base time.Time) map[int64]time.Time`
  - `func ValidateSearchResult(label string, got []AuctionSummary, totalCount int64, want map[int64]time.Time, now time.Time) error`
  - Task 8 が `probeTitleOnly` を Load で使う

**プローブの根拠:**

- `ワークス` は `initial-data/generate.go:75-79` の `chairNames` の要素 `メッシュワークス` の index 3(語中)。どの `chairDescs` にもシード description にも現れない → **title 専用**
- `手作業` は `chairDescs` の `職人による手作業の仕上げ` の index 5(語中)にのみ現れる。どの `chairNames` にもシード title にも現れない → **description 専用**
- **語中であることが要件**: 先頭に出る語(旧案の `エルゴフロー` / `職人`)だと `LIKE 'q%'` への前方一致化を検出できない
- `ズンドコベロンチョ` はどこにも現れない

- [ ] **Step 1: プローブの分類を固定するテストを書く**

`bench/validate_test.go` に追加:

```go
// TestSearchProbesAreClassified はプローブ語が「title 専用 / description 専用 /
// どこにも無い」に厳密に属することを固定する。この分離が崩れると、
// title LIKE と description LIKE の片側を落とした改悪を検出できなくなる。
func TestSearchProbesAreClassified(t *testing.T) {
	snap, err := LoadSnapshot("../initial-data/out/snapshot.json")
	if err != nil {
		t.Fatalf("スナップショットの読み込みに失敗: %v", err)
	}

	titles := []string{}
	descs := []string{}
	for _, e := range expectedInitialAuctions {
		titles = append(titles, e.Title)
		descs = append(descs, e.Description)
	}
	for _, sa := range snap.Auctions {
		titles = append(titles, sa.Title)
		descs = append(descs, sa.Description)
	}
	anyContains := func(ss []string, probe string) bool {
		for _, s := range ss {
			if strings.Contains(s, probe) {
				return true
			}
		}
		return false
	}

	if !anyContains(titles, probeTitleOnly) {
		t.Errorf("%q がどの title にも現れない", probeTitleOnly)
	}
	if anyContains(descs, probeTitleOnly) {
		t.Errorf("%q が description に現れる (title 専用のはず)", probeTitleOnly)
	}
	if !anyContains(descs, probeDescriptionOnly) {
		t.Errorf("%q がどの description にも現れない", probeDescriptionOnly)
	}
	if anyContains(titles, probeDescriptionOnly) {
		t.Errorf("%q が title に現れる (description 専用のはず)", probeDescriptionOnly)
	}
	if anyContains(titles, probeNoMatch) || anyContains(descs, probeNoMatch) {
		t.Errorf("%q がどこかに現れる (該当なしのはず)", probeNoMatch)
	}

	// LIKE のワイルドカードを含むとサーバー側のエスケープ有無で結果が変わり、
	// Go の strings.Contains と食い違う。
	for _, p := range []string{probeTitleOnly, probeDescriptionOnly, probeNoMatch} {
		if strings.ContainsAny(p, `%_\`) {
			t.Errorf("プローブ %q が LIKE のワイルドカード文字を含む", p)
		}
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

Run: `cd bench && go test -run TestSearchProbesAreClassified ./...`
Expected: FAIL(プローブ定数が未定義)

- [ ] **Step 3: プローブ定数と期待集合の計算を実装する**

`bench/validate.go` に追加:

```go
// 検索検証に使うプローブ語。TestSearchProbesAreClassified が分類を固定する。
//
// Prepare は3つ全てを使う(スナップショットから description を知っているので
// 期待集合を計算できる)。Load は probeTitleOnly だけを使う ——
// 一覧レスポンスの AuctionSummary に description が無いため、description で
// 一致した行を Load 側は検証しようがなく、正しい実装を誤判定してしまう。
const (
	probeTitleOnly       = "ワークス"
	probeDescriptionOnly = "手作業"
	probeNoMatch         = "ズンドコベロンチョ"
)

// expectedLiveMatches は初期データのうち probe と categoryID に合致する live
// オークションの期待集合を返す。値はそのオークションの ends_at で、照合側が
// 期限切れを許容できるようにするために持たせる。
// probe が空なら語での絞り込み無し、categoryID が 0 ならカテゴリ絞り込み無し。
func expectedLiveMatches(probe string, categoryID int64, snap *Snapshot, base time.Time) map[int64]time.Time {
	want := map[int64]time.Time{}
	add := func(id int64, title, description string, cat int64, offset int) {
		if probe != "" && !strings.Contains(title, probe) && !strings.Contains(description, probe) {
			return
		}
		if categoryID != 0 && cat != categoryID {
			return
		}
		want[id] = base.Add(time.Duration(offset) * time.Second)
	}
	for id, e := range expectedInitialAuctions {
		add(id, e.Title, e.Description, e.CategoryID, e.EndsAtOffset)
	}
	if snap != nil {
		for i := range snap.Auctions {
			sa := &snap.Auctions[i]
			if sa.Status != "live" {
				continue
			}
			add(sa.ID, sa.Title, sa.Description, sa.CategoryID, sa.EndsAtOffset)
		}
	}
	return want
}

// ValidateSearchResult は検索・絞り込み結果を期待集合と照合する。
//
// 非対称なルールを使う。
//   - 期待集合の要素は、その ends_at が既に到来していれば欠けていてよい
//     (Prepare の途中で終了処理バッチが closed にしうる)
//   - 期待集合に無い id が返ってきたら、常に異常
//
// now は全ページを取り終えた後の時刻を渡すこと。取得前の時刻を渡すと
// 「取得中に期限が来た」ケースを許容できず false-FAIL になる。
func ValidateSearchResult(label string, got []AuctionSummary, totalCount int64,
	want map[int64]time.Time, now time.Time) error {

	gotIDs := make(map[int64]bool, len(got))
	for _, a := range got {
		if gotIDs[a.ID] {
			return fmt.Errorf("%s: id=%d が重複している", label, a.ID)
		}
		gotIDs[a.ID] = true
		if _, ok := want[a.ID]; !ok {
			return fmt.Errorf("%s: 期待集合に無い auction %d (title=%q) が返った", label, a.ID, a.Title)
		}
		if a.Status != "live" {
			return fmt.Errorf("%s: auction %d の status が %q (期待: live)", label, a.ID, a.Status)
		}
	}

	var stillLive int64
	for id, endsAt := range want {
		if !endsAt.After(now) {
			continue // 期限到来済み。欠けていてよい
		}
		stillLive++
		if !gotIDs[id] {
			return fmt.Errorf("%s: 期限前(%v)の auction %d が結果に含まれていない", label, endsAt, id)
		}
	}

	if totalCount > int64(len(want)) {
		return fmt.Errorf("%s: total_count が %d (期待: %d以下)", label, totalCount, len(want))
	}
	if totalCount < stillLive {
		return fmt.Errorf("%s: total_count が %d (期待: %d以上、期限未到来の期待件数)", label, totalCount, stillLive)
	}
	if int64(len(got)) != totalCount {
		return fmt.Errorf("%s: 全ページ合計 %d件 と total_count %d が不一致", label, len(got), totalCount)
	}
	return nil
}
```

`bench/validate.go` の import に `strings` を足すこと。

- [ ] **Step 4: `ValidateSearchResult` の単体テストを書く**

`bench/validate_test.go` に追加:

```go
func TestValidateSearchResult(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(time.Hour)
	past := now.Add(-time.Minute)

	want := map[int64]time.Time{1: future, 2: future, 3: past}
	live := []AuctionSummary{
		{ID: 1, Status: "live", EndsAt: future},
		{ID: 2, Status: "live", EndsAt: future},
	}

	// 期限到来済みの 3 が欠けていても通る
	if err := ValidateSearchResult("t", live, 2, want, now); err != nil {
		t.Errorf("期限切れの欠落が拒否された: %v", err)
	}
	// 3 が返ってきても(まだ closed にされていない)通る
	withPast := append(append([]AuctionSummary{}, live...), AuctionSummary{ID: 3, Status: "live", EndsAt: past})
	if err := ValidateSearchResult("t", withPast, 3, want, now); err != nil {
		t.Errorf("期限切れが残っているだけで拒否された: %v", err)
	}
	// 期限前の 2 が欠けていたら異常
	if err := ValidateSearchResult("t", live[:1], 1, want, now); err == nil {
		t.Error("期限前の欠落が検出されない")
	}
	// 期待集合に無い id が混ざったら異常
	extra := append(append([]AuctionSummary{}, live...), AuctionSummary{ID: 99, Status: "live", EndsAt: future})
	if err := ValidateSearchResult("t", extra, 3, want, now); err == nil {
		t.Error("期待集合外の id が検出されない")
	}
	// total_count が期待集合より大きいのは異常
	if err := ValidateSearchResult("t", live, 99, want, now); err == nil {
		t.Error("過大な total_count が検出されない")
	}
	// total_count を len(auctions) で返す改悪(期限前の期待件数を下回る)
	if err := ValidateSearchResult("t", live[:1], 1, map[int64]time.Time{
		1: future, 2: future, 3: future,
	}, now); err == nil {
		t.Error("total_count を件数で返す改悪が検出されない")
	}
	// 重複
	dup := []AuctionSummary{live[0], live[0]}
	if err := ValidateSearchResult("t", dup, 2, want, now); err == nil {
		t.Error("id の重複が検出されない")
	}
}
```

- [ ] **Step 5: テストが通ることを確認する**

Run: `cd bench && go test -run 'TestSearchProbes|TestValidateSearchResult' ./...`
Expected: PASS

- [ ] **Step 6: Prepare に検索検証を組み込む**

`bench/scenario.go` の Prepare の `if s.Snapshot != nil { ... }` ブロック内、範囲外ページの検証の直後に追加:

```go
		// 検索とカテゴリ絞り込み
		for _, probe := range []struct {
			label      string
			q          string
			categoryID int64
		}{
			{"GET /auctions?q=" + probeTitleOnly + " (title専用プローブ)", probeTitleOnly, 0},
			{"GET /auctions?q=" + probeDescriptionOnly + " (description専用プローブ)", probeDescriptionOnly, 0},
			{"GET /auctions?q=" + probeNoMatch + " (該当なし)", probeNoMatch, 0},
			{"GET /auctions?category=1", "", 1},
			{"GET /auctions?q=" + probeTitleOnly + "&category=1 (AND結合)", probeTitleOnly, 1},
		} {
			got, totalCount, err := fetchAllAuctionPages(ctx, c,
				AuctionListParams{Q: probe.q, Category: probe.categoryID})
			if err != nil {
				return err
			}
			// now は取得後に採る。取得中に期限が来たオークションを許容するため。
			if err := ValidateSearchResult(probe.label, got, totalCount,
				expectedLiveMatches(probe.q, probe.categoryID, s.Snapshot, base),
				time.Now().UTC()); err != nil {
				return err
			}
		}

		// 不正値は 400
		for _, raw := range []string{"page=0", "page=-1", "page=abc", "category=abc"} {
			code, err := c.GetAuctionsRaw(ctx, raw)
			if err != nil {
				return err
			}
			if code != 400 {
				return fmt.Errorf("GET /auctions?%s: status %d (期待: 400)", raw, code)
			}
		}
```

**各プローブの期待件数(コミット済みスナップショット `small` から算出済み。実装後に実測して一致を確認すること):**

| プローブ | 期待件数 | 内訳 |
|---|---|---|
| `q=ワークス` | 5 | 生成 live のみ(シードに `ワークス` を含む title/description は無い) |
| `q=手作業` | 13 | 生成 live の description のみ |
| `q=ズンドコベロンチョ` | 0 | 該当なし |
| `category=1` | 22 | 生成 live 18 + シード live 4(id 2,4,7,10) |
| `q=ワークス&category=1` | 3 | AND。OR なら 5 + 22 - 3 = 24 件になるので明確に区別できる |

`q=ワークス` の live 5件のカテゴリ内訳は {1: 3, 2: 1, 3: 1} である。AND結合プローブに
`category=1` を選ぶのは、期待集合が 3件と十分に非空で、かつ OR 実装との差(3 対 24)が
大きいため。**期待件数が上表と食い違った場合は、スナップショットが作り直されて
中身が変わったことを意味するので、原因を突き止めるまで先へ進まないこと。**

- [ ] **Step 7: 実機で Prepare を通す**

```bash
docker compose -f dev/compose.yaml up -d
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json -prepare-only
```

Expected: `PREPARE: PASS`

期待集合が空でないことの確認のため、一時的に `ValidateSearchResult` の冒頭に
`fmt.Printf("%s: want=%d got=%d total=%d\n", label, len(want), len(got), totalCount)` を入れて
1回走らせ、各プローブの `want` が 0 でないことを目視してから外すこと(`probeNoMatch` は 0 でよい)。

- [ ] **Step 8: 全テストとビルド**

Run: `cd bench && go build ./... && go test ./... && go vet ./... && gofmt -l .`
Expected: PASS / vet クリーン / gofmt 出力なし

- [ ] **Step 9: コミット**

```bash
git add bench/validate.go bench/validate_test.go bench/scenario.go
git commit -m "feat: Prepareに検索・カテゴリ・不正値の検証を追加する"
```

---

### Task 7: 持ち越し7 の修正(詳細検証の期限切れ許容)

**Files:**
- Modify: `bench/validate.go:202-251`(`ValidateSnapshotAuctionDetail`)
- Modify: `bench/validate_test.go`

**Interfaces:**
- Consumes: Task 5・6 で増えた Prepare のリクエスト数
- Produces: `ValidateSnapshotAuctionDetail(d *AuctionDetail, sa *SnapshotAuction, base time.Time)` の意味論変更(シグネチャは不変)

**背景(`docs/phase4-notes.md` 持ち越し7):** `BuildSnapshot` は詳細検証用に live オークションを10件サンプルするが、live のオフセットはシャッフルされているため任意のオフセットに着地する(コミット済みスナップショットで最短21秒)。しかもサンプルされた live は**最後に**fetch される。Prepare の末尾がそのオフセットを跨ぐと `d.Status == "closed"` と `sa.Status == "live"` が食い違い、**正しいアプリが hard-fail する。** Task 5・6 でリクエスト数が増えたぶん、この窓に近づいている。

- [ ] **Step 1: 失敗するテストを書く**

`bench/validate_test.go` に追加:

```go
func TestValidateSnapshotAuctionDetailAcceptsClosedWhenDue(t *testing.T) {
	base := time.Now().UTC()
	// 期限が 1 秒前に到来している live サンプル
	sa := &SnapshotAuction{
		ID: 42, Title: "テスト椅子", Description: "説明", CategoryID: 1,
		SellerID: 7, SellerName: "gen_user_00007",
		StartingPrice: 1000, CurrentPrice: 1000, BidCount: 0,
		Status: "live", EndsAtOffset: -1,
	}
	d := &AuctionDetail{
		AuctionSummary: AuctionSummary{
			ID: 42, Title: "テスト椅子", CategoryID: 1,
			Seller:       User{ID: 7, Name: "gen_user_00007"},
			CurrentPrice: 1000, BidCount: 0,
			EndsAt: base.Add(-time.Second), Status: "closed", // バッチが閉じた
		},
		Description: "説明", StartingPrice: 1000, Bids: []Bid{},
	}
	if err := ValidateSnapshotAuctionDetail(d, sa, base); err != nil {
		t.Errorf("期限到来済みの closed が拒否された: %v", err)
	}

	// 期限がまだ来ていないのに closed なら異常
	sa.EndsAtOffset = 3600
	d.EndsAt = base.Add(time.Hour)
	if err := ValidateSnapshotAuctionDetail(d, sa, base); err == nil {
		t.Error("期限前の closed が検出されない")
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

Run: `cd bench && go test -run TestValidateSnapshotAuctionDetailAcceptsClosedWhenDue ./...`
Expected: FAIL(`status が "closed" (期待: "live")`)

- [ ] **Step 3: status 照合に期限切れ許容を入れる**

`ValidateSnapshotAuctionDetail` の status 照合(`:209-211`)を差し替える:

```go
	if d.Status != sa.Status {
		// スナップショットが live としているオークションは、Prepare の実行中に
		// 終了処理バッチが closed へ移しうる。live のオフセットはシャッフルされて
		// いるため最短で 15秒(コミット済みスナップショットの実測値)に着地し、
		// しかもサンプルされた live は Prepare の最後に fetch される。
		// 期限が実際に到来しているなら closed を受理する。逆向き
		// (closed のはずが live)や、期限前の closed は従来どおり異常とする。
		dueClosed := sa.Status == "live" && d.Status == "closed" &&
			!base.Add(time.Duration(sa.EndsAtOffset) * time.Second).After(time.Now().UTC())
		if !dueClosed {
			return fmt.Errorf("auction %d: status が %q (期待: %q)", d.ID, d.Status, sa.Status)
		}
	}
```

**注意:** closed へ移ると `winner_id` / `winning_price` / `current_price` が変わりうる。`sa.Status == "live"` のときは `winner_id` 系の照合ブロックに入らない(`sa.Status == "closed"` でガードされている)ため、そこは影響しない。`CurrentPrice` / `BidCount` は入札が入らない限り変わらないので照合を続けてよい。`ends_at` の照合(`sa.Status == "live" || sa.Status == "upcoming"` のブロック)も、closed になっても `ends_at` の値自体は変わらないので通る。

- [ ] **Step 4: テストが通ることを確認する**

Run: `cd bench && go test ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add bench/validate.go bench/validate_test.go
git commit -m "fix: 期限到来済みのサンプルliveがclosedでも詳細検証を通す(4-A持ち越し7)"
```

---

### Task 8: Load シナリオの変更と検索の採点

**Files:**
- Modify: `bench/score.go`(`ScoreGETSearch`)
- Modify: `bench/load.go:86-113`(bidderIteration)、`:175-197`(watcherIteration)
- Modify: `bench/main.go`(スコア内訳の出力に新タグを追加)

**Interfaces:**
- Consumes: Task 4 の `AuctionListParams`、Task 5 の `ValidatePagedListShape`、Task 6 の `probeTitleOnly`
- Produces: `ScoreGETSearch score.ScoreTag`(配点 2)

**設計意図:** bidder が `page=1`(= 終了が最も近い20件)から選ぶことで、入札が終了間際のオークションへ集中する。これは実サイトの挙動であると同時に、4-A の持ち越し1/9(live 件数が増えると入札が分散し `FOR UPDATE` 検出器が発火しなくなる)への直接の対策になる。

- [ ] **Step 1: スコアタグを追加する**

`bench/score.go`:

```go
	ScoreGETList          score.ScoreTag = "GET /auctions"
	ScoreGETSearch        score.ScoreTag = "GET /auctions (検索)"
	ScoreGETDetail        score.ScoreTag = "GET /auctions/:id"
```

```go
var scoreTable = map[score.ScoreTag]int64{
	ScoreGETList:          1,
	ScoreGETSearch:        2, // 最も重い読み取り経路。配点で攻略線へ誘導する
	ScoreGETDetail:        1,
	ScorePOSTBid:          5,
	ScoreGETFeed:          1,
	ScoreGETNotifications: 2,
	ScorePOSTAuction:      5,
}
```

- [ ] **Step 2: スコア内訳の出力に新タグを追加する**

`bench/main.go:98-103` にスコアタグとラベルの並びが直書きされている。そこへ1行足す:

```go
		{ScoreGETList, "GET /auctions"},
		{ScoreGETSearch, "GET /auctions (検索)"},
		{ScoreGETDetail, "GET /auctions/:id"},
```

**この列挙に漏れがあると内訳の合計が `raw` と一致しなくなり、ゲート3の目視確認が壊れる。** 追加後、内訳の合計が `raw` と一致することを実行して確認すること(`bench/main.go:60` の `for tag, mag := range scoreTable` が `raw` を計算しており、こちらは `scoreTable` を回すので自動的に新タグを含む)。

- [ ] **Step 3: bidderIteration を page=1 に変更する**

`bench/load.go:98-107` を差し替える:

```go
	// 入札対象は「終了が最も近い20件」= 1ページ目から選ぶ。実サイトの挙動である
	// と同時に、live 件数が増えても入札が分散しないようにする狙いがある
	// (docs/phase4-notes.md 持ち越し1/9: 分散すると FOR UPDATE 検出器が発火しない)。
	l, err := c.GetAuctions(ctx, AuctionListParams{Page: 1})
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(ScoreGETList)
	if err := ValidatePagedListShape(1, l); err != nil {
		addErr(ctx, step, ErrCritical, err)
		return
	}
	list := l.Auctions
	if len(list) == 0 {
		addErr(ctx, step, ErrCritical, fmt.Errorf("GET /auctions: 開催中オークションが0件"))
		return
	}
```

- [ ] **Step 4: watcherIteration をページ回遊+検索に変更する**

`bench/load.go:176-197` の冒頭を差し替える:

```go
// watcherIteration は「一覧(ページ回遊・検索あり)→ランダム詳細+不変条件チェック」の回遊。
func (s *Scenario) watcherIteration(ctx context.Context, step *isucandar.BenchmarkStep) {
	c, err := NewClient(s.Target)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	page := 1 + rand.Intn(3)
	p := AuctionListParams{Page: page}
	tag := ScoreGETList
	// 1/3 の確率で絞り込みを付ける。q は title 専用プローブに限る ——
	// 一覧レスポンスに description が無いため、description で一致した行を
	// 走行中に検証する術がなく、正しい実装を誤判定してしまう。
	switch rand.Intn(3) {
	case 0:
		p.Q = probeTitleOnly
		tag = ScoreGETSearch
	case 1:
		p.Category = int64(1 + rand.Intn(3))
		tag = ScoreGETSearch
	}
	l, err := c.GetAuctions(ctx, p)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(tag)
	if err := ValidatePagedListShape(page, l); err != nil {
		addErr(ctx, step, ErrCritical, err)
		return
	}
	list := l.Auctions
	// 絞り込み結果は述語に合致していなければならない。これは一方向の検査である:
	// 「期待集合にあるのに返ってこない」のは走行中なら正常(closed になった、
	// 別ページへ移った)だが、「述語に合致しない行が返る」のは常に異常。
	// 「返さなすぎ」は静穏期の Prepare が見る。
	for _, a := range list {
		if p.Q != "" && !strings.Contains(a.Title, p.Q) {
			addErr(ctx, step, ErrCritical,
				fmt.Errorf("GET /auctions?q=%s: title が一致しない行が返った (id=%d title=%q)", p.Q, a.ID, a.Title))
			return
		}
		if p.Category != 0 && a.CategoryID != p.Category {
			addErr(ctx, step, ErrCritical,
				fmt.Errorf("GET /auctions?category=%d: category_id=%d の行が返った (id=%d)", p.Category, a.CategoryID, a.ID))
			return
		}
	}
	if len(list) == 0 {
		return
	}
```

以降(`d, err := c.GetAuction(...)` から先)はそのまま残す。旧コードにあった `status != "live"` のループは `ValidatePagedListShape` が担うので削除する。

`bench/load.go` の import に `strings` を足すこと。

- [ ] **Step 5: ビルドとテスト**

Run: `cd bench && go build ./... && go test ./... && go vet ./... && gofmt -l .`
Expected: PASS / vet クリーン / gofmt 出力なし

- [ ] **Step 6: 実機で60秒走らせる**

```bash
docker compose -f dev/compose.yaml up -d
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json
```

Expected: `RESULT: PASS`、critical 0件、**内訳に `GET /auctions (検索)` が0回でない行として現れる**。

- [ ] **Step 7: コミット**

```bash
git add bench/score.go bench/load.go bench/main.go
git commit -m "feat: Loadをページ回遊・検索対応にしScoreGETSearchを追加する"
```

---

### Task 9: ゲート測定と持ち越しの記録

**Files:**
- Modify: `docs/phase4-notes.md`(4-B のセクションを追加)

**Interfaces:**
- Consumes: Task 1〜8 の全て
- Produces: なし(このフェーズの最終タスク)

**注意:** 測定値は**必ずツールの実出力から転記する**こと。数字を辻褄合わせで再構成してはならない。出力を確実に復元できない場合は「復元できない」と正直に書く。

- [ ] **Step 1: ゲート1(初期化)**

```bash
docker compose -f dev/compose.yaml down -v && docker compose -f dev/compose.yaml up -d
sleep 20
for i in 1 2 3; do
  echo "=== run $i ==="
  curl -s -o /dev/null -w 'http_code=%{http_code}\n' -X POST http://localhost:8080/initialize -d '{}'
done
```

Expected: 3回とも `http_code=200`

- [ ] **Step 2: ゲート2(Prepare 3回連続、6秒基準)**

```bash
cd bench
for i in 1 2 3; do
  echo "=== run $i ==="
  ( time go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json -prepare-only ) 2>&1 | grep -E 'PREPARE|real'
done
```

Expected: 3回とも `PREPARE: PASS`、`real` が 6秒未満。

**6秒を超えた場合**: ページ走査と検索プローブでリクエスト数が増えたことが原因。`docs/phase4-notes.md` に実測値を記録し、プローブを減らすか、ゲート基準そのものを見直すかを判断する材料にすること。**基準を独断で緩めないこと。**

- [ ] **Step 3: ゲート3(60秒走行、スコア)**

```bash
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json
```

Expected: `RESULT: PASS`、critical 0件。

**採点対象7本すべてが0回でないことを目視で確認すること。** `RESULT: PASS` を信用してはならない(4-A 持ち越し2: エラー上限が絶対件数なので「遅い全滅」を見逃す)。0回のエンドポイントがあれば、それは PASS の表示に関わらず**不合格**として扱う。

- [ ] **Step 4: ゲート4(改悪7種がすべて FAIL すること)**

各改悪を1つずつ入れ、ベンチを走らせ、`FAIL` になることを確認したうえで `git checkout` で戻す。改悪を2つ以上同時に入れないこと。

| # | 改悪 | 対象ファイル | 期待 |
|---|---|---|---|
| 1 | `LIKE ?` のパターンを `q + "%"`(前方一致)に変える | `webapp/go/auctions.go` の `where()` | Prepare が検索集合の不一致で FAIL |
| 2 | `OR description LIKE ?` を削る(引数も1つ減らす) | 同上 | プローブ `手作業` で FAIL |
| 3 | `AND category_id = ?` を `OR category_id = ?` に変える | 同上 | AND結合プローブで FAIL |
| 4 | `TotalCount: total` を `TotalCount: int64(len(summaries))` に変える | `getAuctions` | Prepare が total_count 不一致で FAIL |
| 5 | `ORDER BY ends_at ASC, id ASC` を `ORDER BY id ASC` に変える | 同上 | `ends_at` 非減少で FAIL |
| 6 | `LIMIT ? OFFSET ?` を削る(引数も削る) | 同上 | `len(auctions) <= 20` で FAIL |
| 7 | `postBid` の `SELECT ... FOR UPDATE` から `FOR UPDATE` を削る | `webapp/go/bids.go` | 単調増加検査で FAIL |

各改悪の手順:

```bash
# 改悪を入れる(エディタで編集)
docker compose -f dev/compose.yaml build app && docker compose -f dev/compose.yaml up -d
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json
# FAIL とエラー内容を記録
cd .. && git checkout webapp/go/auctions.go   # または bids.go
docker compose -f dev/compose.yaml build app && docker compose -f dev/compose.yaml up -d
```

**改悪7(`FOR UPDATE`)は3回まで走らせてよい。** 単調増加検出器は確率的で、4-A では3回中1回しか発火しなかった実績がある。3回とも発火しなかった場合は、bidder の `page=1` 集中が効いていないことを意味するので、**PASS とせず** `docs/phase4-notes.md` に記録して持ち越すこと。

- [ ] **Step 5: ゲート5(非回帰)**

ゲート3のスコアを 4-A の実測(`docs/phase4-notes.md` の「実測ログ」、SCORE 6177 前後)と比較する。

`GET /auctions` の回数は**増える**はず(1ページ20件になり1リクエストが軽くなるため)。逆に減っている場合は原因を突き止めること。総スコアが 4-A の水準から大きく落ちている場合も同様。

- [ ] **Step 6: medium / full の探り**

```bash
cd initial-data
go run . -scale medium -out /tmp/isubid-medium
go run . -scale full   -out /tmp/isubid-full
```

生成物を `ISUBID_INITIAL_DATA_DIR` で読ませ(4-A の実測ログの手順に従う)、各1回だけ60秒走行させる。

記録するのは**1点だけ**:

> ページネーション導入後、`GET /auctions` は `medium` / `full` で完走するか(4-A では `full` で 0回だった)

**採用スケールはここでは変えない。** `medium`/`full` の生成物はコミットしない。

- [ ] **Step 7: `docs/phase4-notes.md` に 4-B のセクションを追記する**

以下を含めること。

- 設計判断(レスポンス形を `{auctions,total_count,has_next}` にした理由、`total_count` がトランザクションを必要とする関係、Load の一方向検査、Load のプローブが title 専用に限られる理由)
- ゲート1〜5 の実測ログ(**ツールの実出力を転記**)
- 改悪7種それぞれの検出結果(検出したエラーメッセージつき)
- `medium` / `full` の探りの結果
- 4-E への持ち越し。最低限、以下の2つを新規に足すこと:
  - **オフセットページネーションの読み飛ばし。** Prepare の全ページ走査中に先頭側の live が closed になると後続ページが手前へずれ、まだ読んでいない行が読み飛ばされる。期限切れ許容は「消えた行」しか救わないため、ずれて隠れた行は「期限前なのに欠けている」として false-FAIL になりうる。採用スケール `small` では最短の期限が +12秒、Prepare 実測が数秒なので窓に余裕があるが、`full`(Prepare 11〜12秒)では危険。候補の対策: 最終ページから逆順に辿る(行が先頭から消えると逆順走査では重複が出るだけで、欠落にはならない = 安全な向きに倒れる)
  - **`total_count` をトランザクション外に出す改変は検出できない。** 理論上のレースとしてしか現れないため、一貫性の担保はコード構造(単一トランザクション)に委ねており、ベンチによる検出は期待していない
- 4-A の持ち越し7 を Task 7 で解消したことを、該当箇所に追記する(持ち越しリストから消すのではなく「4-B で対応済み」と明記する)

- [ ] **Step 8: 全モジュールの最終確認**

```bash
cd /Users/abe/ghq/github.com/kyosu-1/isubid
gofmt -l ./webapp/go ./bench ./initial-data
(cd webapp/go && go vet ./... && go test ./...)
(cd bench && go vet ./... && go test ./...)
(cd initial-data && go vet ./... && go test ./...)
git status --short
```

Expected: gofmt 出力なし、vet クリーン、3モジュールとも `ok`、作業ツリーがクリーン(改悪を戻し忘れていないこと)。

- [ ] **Step 9: コミット**

```bash
git add docs/phase4-notes.md
git commit -m "docs: 4-Bのゲート実測と4-Eへの持ち越しを記録する"
```

---

## Self-Review メモ

**Spec カバレッジ:**

| Spec セクション | 実装タスク |
|---|---|
| §1 API 契約 | Task 2(page)、Task 3(q/category) |
| §2 参照実装・仕込み5点 | Task 2 |
| §3-1 スナップショット拡張 | Task 1 |
| §3-2 プローブ語 | Task 6 |
| §3-3 期限切れ許容・持ち越し7 | Task 6(検索)、Task 7(詳細) |
| §3-4 Prepare | Task 5(全ページ走査)、Task 6(検索・不正値) |
| §3-5 Load | Task 8 |
| §4-1 シナリオ | Task 8 |
| §4-2 採点 | Task 8 |
| §5 完了ゲート G1〜G5 | Task 9 |
| §5-2 検出できないもの | Task 9 Step 7 |
| §6 影響範囲 | Task 1〜9 で全ファイルを網羅 |
| §7 4-D への影響 | 4-D の実装時に反映(4-B では扱わない) |
| §8 medium/full の探り | Task 9 Step 6 |

**型の一貫性:** `auctionsPerPage` は `webapp/go/auctions.go` と `bench/validate.go` の両方で 20。`AuctionList` / `AuctionListParams` / `ValidatePagedListShape` / `fetchAllAuctionPages` / `expectedLiveMatches` / `ValidateSearchResult` の名前とシグネチャは Task 4〜8 を通じて一致している。`SnapshotAuction.Description` は Task 1 で両モジュールに追加され Task 6 が読む。
