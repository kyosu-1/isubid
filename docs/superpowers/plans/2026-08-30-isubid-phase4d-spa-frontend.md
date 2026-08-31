# Phase 4-D SPAフロントエンドとアセット追従検証 実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ISUBID に React 製 SPA を載せ、静的配信をアプリ経由の意図的なボトルネックとして成立させ、ベンチがアセットの正しさをハッシュで検証し、静的配信の負荷を走行に乗せる。

**Architecture:** API を `/api` 配下へ移して SPA のクライアントルートとの衝突を解く。`webapp/public/` にビルド済み SPA をコミットし、アプリが `os.ReadFile` で素朴に配信する(キャッシュヘッダ無し・非圧縮)。ビルド成果物の sha256 マニフェストをベンチバイナリに `go:embed` で埋め込み、Prepare で全アセットのバイト一致を検証、Load では訪問者 worker がページロードを行って静的配信の負荷を走行に乗せる。

**Tech Stack:** Go 1.26 / chi v5 / sqlx / React 18 / Vite 5 / TypeScript / react-router-dom / isucandar

**Spec:** `docs/superpowers/specs/2026-08-30-isubid-phase4d-spa-frontend-design.md`

> **改訂について(2026-08-31)。** 本計画は 4-B / 4-C / 4-E1 が着地する前に書かれた。
> 設計文書が同日に改訂されたので、それを正として次を追随させた。9タスクの分割と順序は
> 変えていない。
>
> | 改訂 | 影響したタスク | 元はどうだったか |
> |---|---|---|
> | `/api` 移行の対象が11ルート・`bench/client.go` 12箇所・`bench/scenario.go` 2箇所(いずれも 2026-08-31 の実測) | Task 1 | `/users/{id}/icon`(4-C)が無い10ルート前提。置換対象に `bench/scenario.go` が入っていなかった |
> | 一覧 API が `{auctions, total_count, has_next}` になり `?page=` を受ける(4-B) | Task 4・Task 5・Task 8 | 裸の JSON 配列前提。`api.auctions()` に page が無く、一覧画面にページ送りも無かった |
> | `GET /api/users/:id/icon` が実在する(4-C) | Task 4・Task 5・Task 6 | アイコンは「4-C 待ち」の未確定事項だった |
> | 採点タグの登録先が `bench/liveness.go` の `scoredTags` と `livenessRequired` の2箇所になった(4-E1) | Task 8 | `bench/main.go` に内訳が直書きされていた時代の前提。`scoredTags` も `livenessRequired` も存在しなかった |
> | ゲート判定が `LIVENESS: PASS` の確認に置き換わり、G5(Prepare 3回連続・6秒基準)が増えた(4-E1) | Task 9 | 「採点対象すべてが0回でないことを目視で明示的に確認」だった |
>
> 現物との照合で見つかった食い違い(計画が書かれた後にコードが動いていた箇所)も
> あわせて直した。各タスクの `> **改訂:**` 引用がその内訳である。

## Global Constraints

- 両モジュールとも Go 1.26。`gofmt -l` が何も出力せず、`go vet ./...` が通ること
- コード内のコメント・エラーメッセージは日本語(既存コードに合わせる)
- **既存の「意図的に遅い実装」を修正しない。** `docs/phase2-notes.md` のインベントリにあるものはすべて仕様である
- **`dev/nginx.conf` は変更しない。** Task 9 の G4 計測でのみ一時的に変更し、計測後に必ず戻す
- `webapp/public/` 配下の合計サイズ(未圧縮、html+js+css+favicon)は **300KB 未満**
- フロントエンドに状態管理ライブラリと CSS フレームワークを追加しない
- **`GET /api/me` に意図的な遅さを入れない**(全画面の初期表示が通るため、他の計測を濁らせる)
- **`ScoreGETPage` はページロード1回につき1点。アセット1本ごとに加点しない**(キャッシュを効かせる正しい最適化がスコアを下げるため)
- **新しい採点タグは `bench/score.go` の `scoreTable` に加えて、`bench/liveness.go` の
  `scoredTags` と `livenessRequired` の両方へ必ず登録する**(4-E1)。`scoredTags` を忘れると
  内訳の合計が `raw` と一致しなくなり、`livenessRequired` を忘れると新エンドポイントが
  liveness 判定を黙ってすり抜ける。`TestScoredTagsCoversScoreTable` と
  `TestLivenessRequiredCoversAllTags` の2本が固定しているので、忘れれば `bench` の単体テストが落ちる
- **一覧 API の契約は `{auctions, total_count, has_next}`**(4-B)。`?page=` / `?q=` / `?category=`
  を受ける。`per_page` は返らずサーバ定数の **20件固定**。**裸の JSON 配列を前提にしたコードを書かない**
- **カテゴリはフロント側に3件をハードコードする。** `GET /api/categories` を作らない
  (`/initialize` が常に同じ3件へ戻すため。API 面を増やすとベンチ検証も足す必要が出る)
- アセット検証は **200 と 304 の両方を受理**し、ハッシュは**デコード後のバイト列**で取る(gzip/br を許容するため)
- Content-Type の検証は `/index.html` が `text/html` で始まること、それ以外のアセットが `text/html` で始まら**ない**ことのみ
- 実測値は実際に観測した出力だけを書く。走らせていない結果を書かない
- Phase 3 のスコアと比較して性能の優劣を述べない(初期データが異なり同一条件ではない)

## File Structure

| ファイル | 責務 |
|---|---|
| `webapp/go/main.go` | ルータ。`/api` サブルータ + `NotFound` → 静的配信 |
| `webapp/go/me.go` / `me_test.go` | `GET /api/me`。SPA の身元解決専用、遅くしない |
| `webapp/go/static.go` / `static_test.go` | 意図的に遅い静的配信と SPA フォールバック |
| `webapp/frontend/` | SPA ソース(React+Vite+TS)。参加者は触らない |
| `webapp/public/` | ビルド成果物。**コミットする** |
| `bench/assets.go` | マニフェスト定義、Prepare のハッシュ照合、ページロード処理 |
| `bench/assets/manifest.json` | `go:embed` される正解ハッシュ表 |
| `bench/cmd/genmanifest/main.go` | `webapp/public` からマニフェストを生成 |
| `bench/assets_test.go` | マニフェストのドリフト検知とバンドル上限 |
| `bench/liveness.go` / `liveness_test.go` | **(4-E1 で新設)** `scoredTags` と `livenessRequired`。`ScoreGETPage` の登録先(Task 8) |
| `bench/main.go` | フラグ、スコア内訳の出力(`scoredTags` を回す)、`workerCounts` の組み立て |
| `dev/compose.yaml` | `webapp/public` のマウントと `ISUBID_PUBLIC_DIR` |

---

### Task 1: API を `/api` 配下へ移す

**Files:**
- Modify: `webapp/go/main.go`, `webapp/go/*_test.go`(全ファイル), `bench/client.go`, `bench/load.go`, `bench/scenario.go`, `README.md`

**Interfaces:**
- Produces: 全 API が `/api` prefix を持つ。`/api` 以外のパスは以降のタスクで静的配信に回る

> **改訂(2026-08-31):** 執筆時点で `GET /users/{id}/icon` は存在しなかった(4-C で実装)。
> 移行対象は **11ルート**、`bench/client.go` の**12箇所**、`bench/scenario.go` の**2箇所**である
> (設計 §3.1 の表と、下記 Step 2 のコマンドで実測して確認すること)。
> 置換対象に `bench/scenario.go` が入っていなかったのも欠落だったので足した。
> 置換の正規表現にも `users` を加えてある。

**`POST /initialize` も `/api` 配下へ移す**(設計 §3.1)。例外を作ると nginx の分担
(`location /api/ → app` / それ以外は静的)が割れなくなり、この案を採った理由が薄れる。
下の `routerFor` はそうなっている。

- [ ] **Step 1: ルータを `/api` で束ねる**

`webapp/go/main.go` の `routerFor` を差し替える。ハンドラ本体は一切変更しない。
**11ルートすべてが `/api` の内側に入ること**(`/users/{id}/icon` を落とさない)。

```go
// routerFor は handler からルーターを組み立てる。
// main はバッチ用に handler を先に作る必要があるため分離している。
//
// API は /api 配下に置く。SPA のクライアントルート(/auctions/123 など)と
// API のパスが同一になると、chi が API ハンドラを先にマッチさせてしまい
// ディープリンクが JSON を返す。ISUCON11/12/13 が同じ理由で同じ形を採っている。
//
// POST /initialize も例外にしない。ここだけ /api の外に出すと nginx の分担
// (location /api/ → app / それ以外は静的)が割れなくなる。
func routerFor(h *handler) http.Handler {
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Post("/initialize", h.postInitialize)
		r.Post("/register", h.postRegister)
		r.Post("/login", h.postLogin)
		r.Get("/auctions", h.getAuctions)
		r.Post("/auctions", h.postAuction)
		r.Get("/auctions/{id}", h.getAuction)
		r.Get("/auctions/{id}/bids", h.getAuctionBids)
		r.Post("/auctions/{id}/bids", h.postBid)
		r.Get("/notifications", h.getNotifications)
		r.Get("/users/{id}/icon", h.getUserIcon)
		r.Get("/stats/me", h.getStatsMe)
	})
	return r
}
```

- [ ] **Step 2: 移行前のパス出現箇所を数える**

Run:
```bash
grep -c '"/\(initialize\|register\|login\|auctions\|notifications\|stats\|users\)' \
  webapp/go/*_test.go bench/client.go bench/scenario.go
```
出力を控えておく。Step 4 の検算に使う。**2026-08-31 時点の実測では
`bench/client.go` が 12、`bench/scenario.go` が 3**(うち1つは
`// GetAuctionsRaw は "/auctions?" + rawQuery を…` というコメント。置換されるが、
移行後の実際のパスは `/api/auctions?` なので書き換わったほうが正しい)。

- [ ] **Step 3: テストとベンチのパス文字列を機械的に置換する**

Run:
```bash
perl -pi -e 's{"/(initialize|register|login|auctions|notifications|stats|users)}{"/api/$1}g' \
  webapp/go/*_test.go bench/client.go bench/scenario.go
```

`users` を含めるのは `bench/client.go` の `GetUserIcon`
(`fmt.Sprintf("/users/%d/icon", id)`)と `bench/scenario.go` の
`c.doRaw(ctx, "/users/notanumber/icon")` のためである。この正規表現は
`"` の直後に来るパスだけを拾うので、`"GET /auctions: …"` のような**エラーメッセージは
マッチしない**(それらは Step 5 で直す)。

- [ ] **Step 4: 二重付与が無いことを確認する**

Run:
```bash
grep -rn '"/api/api/' webapp/go bench ; echo "exit=$?"
```
Expected: 何もマッチしない(`exit=1`)。マッチしたら該当行を手で直す。

Run:
```bash
grep -rn '"/\(initialize\|register\|login\|auctions\|notifications\|stats\|users\)' \
  webapp/go/*_test.go bench/client.go bench/scenario.go ; echo "exit=$?"
```
Expected: 何もマッチしない(`exit=1`)。残っていれば `/api` を付け忘れている。

Run:
```bash
grep -c 'r\.\(Get\|Post\)("' webapp/go/main.go
```
Expected: `11`。10 なら `/users/{id}/icon` を落としている。

- [ ] **Step 5: ベンチのエラーメッセージ内のパス表記も直す**

エラーメッセージは参加者が読むので実際のパスと一致させる。

Run:
```bash
grep -rn 'GET /auctions\|POST /auctions\|GET /notifications\|GET /stats/me\|POST /initialize\|POST /login\|POST /register\|GET /users/' bench/*.go
```
ヒットした文字列リテラル(`fmt.Errorf` / `fmt.Sprintf` の中)のパス部分に `/api` を足す。
Step 3 でパスそのものは置換済みなので、ここで残るのは**メッセージ内の表記だけ**である。
対象は `bench/client.go`(`GET %s` 形式でないベタ書き: `GET /notifications` /
`GET /stats/me` / `POST /auctions` / `POST /initialize`)、`bench/load.go`、
`bench/scenario.go`(`GET /users/notanumber/icon` を含む)。

なお `bench/scenario.go` の検索プローブのラベル
(`"GET /auctions?q=" + probeTitleOnly + " (title専用プローブ)"` など5件)も
ここに含まれる。ラベルはそのまま Prepare の失敗メッセージに出るので忘れないこと。

**採点タグの文字列(`bench/score.go` の `ScoreGETList = "GET /auctions"` など)と
`bench/liveness.go` の `scoredTags` の表示名は、本フェーズでは触らない。** これは
意図的な先送りである: 両者は必ず対で直す必要があり(片方だけ直すと
`TestScoredTagsCoversScoreTable` は通るのに内訳の表示名だけがズレる)、直すと
`docs/phase4-notes.md` に残る 4-A / 4-B / 4-C / 4-E1 の実測ログとの文字列突合が
すべて壊れる。表示名は正しさの検証に一切使われていないので、**`/api` を反映するかどうかは
4-F(レギュレーション文書)で他の表記と一緒に決める**。Task 9 の持ち越しに書くこと。

- [ ] **Step 6: README のコマンド例を直す**

`README.md` 内の `curl` 例やエンドポイント一覧に `/api` を足す。

- [ ] **Step 7: テストを流す**

Run:
```bash
docker compose -f dev/compose.yaml up -d
cd webapp/go && go test -count=1 ./...
```
Expected: 全 PASS。**MySQL が必要**(テストは実 DB を使う)。

Run:
```bash
cd bench && go build ./... && go vet ./...
cd .. && gofmt -l webapp/go bench
```
Expected: エラーなし、`gofmt -l` は無出力。

- [ ] **Step 8: コミット**

```bash
git add webapp/go bench README.md
git commit -m "refactor: API を /api 配下へ移し SPA のクライアントルートとの衝突を解く"
```

---

### Task 2: `GET /api/me` を足す

**Files:**
- Create: `webapp/go/me.go`, `webapp/go/me_test.go`
- Modify: `webapp/go/main.go`, `bench/client.go`, `bench/scenario.go`

**Interfaces:**
- Consumes: Task 1 の `/api` サブルータ
- Produces: `GET /api/me` → 200 `{"id":int64,"name":string}` / 401 `{"error":"login required"}`。ベンチ側は `func (c *Client) GetMe(ctx context.Context) (*User, error)`

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/me_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGetMeLoggedIn(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := loginSeedUser(t, ts.URL, "seed_user_05")

	res, err := client.Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/me status = %d, want 200", res.StatusCode)
	}
	var u struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(res.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.ID != 5 || u.Name != "seed_user_05" {
		t.Errorf("GET /api/me = %+v, want id=5 name=seed_user_05", u)
	}
}

func TestGetMeGuest(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := newClientWithJar(t)

	res, err := client.Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/me status = %d, want 401", res.StatusCode)
	}
}
```

- [ ] **Step 2: テストが落ちることを確認する**

Run: `cd webapp/go && go test -count=1 -run 'TestGetMe' ./...`
Expected: FAIL(404 が返る)

- [ ] **Step 3: ハンドラを書く**

`webapp/go/me.go`:

```go
package main

import (
	"database/sql"
	"errors"
	"net/http"
)

// getMe はログイン中のユーザーを返す。
//
// SPA はリロード後に「自分が誰か」を知る必要があるが、セッションは httpOnly Cookie なので
// JS からは読めない。ログイン応答をクライアント側に保存する方式は Cookie と保存値が
// 乖離しうるため、サーバーに1本聞く形にしている。
//
// ここには意図的な遅さを入れていない。全画面の初期表示が必ずこの1本を通るため、
// 遅くしても改善路が増えず、他のボトルネックの計測を濁らせるだけである。
func (h *handler) getMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	var u struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
	}
	err := h.db.GetContext(r.Context(), &u,
		"SELECT id, name FROM users WHERE id = ?", userID)
	// initialize を挟んで古いセッションが残ると、存在しない user_id を指しうる。
	// これはログインしていないのと同じ扱いにする。
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, userResponse{ID: u.ID, Name: u.Name})
}
```

`webapp/go/main.go` の `/api` サブルータに1行足す(`r.Post("/login", ...)` の直後):

```go
		r.Get("/me", h.getMe)
```

- [ ] **Step 4: テストが通ることを確認する**

Run: `cd webapp/go && go test -count=1 -run 'TestGetMe' ./...`
Expected: PASS

- [ ] **Step 5: ベンチにクライアントメソッドを足す**

`bench/client.go` の `Login` の直後に追加:

```go
// GetMe はログイン中のユーザーを返す。未ログインなら401を期待する呼び出し側のために
// ステータスをそのまま返さず、401 は (nil, nil) で表現する。
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	code, b, err := c.doJSON(ctx, http.MethodGet, "/api/me", nil)
	if err != nil {
		return nil, err
	}
	if code == http.StatusUnauthorized {
		return nil, nil
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET /api/me: status %d (期待: 200 か 401, body: %s)", code, b)
	}
	var u User
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, fmt.Errorf("GET /api/me: 不正なJSON: %w", err)
	}
	return &u, nil
}
```

- [ ] **Step 6: Prepare に検証を足す**

> **改訂(2026-08-31):** 元は「既存のログイン済みクライアント変数名は実際のコードを読んで
> 合わせること」というプレースホルダだった。現物(`bench/scenario.go` の `Prepare` 手順3)を
> 読んで確定させた。ログイン済みクライアントは `seedClient`、その User は `seedUser` である。
> Prepare の主クライアント `c` は手順3で `Register` しているため**ログイン済み**であり、
> 未ログインの検証には使えない。

`bench/scenario.go` の `Prepare` の手順3(`// 3. 新規ユーザー登録と、シードユーザーのログイン`)の
末尾、`seedUser, err := seedClient.Login(...)` のエラーチェック直後・手順4
(`// 4. 入札の検証`)の手前に、そのまま以下を挿入する。

```go
	// セッションが実際に確立していることを /api/me で確認する。
	// SPA はリロード後にこの1本だけで身元を解決するので、ここが壊れると
	// ログイン済みの画面がすべてゲスト表示になる。
	me, err := seedClient.GetMe(ctx)
	if err != nil {
		return err
	}
	if me == nil {
		return fmt.Errorf("GET /api/me: ログイン直後なのに401が返った")
	}
	if me.ID != seedUser.ID || me.Name != seedUser.Name {
		return fmt.Errorf("GET /api/me: %+v (期待: %+v)", *me, *seedUser)
	}

	// 未ログインのクライアントでは401になること。
	// c は上で Register 済み(= ログイン済み)なので流用できない。
	guest, err := NewClient(s.Target)
	if err != nil {
		return err
	}
	if g, err := guest.GetMe(ctx); err != nil {
		return err
	} else if g != nil {
		return fmt.Errorf("GET /api/me: 未ログインなのにユーザーが返った: %+v", *g)
	}
```

- [ ] **Step 7: 全テストと Prepare を流す**

Run:
```bash
cd webapp/go && go test -count=1 ./...
cd ../bench && go vet ./... && go run . -target http://localhost:8080 -prepare-only
```
Expected: テスト全 PASS、`PREPARE: PASS`

- [ ] **Step 8: コミット**

```bash
git add webapp/go/me.go webapp/go/me_test.go webapp/go/main.go bench/client.go bench/scenario.go
git commit -m "feat: GET /api/me を追加し SPA のリロード後の身元解決を可能にする"
```

---

### Task 3: 意図的に遅い静的配信

**Files:**
- Create: `webapp/go/static.go`, `webapp/go/static_test.go`, `webapp/public/index.html`(暫定)
- Modify: `webapp/go/main.go`, `dev/compose.yaml`

**Interfaces:**
- Consumes: Task 1 の `/api` サブルータ
- Produces: `/api` 以外の全パスが静的配信に回る。`resolveStaticPath(root, urlPath string) string` は URL パスを public 配下の実パスへ解決する純粋関数

> **改訂(2026-08-31): 内容の変更なし。** 4-B / 4-C / 4-E1 は静的配信に触れていない。
> 現物と照合して次を確認済み: `webapp/go/go.mod` の chi は **v5.3.1**(Step 1 のテストの
> コメントが主張しているとおり)、`dev/compose.yaml` の `app` / `nginx` に
> `webapp/public` のマウントはまだ無い(Step 6 で足す)、`getEnv` は `webapp/go/db.go`、
> `writeError` は `webapp/go/main.go` にある。
>
> アイコン(`/api/users/{id}/icon`)は Task 1 で `/api` の内側に入るので、静的ハンドラには
> 到達しない(設計 §7.3)。`serveStatic` 側に特別扱いは要らない。

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/static_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newStaticFixture は public ディレクトリの模型を作り、ISUBID_PUBLIC_DIR を向ける。
func newStaticFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "<!doctype html><html><body>isubid</body></html>")
	write(filepath.Join("assets", "app.js"), "console.log('isubid')")
	write(filepath.Join("assets", "app.css"), "body{margin:0}")
	t.Setenv("ISUBID_PUBLIC_DIR", dir)
	return dir
}

// getStatic は静的配信ハンドラだけを直接叩く(DB を必要としない)。
func getStatic(t *testing.T, urlPath string) *httptest.ResponseRecorder {
	t.Helper()
	h := &handler{}
	req := httptest.NewRequest(http.MethodGet, "http://example.com"+urlPath, nil)
	rec := httptest.NewRecorder()
	h.serveStatic(rec, req)
	return rec
}

func TestServeStaticIndex(t *testing.T) {
	newStaticFixture(t)
	rec := getStatic(t, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html...", ct)
	}
	if !strings.Contains(rec.Body.String(), "isubid") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestServeStaticAsset(t *testing.T) {
	newStaticFixture(t)
	rec := getStatic(t, "/assets/app.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.js = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript...", ct)
	}
	if rec.Body.String() != "console.log('isubid')" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// 意図的に遅い実装であることの固定: キャッシュ関連ヘッダを付けない。
// これが付き始めたら仕込みが失われている(参加者の環境ではなく参照実装の話)。
func TestServeStaticHasNoCacheHeaders(t *testing.T) {
	newStaticFixture(t)
	rec := getStatic(t, "/assets/app.js")
	for _, k := range []string{"Cache-Control", "ETag", "Last-Modified", "Content-Encoding"} {
		if v := rec.Header().Get(k); v != "" {
			t.Errorf("%s = %q, want 空(意図的に遅い実装)", k, v)
		}
	}
}

func TestServeStaticSPAFallback(t *testing.T) {
	newStaticFixture(t)
	for _, p := range []string{"/auctions/123", "/notifications", "/stats", "/login", "/sell"} {
		rec := getStatic(t, p)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200(SPAフォールバック)", p, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "isubid") {
			t.Errorf("GET %s は index.html を返すべき", p)
		}
	}
}

// 存在しないアセットに index.html を返すと「壊れているのに壊れて見えない」状態になる。
func TestServeStaticMissingAssetIs404(t *testing.T) {
	newStaticFixture(t)
	for _, p := range []string{"/assets/missing.js", "/missing.png", "/favicon.ico"} {
		rec := getStatic(t, p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
	}
}

// 未知の API パスは SPA ではないので 404 を返す。
func TestServeStaticUnknownAPIIs404(t *testing.T) {
	newStaticFixture(t)
	for _, p := range []string{"/api", "/api/unknown", "/api/auctions/1/nope"} {
		rec := getStatic(t, p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<!doctype") {
			t.Errorf("GET %s が HTML を返した", p)
		}
	}
}

// ルータ経由でも同じ挙動になることを固定する。
//
// chi の Mux.NotFound は updateSubRoutes で既存のサブルータにも遡ってハンドラを配るため、
// r.Route("/api", ...) の後に r.NotFound(...) を書いても /api 配下の未知パスは
// serveStatic に届く(chi v5.3.1 の mux.go で確認済み)。この伝播が将来変わると
// /api/unknown が chi 既定のプレーンテキスト404になり、静かに挙動が変わる。
// DB を触るルートは叩かないので handler は空のままでよい。
func TestRouterSendsNonAPIPathsToStatic(t *testing.T) {
	newStaticFixture(t)
	r := routerFor(&handler{})

	cases := []struct {
		path     string
		wantCode int
		wantHTML bool
	}{
		{"/", http.StatusOK, true},
		{"/auctions/123", http.StatusOK, true},
		{"/assets/app.js", http.StatusOK, false},
		{"/assets/missing.js", http.StatusNotFound, false},
		{"/api/unknown", http.StatusNotFound, false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "http://example.com"+tc.path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != tc.wantCode {
			t.Errorf("GET %s = %d, want %d", tc.path, rec.Code, tc.wantCode)
			continue
		}
		isHTML := strings.Contains(rec.Body.String(), "<!doctype")
		if isHTML != tc.wantHTML {
			t.Errorf("GET %s: HTML=%v, want %v (body=%.60q)", tc.path, isHTML, tc.wantHTML, rec.Body.String())
		}
	}
}

// resolveStaticPath は敵対的な入力でも root の外を指さない、という性質を固定する。
// (path.Clean("/"+p) が ".." を先に潰すため、実際には外へ出る経路が無いことの証明)
func TestResolveStaticPathStaysUnderRoot(t *testing.T) {
	root := "/srv/public"
	inputs := []string{
		"/", "/index.html", "/assets/app.js",
		"/../etc/passwd", "/../../etc/passwd", "/assets/../../etc/passwd",
		"/./../../etc/passwd", "//etc/passwd", "/assets/./app.js",
	}
	for _, in := range inputs {
		got := resolveStaticPath(root, in)
		if got != root && !strings.HasPrefix(got, root+string(filepath.Separator)) {
			t.Errorf("resolveStaticPath(%q, %q) = %q は root の外を指している", root, in, got)
		}
	}
}
```

- [ ] **Step 2: テストが落ちることを確認する**

Run: `cd webapp/go && go test -count=1 -run 'Static|ResolveStatic|Router' ./...`
Expected: コンパイルエラー(`serveStatic` / `resolveStaticPath` が未定義)

- [ ] **Step 3: 静的配信を実装する**

`webapp/go/static.go`:

```go
package main

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// contentTypes は拡張子から Content-Type を引く自前の表。
//
// 意図的に遅い実装: mime パッケージにも http.ServeContent にも頼らない。
// 静的配信まわりを素朴なままにしておくことで、nginx 直配信への移行が
// 参加者にとって意味のある改善になる。
var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".svg":  "image/svg+xml",
	".ico":  "image/x-icon",
	".json": "application/json; charset=utf-8",
	".map":  "application/json; charset=utf-8",
	".png":  "image/png",
	".webp": "image/webp",
}

// resolveStaticPath は URL パスを public ディレクトリ内の実パスへ解決する。
//
// path.Clean("/"+urlPath) は先頭に "/" を付けてから正規化するため、結果は必ず "/" 始まりで
// ".." を含まない。したがって filepath.Join の結果が root の外を指すことはない。
// この性質は TestResolveStaticPathStaysUnderRoot で敵対的な入力ごと固定してある。
func resolveStaticPath(root, urlPath string) string {
	clean := path.Clean("/" + urlPath)
	return filepath.Join(root, filepath.FromSlash(clean))
}

// serveStatic は webapp/public 配下のビルド済み SPA を配信する。
//
// 意図的に遅い実装: リクエストのたびにファイル全体を os.ReadFile でメモリに読み、
// Cache-Control / ETag / Last-Modified を一切付けず、gzip も行わない。
// 条件付き GET は常に 200 になる。ベンチは 304 も圧縮も受理するので、
// nginx 直配信・キャッシュヘッダ・gzip のいずれもスコアが伸びる方向にしか働かない。
//
// 配信規則:
//  1. /api 配下は未知のAPIパスなので 404(SPA ではない)
//  2. 実ファイルがあればそれを返す
//  3. 無い場合、/assets/ 配下か拡張子付きのパスは 404。
//     ここで index.html を 200 で返すと「壊れているのに壊れて見えない」状態を作ってしまう
//  4. それ以外(SPA のクライアントルート)は index.html を 200 で返す
func (h *handler) serveStatic(w http.ResponseWriter, r *http.Request) {
	root := getEnv("ISUBID_PUBLIC_DIR", "../public")
	clean := path.Clean("/" + r.URL.Path)

	if clean == "/api" || strings.HasPrefix(clean, "/api/") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	full := resolveStaticPath(root, r.URL.Path)
	if b, err := os.ReadFile(full); err == nil {
		writeStaticFile(w, full, b)
		return
	}

	if strings.HasPrefix(clean, "/assets/") || path.Ext(clean) != "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	index := filepath.Join(root, "index.html")
	b, err := os.ReadFile(index)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeStaticFile(w, index, b)
}

func writeStaticFile(w http.ResponseWriter, name string, b []byte) {
	ct, ok := contentTypes[strings.ToLower(filepath.Ext(name))]
	if !ok {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}
```

`webapp/go/main.go` の `routerFor` の `r.Route("/api", ...)` の**後ろ**に1行足す:

```go
	// /api 以外はすべて静的配信(SPA)へ回す。
	r.NotFound(h.serveStatic)
```

- [ ] **Step 4: テストが通ることを確認する**

Run: `cd webapp/go && go test -count=1 -run 'Static|ResolveStatic|Router' ./...`
Expected: 全 PASS

- [ ] **Step 5: 暫定の index.html を置く**

SPA が載るのは Task 6 なので、それまでスタックが壊れないよう最小の HTML を置く。

`webapp/public/index.html`:

```html
<!doctype html>
<html lang="ja">
  <head>
    <meta charset="utf-8" />
    <title>ISUBID</title>
  </head>
  <body>
    <p>ISUBID: フロントエンドは Phase 4-D のビルド成果物で置き換わります。</p>
  </body>
</html>
```

- [ ] **Step 6: compose に public をマウントする**

`dev/compose.yaml` の `app` サービスを変更する。

`environment` に1行追加:
```yaml
      ISUBID_PUBLIC_DIR: /webapp/public
```
`volumes` に1行追加:
```yaml
      - ../webapp/public:/webapp/public:ro
```

**同じディレクトリを `nginx` サービスにもマウントする**(こちらは `volumes` のみ):
```yaml
      - ../webapp/public:/webapp/public:ro
```

nginx 側のマウントが要る理由: nginx コンテナがビルド成果物を持っていないと、
「静的配信を nginx へ移す」という序盤の王道改善が**そもそも実行できない**。
ファイルは見えるが `dev/nginx.conf` がそれを使っていない、という状態が正しい初期状態である。
`dev/nginx.conf` 自体は変更しない(Global Constraints)。

- [ ] **Step 7: スタックで動作を確認する**

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
curl -s -o /dev/null -w '%{http_code} %{content_type}\n' http://localhost:8080/
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/auctions/1
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/assets/missing.js
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/api/unknown
curl -s -D- -o /dev/null http://localhost:8080/ | grep -i 'cache-control\|etag\|last-modified' ; echo "cache-headers-exit=$?"
```
Expected: `200 text/html; charset=utf-8` / `200` / `404` / `404` / キャッシュヘッダは1つも出ない(`exit=1`)

- [ ] **Step 8: 既存の走行が壊れていないことを確認する**

Run:
```bash
cd bench && go run . -target http://localhost:8080 -prepare-only
```
Expected: `PREPARE: PASS`

- [ ] **Step 9: コミット**

```bash
git add webapp/go/static.go webapp/go/static_test.go webapp/go/main.go webapp/public/index.html dev/compose.yaml
git commit -m "feat: アプリ経由の静的配信を追加(意図的に遅い実装)"
```

---
### Task 4: SPA の土台とログイン画面

**Files:**
- Create: `webapp/frontend/package.json`, `webapp/frontend/vite.config.ts`, `webapp/frontend/tsconfig.json`, `webapp/frontend/tsconfig.node.json`, `webapp/frontend/index.html`, `webapp/frontend/public/favicon.svg`, `webapp/frontend/src/{main.tsx,api.ts,auth.tsx,styles.css}`, `webapp/frontend/src/pages/Login.tsx`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: Task 1 の `/api` prefix、Task 2 の `GET /api/me`
- Produces: `src/api.ts` の `api` オブジェクト(全 API 呼び出しの唯一の入口)と `src/auth.tsx` の `useAuth()`。Task 5 の各画面はこの2つだけを使う

**前提:** Node 20 以上。このマシンでは確認済み(node v22.15.0 / npm 10.9.2)。

- [ ] **Step 1: `.gitignore` に node_modules を足す**

`.gitignore` の末尾に追記:
```
/webapp/frontend/node_modules/
```

`webapp/public/` は**コミットするので無視しない。** `webapp/frontend/package-lock.json` もコミットする(ビルドの再現性のため)。

- [ ] **Step 2: package.json を作る**

`webapp/frontend/package.json`:

```json
{
  "name": "isubid-frontend",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "react": "^18.3.1",
    "react-dom": "^18.3.1",
    "react-router-dom": "^6.26.2"
  },
  "devDependencies": {
    "@types/react": "^18.3.11",
    "@types/react-dom": "^18.3.1",
    "@vitejs/plugin-react": "^4.3.2",
    "typescript": "^5.6.3",
    "vite": "^5.4.9"
  }
}
```

`npm install` が上記のレンジで解決できない場合は、React は 18 系、Vite は 5 系の範囲で解決可能な最新版に落とす。**実際に入ったバージョンを報告に書き、`package-lock.json` をコミットすること。** 状態管理ライブラリと CSS フレームワークは追加しない(Global Constraints)。

- [ ] **Step 3: Vite と TypeScript の設定を作る**

`webapp/frontend/vite.config.ts`:

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// ビルド成果物は webapp/public へ出す。参加者はそれをそのまま配信する。
// sourcemap を出さないのは、バンドル上限(300KB)を守るためと、
// ベンチのマニフェスト対象を js/css/html/favicon に絞るため。
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../public',
    emptyOutDir: true,
    sourcemap: false,
  },
  // 開発時は /api をローカルのスタック(nginx :8080)へ流す。
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
```

`webapp/frontend/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "resolveJsonModule": true,
    "isolatedModules": true,
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true
  },
  "include": ["src"],
  "references": [{ "path": "./tsconfig.node.json" }]
}
```

`webapp/frontend/tsconfig.node.json`:

```json
{
  "compilerOptions": {
    "composite": true,
    "skipLibCheck": true,
    "module": "ESNext",
    "moduleResolution": "bundler",
    "allowSyntheticDefaultImports": true,
    "strict": true,
    "noEmit": true
  },
  "include": ["vite.config.ts"]
}
```

- [ ] **Step 4: index.html と favicon を作る**

`webapp/frontend/index.html`:

```html
<!doctype html>
<html lang="ja">
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" href="/favicon.svg" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>ISUBID — 椅子オークション</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

`webapp/frontend/public/favicon.svg`(Vite が outDir 直下へコピーする):

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
  <rect width="32" height="32" rx="6" fill="#1f4e79"/>
  <path d="M9 8h3v9h8V8h3v12h-4v4h-2v-4h-4v4h-2v-4H9z" fill="#fff"/>
</svg>
```

- [ ] **Step 5: API クライアントを書く**

> **改訂(2026-08-31):** 4-B で一覧の契約が変わり、4-C でアイコンが実装されたので、
> このファイルは書き換わっている。元との差分:
>
> - `api.auctions()` の戻りが `AuctionSummary[]` → **`AuctionList`**
>   (`{auctions, total_count, has_next}`)。`page` 引数を足した
> - `api.notifications()` が `Notification[]` を直に受け取る形だった。
>   **現物の `GET /notifications` は `{"notifications": [...]}` で包まれている**
>   (`webapp/go/notifications.go` の `writeJSON(w, 200, map[string]any{"notifications": out})`、
>   `bench/client.go` の `GetNotifications` も同じ形で剥がしている)。**これは 4-B とは無関係の、
>   計画執筆時からあった誤りである。**そのまま実装すると通知画面が必ず壊れる
> - `iconURL()` を足した(4-C)
> - `CATEGORIES` のコメントにあった「4-B がカテゴリ絞り込みを実装する際に
>   `GET /api/categories` を設けるなら、ここを差し替える」は、4-B が着地して
>   専用エンドポイントを設けないと決まったので落とした(設計 §7.2)

`webapp/frontend/src/api.ts`。型は `bench/model.go` の JSON タグと1対1で対応させること(ベンチとフロントで同じレスポンスを見ているという保証になる)。

```ts
// API クライアント。画面からの HTTP はすべてここを通す。
// レスポンスの型は bench/model.go の JSON タグと1対1に対応させてある。

export type User = { id: number; name: string }

export type AuctionSummary = {
  id: number
  title: string
  category_id: number
  seller: User
  current_price: number
  bid_count: number
  starts_at: string
  ends_at: string
  status: 'upcoming' | 'live' | 'closed'
}

// GET /api/auctions のレスポンス(4-B)。裸の配列ではない。
// per_page は返らない。サーバ定数の20件固定で、次ページの有無は has_next が持つ。
export type AuctionList = {
  auctions: AuctionSummary[]
  total_count: number
  has_next: boolean
}

// AUCTIONS_PER_PAGE はサーバ側の定数(webapp/go/auctions.go の auctionsPerPage)を
// 画面が「何件目〜何件目を表示中か」を出すためだけに写したもの。
// リクエストには送らない(API は per_page を受け付けない)。
export const AUCTIONS_PER_PAGE = 20

export type Bid = { id: number; user: User; amount: number; created_at: string }

export type AuctionDetail = AuctionSummary & {
  description: string
  starting_price: number
  winner_id: number | null
  winning_price: number | null
  bids: Bid[]
}

export type Notification = {
  id: number
  type: string
  auction_id: number
  message: string
  is_read: boolean
  created_at: string
}

export type Stats = {
  listed_count: number
  sold_count: number
  total_sales: number
  live_count: number
}

export type AuctionCreated = {
  id: number
  title: string
  starting_price: number
  ends_at: string
  status: string
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch('/api' + path, {
    ...init,
    credentials: 'same-origin',
    headers: init?.body ? { 'Content-Type': 'application/json' } : undefined,
  })
  const text = await res.text()
  if (!res.ok) {
    let msg = text
    try {
      msg = (JSON.parse(text) as { error?: string }).error ?? text
    } catch {
      // エラー応答がJSONでないこともある(nginxの502など)
    }
    throw new ApiError(res.status, msg)
  }
  return text ? (JSON.parse(text) as T) : (undefined as T)
}

// カテゴリは初期データで固定されている(webapp/sql/90_seed_phase1.sql)。
// 一覧の絞り込みと出品フォームで使う。専用エンドポイント(GET /api/categories)を
// 設けないのは意図的である: この3件は /initialize が必ず同じ内容へ復元する固定値であり、
// API 面を1本増やせばベンチにも検証を1本足す必要が出る(設計 §7.2)。
export const CATEGORIES: { id: number; name: string }[] = [
  { id: 1, name: 'オフィスチェア' },
  { id: 2, name: 'ゲーミングチェア' },
  { id: 3, name: 'アンティーク' },
]

// iconURL は出品者アイコンの URL(4-C)。
// API ルートなので静的ハンドラには届かず、ベンチのアセットマニフェストにも載らない
// (マニフェストは webapp/public を歩いて作る。設計 §7.3)。
export function iconURL(userID: number): string {
  return `/api/users/${userID}/icon`
}

export type AuctionListQuery = { page?: number; q?: string; category?: number }

export const api = {
  me: () => call<User>('/me'),
  login: (name: string, password: string) =>
    call<User>('/login', { method: 'POST', body: JSON.stringify({ name, password }) }),
  register: (name: string, password: string) =>
    call<User>('/register', { method: 'POST', body: JSON.stringify({ name, password }) }),
  // 応答は {auctions, total_count, has_next}(4-B)。裸の配列ではない。
  // page を送らないとサーバー既定の1ページ目になる。
  auctions: ({ page, q, category }: AuctionListQuery = {}) => {
    const p = new URLSearchParams()
    if (page && page > 1) p.set('page', String(page))
    if (q) p.set('q', q)
    if (category) p.set('category', String(category))
    const qs = p.toString()
    return call<AuctionList>('/auctions' + (qs ? '?' + qs : ''))
  },
  auction: (id: number) => call<AuctionDetail>(`/auctions/${id}`),
  // フィードの応答は {"bids":[...]} で包まれている(bench/client.go の GetBidFeed と同じ)。
  bidsSince: (id: number, since: number) =>
    call<{ bids: Bid[] }>(`/auctions/${id}/bids?since=${since}`).then((r) => r.bids),
  bid: (id: number, amount: number) =>
    call<{ id: number }>(`/auctions/${id}/bids`, {
      method: 'POST',
      body: JSON.stringify({ amount }),
    }),
  sell: (input: {
    title: string
    description: string
    category_id: number
    starting_price: number
    duration_seconds: number
  }) => call<AuctionCreated>('/auctions', { method: 'POST', body: JSON.stringify(input) }),
  // 通知の応答も {"notifications":[...]} で包まれている
  // (webapp/go/notifications.go / bench/client.go の GetNotifications と同じ)。
  notifications: () =>
    call<{ notifications: Notification[] }>('/notifications').then((r) => r.notifications),
  stats: () => call<Stats>('/stats/me'),
}
```

**注意:** `api.sell` の `duration_seconds` はサーバー側で 10〜300 の範囲に制限されている(`webapp/go/auctions.go` の `postAuction`)。出品フォームの入力もこの範囲に制限すること。

**注意:** 一覧・通知・フィードの3本は**いずれもオブジェクトで包まれている**。
`auctions` だけ剥がして `notifications` を剥がし忘れる、という取り違えをしないこと。
包まれていないのは `GET /api/auctions/:id`(詳細)・`GET /api/stats/me`・`GET /api/me`・
`POST /api/login` / `register` / `auctions` / `auctions/:id/bids` である。

- [ ] **Step 6: 認証コンテキストを書く**

`webapp/frontend/src/auth.tsx`:

```tsx
import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { api, ApiError, type User } from './api'

type AuthState = {
  user: User | null
  loading: boolean
  login: (name: string, password: string) => Promise<void>
  register: (name: string, password: string) => Promise<void>
}

const Ctx = createContext<AuthState | null>(null)

// セッションは httpOnly Cookie なので JS からは読めない。
// 起動時に GET /api/me を1回だけ叩いて身元を解決する。
export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api
      .me()
      .then(setUser)
      .catch((e) => {
        if (!(e instanceof ApiError && e.status === 401)) {
          console.error(e)
        }
        setUser(null)
      })
      .finally(() => setLoading(false))
  }, [])

  const value: AuthState = {
    user,
    loading,
    login: async (name, password) => setUser(await api.login(name, password)),
    register: async (name, password) => setUser(await api.register(name, password)),
  }
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useAuth(): AuthState {
  const v = useContext(Ctx)
  if (!v) throw new Error('useAuth は AuthProvider の内側でしか使えない')
  return v
}
```

- [ ] **Step 7: エントリポイントとスタイルを書く**

`webapp/frontend/src/main.tsx`。Task 5 で追加する5画面のルートも**この時点で書いておき**、まだ無いコンポーネントはこのタスクでは import しない。Task 5 がここへ足す。

```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Link, Route, Routes } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth'
import { Login } from './pages/Login'
import './styles.css'

function Header() {
  const { user } = useAuth()
  return (
    <header className="header">
      <Link to="/" className="brand">
        ISUBID
      </Link>
      <nav>
        <Link to="/">一覧</Link>
        <Link to="/sell">出品</Link>
        <Link to="/notifications">通知</Link>
        <Link to="/stats">売上</Link>
        {user ? <span className="me">{user.name}</span> : <Link to="/login">ログイン</Link>}
      </nav>
    </header>
  )
}

function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Header />
        <main className="main">
          <Routes>
            <Route path="/login" element={<Login />} />
          </Routes>
        </main>
      </AuthProvider>
    </BrowserRouter>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
```

`webapp/frontend/src/styles.css`(プレーンな1枚。CSS フレームワークを入れない):

```css
:root {
  --fg: #1b1b1b;
  --muted: #6b7280;
  --line: #e5e7eb;
  --accent: #1f4e79;
  --bg: #fafafa;
}
* { box-sizing: border-box; }
body {
  margin: 0;
  color: var(--fg);
  background: var(--bg);
  font-family: system-ui, -apple-system, "Hiragino Sans", "Noto Sans JP", sans-serif;
  line-height: 1.6;
}
a { color: var(--accent); text-decoration: none; }
a:hover { text-decoration: underline; }
.header {
  display: flex; align-items: center; gap: 1.5rem;
  padding: 0.75rem 1.25rem; background: #fff; border-bottom: 1px solid var(--line);
}
.brand { font-weight: 700; font-size: 1.1rem; }
.header nav { display: flex; gap: 1rem; align-items: center; margin-left: auto; }
.me { color: var(--muted); }
.main { max-width: 960px; margin: 0 auto; padding: 1.5rem 1.25rem; }
.card {
  background: #fff; border: 1px solid var(--line); border-radius: 8px;
  padding: 1rem; margin-bottom: 0.75rem;
}
.row { display: flex; gap: 1rem; align-items: baseline; }
.muted { color: var(--muted); font-size: 0.9rem; }
.price { font-weight: 700; font-variant-numeric: tabular-nums; }
table { width: 100%; border-collapse: collapse; }
th, td { text-align: left; padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--line); }
th { color: var(--muted); font-weight: 600; font-size: 0.85rem; }
input, select, textarea, button { font: inherit; }
input, select, textarea {
  width: 100%; padding: 0.45rem 0.6rem;
  border: 1px solid var(--line); border-radius: 6px; background: #fff;
}
button {
  padding: 0.45rem 1rem; border: 0; border-radius: 6px;
  background: var(--accent); color: #fff; cursor: pointer;
}
button:disabled { opacity: 0.5; cursor: default; }
.error { color: #b91c1c; margin: 0.5rem 0; }
.field { margin-bottom: 0.75rem; }
.field label { display: block; font-size: 0.85rem; color: var(--muted); margin-bottom: 0.2rem; }
.tabs { display: flex; gap: 0.5rem; margin-bottom: 1rem; }
.tabs button { background: #fff; color: var(--fg); border: 1px solid var(--line); }
.tabs button[aria-selected='true'] { background: var(--accent); color: #fff; }
```

- [ ] **Step 8: ログイン画面を書く**

`webapp/frontend/src/pages/Login.tsx`:

```tsx
import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../auth'

export function Login() {
  const { user, login, register } = useAuth()
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const navigate = useNavigate()

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      if (mode === 'login') await login(name, password)
      else await register(name, password)
      navigate('/')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (user) return <p>{user.name} としてログイン中です。</p>

  return (
    <div className="card" style={{ maxWidth: 420 }}>
      <div className="tabs" role="tablist">
        <button role="tab" aria-selected={mode === 'login'} onClick={() => setMode('login')}>
          ログイン
        </button>
        <button role="tab" aria-selected={mode === 'register'} onClick={() => setMode('register')}>
          新規登録
        </button>
      </div>
      <form onSubmit={onSubmit}>
        <div className="field">
          <label htmlFor="name">ユーザー名</label>
          <input id="name" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div className="field">
          <label htmlFor="password">パスワード</label>
          <input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </div>
        {error && <p className="error">{error}</p>}
        <button type="submit" disabled={busy}>
          {mode === 'login' ? 'ログイン' : '登録'}
        </button>
      </form>
    </div>
  )
}
```

- [ ] **Step 9: 型検査とビルドが通ることを確認する**

Run:
```bash
cd webapp/frontend && npm install && npm run build
```
Expected: 型エラーなし、`../public` に `index.html` / `assets/index-<hash>.js` / `assets/index-<hash>.css` / `favicon.svg` が出る

Run:
```bash
ls -la webapp/public webapp/public/assets
du -sk webapp/public
```
出力を報告に記録する。合計が 300KB を超えていたら依存を見直す(この段階で超えることは無いはずである)。

- [ ] **Step 10: 実際に画面が出ることを確認する**

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
curl -s http://localhost:8080/ | head -20
curl -s -o /dev/null -w '%{http_code} %{content_type}\n' "http://localhost:8080/$(cd webapp/public && ls assets/*.js | head -1)"
```
Expected: index.html に `<script type="module" src="/assets/index-<hash>.js">` と `<link rel="stylesheet" href="/assets/index-<hash>.css">` が含まれる。JS は `200 text/javascript; charset=utf-8`

- [ ] **Step 11: コミット**

`webapp/public/` はこの時点ではまだコミットしない(Task 6 で確定させる)。

```bash
git add .gitignore webapp/frontend
git commit -m "feat: SPA の土台(Vite/React/TS)とログイン画面を追加"
```

---
### Task 5: 残り5画面

**Files:**
- Create: `webapp/frontend/src/pages/{AuctionList.tsx,AuctionDetail.tsx,Sell.tsx,Notifications.tsx,Stats.tsx}`
- Modify: `webapp/frontend/src/main.tsx`

**Interfaces:**
- Consumes: Task 4 の `api`(`src/api.ts`)と `useAuth()`(`src/auth.tsx`)。HTTP を直接書かない
- Produces: 6画面すべてが揃った SPA

**この Task の書き方について:** 一覧と詳細は完全なコードを示す。残り3画面(出品/通知/売上)は
「何を取得し、何を描き、どの操作ができるか」を厳密に指定し、コードは実装者が
一覧・詳細と同じ書き方で埋める。これは手抜きではなく意図的な線引きである
— ベンチは JS を実行しないため、この3画面には検証可能な契約が API 呼び出し以外に無い。
JSX を1行単位で固定してもレビュアが検証できる対象が増えず、実装者の判断を
無意味に縛るだけになる。**API 呼び出しと入力値の制約は厳密に守ること。**

- [ ] **Step 1: 一覧画面を書く**

> **改訂(2026-08-31):** 4-B で一覧の契約が変わり、4-C でアイコンが実装されたので全面的に
> 書き換えた。元は次のようになっていた。
>
> - `api.auctions()` の戻りを**裸の配列**として `setItems` に渡していた。
>   現在の応答は `{auctions, total_count, has_next}` なので、そのままでは
>   `items.map` が落ちる
> - **ページ送りが無かった。** 一覧は20件で切られるため、これが無いと21件目以降に
>   到達する手段が画面に存在しない
> - `?page=` を `useSearchParams` に載せていなかった(`?q=` と `?category=` は載っていた)。
>   コンポーネント内の状態に留めると、ページを送っても URL が変わらず
>   リロードと「戻る」が壊れる(設計 §7.2)
> - **出品者アイコンを出していなかった**(4-C 未着地だったため)

`webapp/frontend/src/pages/AuctionList.tsx`:

```tsx
import { useEffect, useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, AUCTIONS_PER_PAGE, CATEGORIES, iconURL, type AuctionList as List } from '../api'

function remaining(endsAt: string): string {
  const ms = new Date(endsAt).getTime() - Date.now()
  if (ms <= 0) return '終了'
  const s = Math.floor(ms / 1000)
  if (s < 60) return `残り${s}秒`
  const m = Math.floor(s / 60)
  if (m < 60) return `残り${m}分`
  return `残り${Math.floor(m / 60)}時間`
}

// parsePage は ?page= を1以上の整数に正規化する。
// 不正値でサーバーに400を撃たせないよう、画面側で1へ丸める。
function parsePage(raw: string | null): number {
  const n = Number(raw)
  return Number.isInteger(n) && n >= 1 ? n : 1
}

export function AuctionList() {
  // 検索条件もページ番号も URL のクエリに置く。コンポーネントの state に留めると、
  // ページを送っても URL が変わらないためリロードと「戻る」が壊れ、
  // 共有リンクも同じ結果を再現しない(設計 §7.2)。
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const category = params.get('category') ?? ''
  const page = parsePage(params.get('page'))

  const [list, setList] = useState<List | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    setLoading(true)
    setError('')
    api
      .auctions({ page, q: q || undefined, category: category ? Number(category) : undefined })
      .then(setList)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false))
  }, [page, q, category])

  // 検索条件を変えたらページは1へ戻す(3ページ目のまま絞り込むと空振りするため)。
  function submitSearch(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const f = new FormData(e.currentTarget)
    const next = new URLSearchParams()
    const nq = String(f.get('q') ?? '')
    const nc = String(f.get('category') ?? '')
    if (nq) next.set('q', nq)
    if (nc) next.set('category', nc)
    setParams(next)
  }

  function goToPage(next: number) {
    const p = new URLSearchParams(params)
    if (next <= 1) p.delete('page')
    else p.set('page', String(next))
    setParams(p)
  }

  const items = list?.auctions ?? []
  const total = list?.total_count ?? 0
  const first = total === 0 ? 0 : (page - 1) * AUCTIONS_PER_PAGE + 1
  const last = (page - 1) * AUCTIONS_PER_PAGE + items.length

  return (
    <>
      <form className="row" style={{ marginBottom: '1rem' }} onSubmit={submitSearch}>
        <input name="q" defaultValue={q} placeholder="キーワードで検索" />
        <select name="category" defaultValue={category} style={{ width: 200 }}>
          <option value="">すべてのカテゴリ</option>
          {CATEGORIES.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
        <button type="submit">検索</button>
      </form>

      {error && <p className="error">{error}</p>}
      {loading && <p className="muted">読み込み中…</p>}
      {!loading && !error && (
        <p className="muted">
          {total === 0 ? '該当するオークションはありません。' : `全${total.toLocaleString()}件中 ${first}〜${last}件`}
        </p>
      )}

      {items.map((a) => (
        <div className="card" key={a.id}>
          <div className="row">
            <Link to={`/auctions/${a.id}`} style={{ fontWeight: 600 }}>
              {a.title}
            </Link>
            <span className="price">{a.current_price.toLocaleString()}円</span>
            <span className="muted">{a.bid_count}件の入札</span>
            <span className="muted" style={{ marginLeft: 'auto' }}>
              {remaining(a.ends_at)}
            </span>
          </div>
          <div className="row muted" style={{ gap: '0.4rem' }}>
            {/* アイコンは API ルート(/api/users/:id/icon)。未設定なら404が返るので
                alt を出さない空文字にして、壊れた画像アイコンだけを表示させる。 */}
            <img className="icon" src={iconURL(a.seller.id)} alt="" width={24} height={24} />
            <span>出品者: {a.seller.name}</span>
          </div>
        </div>
      ))}

      {!loading && total > 0 && (
        <div className="row" style={{ justifyContent: 'center', marginTop: '1rem' }}>
          <button type="button" onClick={() => goToPage(page - 1)} disabled={page <= 1}>
            前へ
          </button>
          <span className="muted">{page}ページ目</span>
          <button type="button" onClick={() => goToPage(page + 1)} disabled={!list?.has_next}>
            次へ
          </button>
        </div>
      )}
    </>
  )
}
```

`styles.css` に `.icon` を1つ足す(Task 4 Step 7 の CSS の末尾に追記):

```css
.icon { width: 24px; height: 24px; border-radius: 50%; object-fit: cover; background: var(--line); }
```

- [ ] **Step 2: 詳細画面を書く**

`webapp/frontend/src/pages/AuctionDetail.tsx`。入札フィードは1秒間隔のポーリングで、
`?since=<最後のbid_id>` を送る。これは Phase 3 でベンチが前提にしている形であり、
参加者が終盤に pub/sub 化する対象そのものなので、間隔と形を変えないこと。

```tsx
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { useParams } from 'react-router-dom'
import { api, type AuctionDetail as Detail, type Bid } from '../api'
import { useAuth } from '../auth'

const FEED_INTERVAL_MS = 1000

export function AuctionDetail() {
  const { id } = useParams()
  const auctionID = Number(id)
  const { user } = useAuth()
  const [detail, setDetail] = useState<Detail | null>(null)
  const [bids, setBids] = useState<Bid[]>([])
  const [amount, setAmount] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  // ポーリングのカーソル。詳細は created_at DESC, id DESC なので先頭が最大 id。
  const since = useRef(0)

  const load = useCallback(async () => {
    const d = await api.auction(auctionID)
    setDetail(d)
    setBids(d.bids)
    since.current = d.bids.length > 0 ? d.bids[0].id : 0
  }, [auctionID])

  useEffect(() => {
    load().catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [load])

  useEffect(() => {
    if (!detail || detail.status !== 'live') return
    const t = setInterval(() => {
      api
        .bidsSince(auctionID, since.current)
        .then((fresh) => {
          if (fresh.length === 0) return
          // フィードは id 昇順で返る。表示は新しい順なので反転して先頭に積む。
          since.current = fresh[fresh.length - 1].id
          setBids((prev) => [...fresh.slice().reverse(), ...prev])
        })
        .catch(() => {
          /* ポーリングの失敗は画面を壊さない */
        })
    }, FEED_INTERVAL_MS)
    return () => clearInterval(t)
  }, [auctionID, detail])

  async function onBid(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      await api.bid(auctionID, Number(amount))
      setAmount('')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (error && !detail) return <p className="error">{error}</p>
  if (!detail) return <p className="muted">読み込み中…</p>

  const current = bids.length > 0 ? bids[0].amount : detail.starting_price

  return (
    <>
      <div className="card">
        <h1 style={{ margin: '0 0 0.5rem' }}>{detail.title}</h1>
        <div className="row">
          <span className="price">{current.toLocaleString()}円</span>
          <span className="muted">{detail.status}</span>
          <span className="muted">出品者: {detail.seller.name}</span>
        </div>
        <p>{detail.description}</p>
        {detail.status === 'closed' && (
          <p className="muted">
            {detail.winner_id
              ? `落札: ${detail.winning_price?.toLocaleString()}円`
              : '入札がないまま終了しました'}
          </p>
        )}
      </div>

      {detail.status === 'live' && (
        <div className="card">
          {user ? (
            <form className="row" onSubmit={onBid}>
              <input
                type="number"
                min={current + 1}
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                placeholder={`${(current + 1).toLocaleString()}円以上`}
                required
              />
              <button type="submit" disabled={busy}>
                入札する
              </button>
            </form>
          ) : (
            <p className="muted">入札するにはログインしてください。</p>
          )}
          {error && <p className="error">{error}</p>}
        </div>
      )}

      <div className="card">
        <table>
          <thead>
            <tr>
              <th>入札者</th>
              <th>金額</th>
              <th>時刻</th>
            </tr>
          </thead>
          <tbody>
            {bids.map((b) => (
              <tr key={b.id}>
                <td>{b.user.name}</td>
                <td className="price">{b.amount.toLocaleString()}円</td>
                <td className="muted">{new Date(b.created_at).toLocaleTimeString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {bids.length === 0 && <p className="muted">まだ入札はありません。</p>}
      </div>
    </>
  )
}
```

- [ ] **Step 3: 出品画面を書く**

`webapp/frontend/src/pages/Sell.tsx`。以下を厳密に守ること。

- 未ログインなら「ログインしてください」とだけ出し、フォームを出さない(`useAuth()` の `user`)
- フォーム項目: タイトル(必須、1〜255文字)、説明(任意、textarea)、カテゴリ(`CATEGORIES` から select、必須)、開始価格(number、**1以上**)、公開時間(number、**10〜300秒**、既定60)
- 送信は `api.sell({ title, description, category_id, starting_price, duration_seconds })` の1回のみ
- 成功したら `useNavigate()` で `/auctions/<返ってきたid>` へ遷移する
- 失敗したら `.error` クラスの段落にメッセージを出し、フォームの入力は保持する

範囲の根拠は `webapp/go/auctions.go` の `postAuction` のバリデーションである
(`starting_price < 1` と `duration_seconds` の 10〜300 で 400 になる)。

- [ ] **Step 4: 通知画面を書く**

`webapp/frontend/src/pages/Notifications.tsx`。

- 未ログインなら「ログインしてください」とだけ出す
- マウント時に `api.notifications()` を1回呼ぶ。**ポーリングしない**(通知の追従はベンチが
  `GET /api/notifications` の単発呼び出しで検証しており、画面が余計な負荷を作る必要がない)
- 各通知を `.card` で描く: `message`、`type`、`created_at`、`auction_id` への `Link`
- 0件なら「通知はありません。」

- [ ] **Step 5: 売上画面を書く**

`webapp/frontend/src/pages/Stats.tsx`。

- 未ログインなら「ログインしてください」とだけ出す
- マウント時に `api.stats()` を1回呼ぶ
- 4つの数値を出す: `listed_count`(出品数)、`live_count`(開催中)、`sold_count`(落札成立)、
  `total_sales`(売上合計、円)
- 数値は `toLocaleString()` で桁区切りする

- [ ] **Step 6: ルーティングを完成させる**

`webapp/frontend/src/main.tsx` の `<Routes>` を差し替える。

```tsx
          <Routes>
            <Route path="/" element={<AuctionList />} />
            <Route path="/auctions/:id" element={<AuctionDetail />} />
            <Route path="/sell" element={<Sell />} />
            <Route path="/notifications" element={<Notifications />} />
            <Route path="/stats" element={<Stats />} />
            <Route path="/login" element={<Login />} />
            <Route path="*" element={<p className="muted">ページが見つかりません。</p>} />
          </Routes>
```

対応する import を上部に足す。

- [ ] **Step 7: ビルドして型検査を通す**

Run:
```bash
cd webapp/frontend && npm run build
du -sk ../public
```
Expected: 型エラーなし。`../public` の合計が 300KB 未満(実測値を報告に記録)

- [ ] **Step 8: 手で全画面を確認する**

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
curl -s -XPOST http://localhost:8080/api/initialize -o /dev/null -w '%{http_code}\n'
```
ブラウザで `http://localhost:8080/` を開き、以下を実際に確認して報告に書く。

1. 一覧が表示され、「全N件中 1〜20件」が出る
2. **「次へ」を押すと URL が `/?page=2` になり、別の20件が出る。**
   ブラウザの「戻る」で1ページ目に戻る。`/?page=2` を直接リロードしても同じ2ページ目が出る
3. **検索とカテゴリ絞り込みが効き、`?q=` / `?category=` が URL に載る。**
   絞り込むと `page` が落ちて1ページ目に戻る
4. **一覧に出品者アイコンが表示される**(生成データのユーザーはアイコンを持つ。
   シードユーザー(id 1〜20)は未設定なので 404 になり、画像が出ないのが正常)
5. 詳細を開くと入札履歴が出る
6. `seed_user_05` / `password` でログインできる
7. 入札すると履歴が増える
8. 出品すると詳細画面へ遷移する
9. **通知が表示される**(`{"notifications":[...]}` の剥がし忘れがあるとここで空になるか例外になる)。売上も表示される
10. `http://localhost:8080/auctions/1` を直接リロードしても 200 で画面が出る(SPAフォールバック)

ブラウザを使えない場合は、その旨を報告に明記し、`curl` で確認できる範囲(1・7と各 API の応答)だけを記録すること。**確認していない項目を「確認した」と書かないこと。**

- [ ] **Step 9: コミット**

```bash
git add webapp/frontend
git commit -m "feat: SPA の残り5画面(一覧/詳細/出品/通知/売上)を実装"
```

---

### Task 6: ビルド成果物のコミットとアセットマニフェスト

**Files:**
- Create: `bench/cmd/genmanifest/main.go`, `bench/assets.go`(マニフェストの型と埋め込みのみ), `bench/assets_test.go`, `bench/assets/manifest.json`
- Modify: `webapp/public/`(ビルド成果物をコミット), `README.md`

**Interfaces:**
- Consumes: Task 5 のビルド成果物
- Produces:
  - `type ManifestFile struct { Path, SHA256, ContentType string; Size int64 }`
  - `type Manifest struct { GeneratedAt time.Time; Files []ManifestFile }`
  - `func LoadEmbeddedManifest() (*Manifest, error)`

> **改訂(2026-08-31): 生成器・型・ドリフトテストの内容は変更なし。** 4-C(アイコン)が
> 着地したので、その関係だけ明記した。
>
> **ユーザーアイコンはマニフェストに載らないし、載せてはならない**(設計 §7.3)。
> 理由は2つあり、どちらも自動的に成立する:
>
> 1. マニフェストは `webapp/public` を歩いて作る。アイコンの実体は DB の LONGBLOB なので
>    そもそもディレクトリに存在しない
> 2. アイコンは `/api/users/{id}/icon` の API ルート(Task 1 で `/api` 配下)なので、
>    静的ハンドラにも `VerifyAssets` の照合対象にも到達しない
>
> したがって Step 4 で生成されるのは **`/index.html` / `/assets/*.js` / `/assets/*.css` /
> `/favicon.svg` の4件前後だけ**である。ここにアイコンが混ざっていたら、
> `webapp/public` に置いてはいけないファイルが紛れ込んでいる。
>
> **一覧画面の `<img src="/api/users/:id/icon">` はページロードでは取得されない**点にも
> 注意。ベンチは JS を実行しないので、`ProcessHTML` が見るのは `webapp/public/index.html`
> の静的なタグだけであり、SPA が描画するアイコンはそこに現れない。アイコンへの負荷は
> 4-C が `watcherIteration` に入れた `GetUserIcon` が担い続ける(`bench/load.go`)。

- [ ] **Step 1: マニフェスト生成器を書く**

`bench/cmd/genmanifest/main.go`:

```go
// genmanifest は webapp/public を歩き、ベンチが埋め込む正解ハッシュ表を出力する。
//
// フロントエンドを再ビルドしたら必ず実行すること:
//   cd bench && go run ./cmd/genmanifest -public ../webapp/public -out assets/manifest.json
// 実行し忘れは bench/assets_test.go の TestManifestMatchesPublicDir が検出する。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

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

// contentTypeOf は webapp/go/static.go の contentTypes と同じ規則である。
// 記録用であって照合基準ではない(ベンチは text/html か否かだけを見る)。
var contentTypeOf = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".svg":  "image/svg+xml",
	".ico":  "image/x-icon",
	".json": "application/json; charset=utf-8",
	".png":  "image/png",
	".webp": "image/webp",
}

func main() {
	publicDir := flag.String("public", "../webapp/public", "ビルド成果物のディレクトリ")
	out := flag.String("out", "assets/manifest.json", "出力先")
	flag.Parse()

	m, err := Build(*publicDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%d files -> %s\n", len(m.Files), *out)
}

// Build は dir を歩いてマニフェストを組み立てる。パスは URL パス(/ 始まり)で持つ。
func Build(dir string) (*Manifest, error) {
	var files []ManifestFile
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		files = append(files, ManifestFile{
			Path:        "/" + filepath.ToSlash(rel),
			SHA256:      hex.EncodeToString(sum[:]),
			Size:        int64(len(b)),
			ContentType: contentTypeOf[strings.ToLower(path.Ext(rel))],
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s にファイルが1つも無い(フロントエンドをビルドしたか?)", dir)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return &Manifest{GeneratedAt: time.Now().UTC().Truncate(time.Second), Files: files}, nil
}
```

- [ ] **Step 2: ベンチ側にマニフェストの型と埋め込みを書く**

`ManifestFile` / `Manifest` の定義は `cmd/genmanifest` 側と重複するが、これは避けられない。
`bench` はモジュールルートの `package main` なので、`cmd/genmanifest` から import できない。
共有パッケージを切るほどの規模でもないため重複を受け入れる。**両者の契約は JSON のタグであり、
それが食い違えば `TestManifestMatchesPublicDir` が落ちる**ので、実質的な守りはある。

`bench/assets.go`(このタスクでは型と読み込みだけ。検証は Task 7):

```go
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
```

- [ ] **Step 3: ドリフト検知テストを書く**

`bench/assets_test.go`:

```go
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
```

- [ ] **Step 4: ビルドしてマニフェストを生成する**

Run:
```bash
cd webapp/frontend && npm run build && cd ../..
cd bench && go run ./cmd/genmanifest -public ../webapp/public -out assets/manifest.json && cat assets/manifest.json
```
Expected: `index.html` / `assets/*.js` / `assets/*.css` / `favicon.svg` の4件前後が出る

- [ ] **Step 5: ドリフトテストが通ることを確認する**

Run: `cd bench && go test -count=1 -run TestManifestMatchesPublicDir ./...`
Expected: PASS

- [ ] **Step 6: ドリフトテストが実際に落ちることを確認する**

検出器が効いていることの実証。**必ず元に戻すこと。**

Run:
```bash
cd bench
printf '\n// drift\n' >> ../webapp/public/index.html
go test -count=1 -run TestManifestMatchesPublicDir ./... ; echo "exit=$?"
git checkout ../webapp/public/index.html
go test -count=1 -run TestManifestMatchesPublicDir ./...
```
Expected: 1回目は FAIL(sha256 不一致)、`git checkout` 後は PASS。両方の出力を報告に記録する。

なお `webapp/public/index.html` はこの時点で未コミットなので、`git checkout` では戻らない。
その場合は `cd webapp/frontend && npm run build` で作り直すこと。

- [ ] **Step 7: README を更新する**

「フロントエンドをビルドし直す手順」を追記する:

````markdown
### フロントエンドを変更したとき

```bash
cd webapp/frontend && npm install && npm run build   # -> webapp/public/
cd ../../bench && go run ./cmd/genmanifest -public ../webapp/public -out assets/manifest.json
go test ./...                                        # マニフェストのドリフト検知
```

`webapp/public/` と `bench/assets/manifest.json` はセットでコミットする。
片方だけ更新すると Prepare がアセットのハッシュ不一致で FAIL する。
````

- [ ] **Step 8: 最終確認とコミット**

Run:
```bash
cd bench && go test -count=1 ./... && go vet ./...
cd .. && gofmt -l bench
git status --short
```
Expected: 全 PASS、`gofmt -l` は無出力

```bash
git add webapp/public bench/assets bench/assets.go bench/assets_test.go bench/cmd README.md
git commit -m "feat: ビルド成果物をコミットしアセットマニフェストをベンチに埋め込む"
```

---
### Task 7: Prepare でアセットのハッシュを照合する

**Files:**
- Modify: `bench/client.go`, `bench/assets.go`, `bench/scenario.go`
- Create: `bench/assets_verify_test.go`

**Interfaces:**
- Consumes: Task 6 の `Manifest` / `LoadEmbeddedManifest()`
- Produces:
  - `type LoadedResource struct { Path string; Status int; Type string; Body []byte }`
  - `type PageLoad struct { IndexStatus int; IndexType string; IndexBody []byte; Resources map[string]LoadedResource }`
  - `func (c *Client) GetPage(ctx context.Context) (*PageLoad, error)`
  - `func VerifyAssets(m *Manifest, pl *PageLoad) error`(純粋関数。Task 8 も使う)

> **改訂(2026-08-31): コードの変更なし。** 現物と照合して次を確認済み。
>
> - `bench/client.go` は `bytes` / `io` / `net/http` / `fmt` を既に import している。
>   Step 1 の追加で新しい import は要らない
> - isucandar の API は Step 1 のコードのとおり(module cache 実物で確認):
>   `(*agent.Agent).GET(target string) (*http.Request, error)`、
>   `ProcessHTML(ctx, *http.Response, io.ReadCloser) (Resources, error)`、
>   `Resources = map[string]*Resource`、`Resource{InitiatorType, Request, Response, Error}`
> - `processHTMLLink` は `rel` が `stylesheet` / `icon` / `shortcut icon` /
>   `apple-touch-icon` / `manifest` / `modulepreload` のときだけ辿る。Vite が出す
>   `<link rel="icon" href="/favicon.svg">` と `<link rel="stylesheet">` は両方とも対象なので、
>   マニフェストの4件は `/index.html` を除いて全部 `ProcessHTML` から辿れる
> - Step 6 の挿入先で Prepare が持っているクライアント変数は `c` で正しい

- [ ] **Step 1: ページロードのクライアント処理を書く**

`bench/client.go` の末尾に追加する。`bytes` の import を足すこと(既にある)。

```go
// LoadedResource は取得済みのサブリソース1本。
type LoadedResource struct {
	Path   string
	Status int
	Type   string
	Body   []byte
}

// PageLoad は「ブラウザがサイトを1回開いた」結果。
type PageLoad struct {
	IndexStatus int
	IndexType   string
	IndexBody   []byte
	Resources   map[string]LoadedResource
}

// GetPage は / を取得し、HTML から辿れるサブリソース(script/stylesheet/icon/img)を
// すべて取得する。
//
// Body は必ずデコード後のバイト列になる。isucandar の agent が
// Content-Encoding を透過的に解凍し(agent/decompress.go)、304 のときは
// キャッシュ済みのボディを res.Body へ差し戻す(agent/cache.go の newCache)ため、
// 呼び出し側は圧縮とキャッシュの有無を意識しなくてよい。
func (c *Client) GetPage(ctx context.Context) (*PageLoad, error) {
	req, err := c.ag.GET("/")
	if err != nil {
		return nil, err
	}
	res, err := c.ag.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("GET /: 本文の読み取りに失敗: %w", err)
	}
	pl := &PageLoad{
		IndexStatus: res.StatusCode,
		IndexType:   res.Header.Get("Content-Type"),
		IndexBody:   body,
		Resources:   map[string]LoadedResource{},
	}
	// ProcessHTML は渡した body を読み切って閉じるので、読み終えた中身を包み直して渡す。
	resources, err := c.ag.ProcessHTML(ctx, res, io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return nil, fmt.Errorf("GET /: HTMLの解析に失敗: %w", err)
	}
	for _, r := range resources {
		if r.Request == nil {
			continue
		}
		p := r.Request.URL.Path
		if r.Error != nil {
			return nil, fmt.Errorf("GET %s: 取得に失敗: %w", p, r.Error)
		}
		if r.Response == nil {
			return nil, fmt.Errorf("GET %s: 応答が無い", p)
		}
		rb, err := io.ReadAll(r.Response.Body)
		r.Response.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("GET %s: 本文の読み取りに失敗: %w", p, err)
		}
		pl.Resources[p] = LoadedResource{
			Path:   p,
			Status: r.Response.StatusCode,
			Type:   r.Response.Header.Get("Content-Type"),
			Body:   rb,
		}
	}
	return pl, nil
}
```

- [ ] **Step 2: 検証の失敗するテストを書く**

`bench/assets_verify_test.go`。`VerifyAssets` は純粋関数なのでサーバー無しで表駆動にできる。

```go
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
```

- [ ] **Step 3: テストが落ちることを確認する**

Run: `cd bench && go test -count=1 -run TestVerifyAssets ./...`
Expected: コンパイルエラー(`VerifyAssets` が未定義)

- [ ] **Step 4: 検証を実装する**

`bench/assets.go` に追加する(`crypto/sha256`, `encoding/hex`, `net/http`, `strings` の import を足す):

```go
func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// VerifyAssets はページロードの結果をマニフェストと突き合わせる。
//
// 200 と 304 の両方を受理し、ハッシュはデコード後のバイト列で取る。
// これは「キャッシュを効かせる」「gzip を有効にする」という正しい改善を罰しないためである。
// Content-Type の照合を限定しているのも同じ理由で、nginx の既定 mime 表と
// アプリの自前表は application/javascript と text/javascript のように正当に食い違う。
// 一方「アセットが text/html で返る」は、存在しないファイルを SPA フォールバックが
// 飲み込んでいる状態そのものであり、実際にページが壊れるので検出する。
func VerifyAssets(m *Manifest, pl *PageLoad) error {
	if pl.IndexStatus != http.StatusOK && pl.IndexStatus != http.StatusNotModified {
		return fmt.Errorf("GET /: status %d (期待: 200 か 304)", pl.IndexStatus)
	}
	if pl.IndexStatus == http.StatusOK && !strings.HasPrefix(pl.IndexType, "text/html") {
		return fmt.Errorf("GET /: Content-Type が %q (期待: text/html で始まること)", pl.IndexType)
	}
	want, ok := m.ByPath("/index.html")
	if !ok {
		return fmt.Errorf("マニフェストに /index.html が無い(ベンチのビルドが壊れている)")
	}
	if got := sha256hex(pl.IndexBody); got != want.SHA256 {
		return fmt.Errorf("GET /: 中身がビルド成果物と異なる (sha256=%s, 期待=%s)", got, want.SHA256)
	}

	for _, f := range m.Files {
		if f.Path == "/index.html" {
			continue
		}
		r, ok := pl.Resources[f.Path]
		if !ok {
			return fmt.Errorf("%s が HTML から辿れない(script/link の参照が消えている)", f.Path)
		}
		if r.Status != http.StatusOK && r.Status != http.StatusNotModified {
			return fmt.Errorf("GET %s: status %d (期待: 200 か 304)", f.Path, r.Status)
		}
		if r.Status == http.StatusOK && strings.HasPrefix(r.Type, "text/html") {
			return fmt.Errorf("GET %s: Content-Type が text/html になっている"+
				"(存在しないアセットが index.html で代替されている疑い)", f.Path)
		}
		if got := sha256hex(r.Body); got != f.SHA256 {
			return fmt.Errorf("GET %s: 中身がビルド成果物と異なる (sha256=%s, 期待=%s)", f.Path, got, f.SHA256)
		}
	}
	return nil
}
```

- [ ] **Step 5: テストが通ることを確認する**

Run: `cd bench && go test -count=1 -run TestVerifyAssets ./...`
Expected: 全 PASS

- [ ] **Step 6: Prepare に組み込む**

`bench/scenario.go` の `Scenario` 構造体に `Assets *Manifest` を足し、`Prepare` の冒頭
(`Initialize()` の直後、他の検証より前)に以下を入れる。アセットが壊れていたら
以降の検証は意味を持たないので最初に見る。

```go
	// アセット検証。壊れたビルドを配信していたら以降の検証は意味を持たないので最初に見る。
	m, err := LoadEmbeddedManifest()
	if err != nil {
		return err
	}
	s.Assets = m
	pl, err := c.GetPage(ctx)
	if err != nil {
		return err
	}
	if err := VerifyAssets(m, pl); err != nil {
		return err
	}
```

挿入位置は、`lang == ""` のチェックの直後・`// 2. 初期データの検証` の手前である。
`c` は Prepare が既に持っているクライアント変数で、そのまま使える(確認済み)。

**時間予算に注意する。** `base := time.Now().UTC()` は `Initialize` の応答直後に採られ、
その後の `ValidateSnapshotAuctionDetail` はスナップショットの `status` と厳密比較するため、
**live サンプルが Prepare 中に期限を迎えて closed になると正しいアプリでも落ちる**
(最短は auction 1021 の +21秒。`bench/scenario.go` のコメント参照)。ここへ入れる
ページロードはその21秒を削る側に働く。バンドルは 300KB 未満・localhost 越しなので
実測では数十msに収まるはずだが、**Task 9 の G5(Prepare 3回連続・6秒基準)で
所要時間を必ず記録し、4-B / 4-C 時点の 1.79〜1.81秒からどれだけ増えたかを見ること。**
1秒以上増えているなら挿入位置(Prepare の末尾へ移す)を再検討する。

- [ ] **Step 7: 実スタックで Prepare を通す**

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
cd bench && go run . -target http://localhost:8080 -prepare-only
```
Expected: `PREPARE: PASS`

- [ ] **Step 8: コミット**

```bash
git add bench/client.go bench/assets.go bench/assets_verify_test.go bench/scenario.go
git commit -m "feat: Prepare でアセットのハッシュを照合する"
```

---

### Task 8: Load に訪問者 worker を足す

**Files:**
- Modify: `bench/score.go`, `bench/liveness.go`, `bench/liveness_test.go`, `bench/scenario.go`, `bench/load.go`, `bench/main.go`, `README.md`

**Interfaces:**
- Consumes: Task 7 の `GetPage` / `VerifyAssets` / `s.Assets`
- Produces: `ScoreGETPage` タグと `-visitors` フラグ。`workerCounts` に `Visitors` フィールド

> **改訂(2026-08-31): このタスクは4-E1 の着地で最も大きく変わった。** 元との差分:
>
> - **採点タグの登録先が3箇所になった。** 執筆時点ではスコア内訳が `bench/main.go` に
>   直書きされていたので `bench/score.go` だけを直せばよかった。4-E1 で
>   **`bench/liveness.go`** が新設され、`scoredTags`(内訳出力と liveness 判定が使う
>   順序付き一覧)と `livenessRequired`(そのタグに floor を課すかをワーカー数から決める)
>   への登録が必須になった。**片方を忘れると `bench` の単体テストが落ちる**
>   (`TestScoredTagsCoversScoreTable` / `TestLivenessRequiredCoversAllTags`)
> - **`workerCounts` に `Visitors` を足す必要が生じた。** これに伴い
>   `bench/liveness_test.go` の既存フィクスチャの更新も要る(Step 4)。ここを飛ばすと
>   実装が正しくてもテストが落ちる
> - **`Load` の worker 起動は `wg.Add(4)` ではなくなっていた。** 4-E1 が
>   「ワーカー数0以下なら `Process` を呼ばない」ガード(持ち越し23)を入れた際、
>   `start(n, w)` ヘルパー + `wg.Add(1)` の形に書き換わっている。
>   元の「`wg.Add(4)` を `wg.Add(5)` に変える」という手順は現物に存在しない
> - **`GetAuctions` のシグネチャが変わっていた**(4-B)。
>   `GetAuctions(ctx) ([]AuctionSummary, error)` ではなく
>   `GetAuctions(ctx, AuctionListParams) (*AuctionList, error)` である。
>   元の `visitorIteration` のコードはコンパイルが通らない

- [ ] **Step 1: スコアタグを足す**

`bench/score.go` の定数群の**末尾**に足す:

```go
	ScoreGETPage          score.ScoreTag = "GET / (ページロード)"
```

配点表の**末尾**に足す:

```go
	ScoreGETPage:          1,
```

**末尾に足す理由。** `bench/liveness.go` の `scoredTags` の並びがスコア内訳の出力順であり、
`checkLiveness` が返す「floor を下回った一覧」の順でもある。末尾に足せば既存7本の
出力順が動かず、`docs/phase4-notes.md` に残る 4-A〜4-E1 の実測ログと目で突き合わせられる。

配点表の直前のコメントを次に差し替える:

```go
// 配点(スペック準拠: 入札が主役)
//
// ScoreGETPage は「ページロード1回」につき1点であって、アセット1本につき1点ではない。
// アセット単位で加点すると、キャッシュを効かせて取得回数を減らすという正しい最適化が
// スコアを下げてしまう。ページロード単位なら「静的配信が速いほど1イテレーションが
// 短くなり、他のスコアが伸びる」という正しい向きだけが残る。
// (Phase 3 で ScoreGETFeed をポーリング1回ごとに加点し、遅いフィードほど高得点に
//  なりかけた失敗と同型なので、同じ轍を踏まないこと)
//
// タグを足したら bench/liveness.go の scoredTags と livenessRequired にも必ず足すこと。
```

**`errorPenalty` のコメント(同ファイル)は触らない。** 「スコア内訳7本の合計」という
記述は 4-B ゲート3 の実測1本(当時7本だった)の再計算過程であって、現在のタグ本数の
説明ではない。8本に書き換えると、参照している実測ログと合わなくなる。

- [ ] **Step 2: `bench/liveness.go` に登録する(2箇所)**

**このタスクで最も間違えやすい手順である。** 片方だけ足すと、`scoredTags` 側を忘れれば
内訳の合計が `raw` と一致しなくなり、`livenessRequired` 側を忘れれば新エンドポイントが
liveness 判定を黙ってすり抜ける。

(a) `scoredTags` の**末尾**に1行足す:

```go
var scoredTags = []scoredTag{
	{ScoreGETList, "GET /auctions"},
	{ScoreGETSearch, "GET /auctions (検索)"},
	{ScoreGETDetail, "GET /auctions/:id"},
	{ScorePOSTBid, "POST /auctions/:id/bids"},
	{ScoreGETFeed, "GET /auctions/:id/bids"},
	{ScoreGETNotifications, "GET /notifications"},
	{ScorePOSTAuction, "POST /auctions"},
	{ScoreGETPage, "GET / (ページロード)"},
}
```

(b) `workerCounts` に `Visitors` を足す:

```go
// workerCounts は Load を駆動するワーカー数。liveness floor の条件付けに使う。
type workerCounts struct {
	Bidders   int
	Watchers  int
	Notifiers int
	Sellers   int
	Visitors  int
}
```

(c) `livenessRequired` に `ScoreGETPage` を足し、**一覧と詳細の条件に visitor を加える**:

```go
func livenessRequired(tag score.ScoreTag, w workerCounts) bool {
	switch tag {
	case ScoreGETList, ScoreGETDetail:
		// bidder も watcher も visitor も、一覧を引いてから詳細を開く
		return w.Bidders > 0 || w.Watchers > 0 || w.Visitors > 0
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
	case ScoreGETPage:
		// ページロードを行うのは visitor だけ
		return w.Visitors > 0
	}
	return false
}
```

`ScoreGETList` / `ScoreGETDetail` の条件に `w.Visitors > 0` を足すのは、Step 5 の
`visitorIteration` がこの2本を実際に加点するからである。`livenessRequired` の条件は
「そのタグを叩くワーカーが1つでも生きているか」を表すという既存のコメントの契約
(`bench/load.go` の各 `*Iteration` が実際に呼ぶエンドポイントから導く)に従う。
これを足しておかないと、`-bidders 0 -watchers 0 -visitors 4` のデバッグ走行で
一覧と詳細に floor が課されなくなる。

- [ ] **Step 3: 登録漏れが実際に検出されることを確認する**

**登録漏れを2回わざと作り、テストが落ちることを目で見ること。** この2本のテストが
本タスクの安全網そのものであり、効いていることを実証してから先へ進む。

判定コマンドは共通:
```bash
cd bench && go test -count=1 -run 'TestScoredTagsCoversScoreTable|TestLivenessRequiredCoversAllTags' ./...
```

**実験1(`scoredTags` の足し忘れ)**: Step 1 だけを済ませ、Step 2 をまだ入れていない状態
(= `scoreTable` に8本、`scoredTags` に7本)で流す。
Expected: `TestScoredTagsCoversScoreTable` が
「scoredTags 7件 と scoreTable 8件 が不一致」で FAIL。

**実験2(`livenessRequired` の足し忘れ)**: Step 2 の (a) と (b) を入れ、
**(c) の `case ScoreGETPage:` だけを入れない**状態で流す((c) の他の変更は入れてよい)。
Expected: `TestLivenessRequiredCoversAllTags` が
「"GET / (ページロード)" に floor が課されていない」で FAIL。

((b) の `workerCounts.Visitors` を入れないと (c) がコンパイルできないので、
実験2 では (b) まで入れる。)

両方の出力を報告に記録し、そのうえで (a)(b)(c) をすべて入れた状態に戻す。

- [ ] **Step 4: `bench/liveness_test.go` のフィクスチャを更新する**

`workerCounts` にフィールドが増えたので、既存テストの**ワーカー全部入りリテラルに
`Visitors` を足さないと、実装が正しくてもテストが落ちる。**

(a) `TestLivenessRequiredCoversAllTags` の `all`:

```go
	all := workerCounts{Bidders: 1, Watchers: 1, Notifiers: 1, Sellers: 1, Visitors: 1}
```

(b) `TestLivenessRequiredRespectsWorkerCounts` に `-visitors 0` のケースを足す
(`noWatchers` のブロックの直後、`none` の手前):

```go
	noVisitors := workerCounts{Bidders: 1, Watchers: 1, Notifiers: 1, Sellers: 1, Visitors: 0}
	if livenessRequired(ScoreGETPage, noVisitors) {
		t.Error("visitors=0 なのに GET / (ページロード) に floor が課されている")
	}
	// 一覧と詳細は bidder / watcher も叩くので、visitor を止めても課され続ける
	if !livenessRequired(ScoreGETList, noVisitors) {
		t.Error("bidders=1 なのに GET /auctions に floor が課されていない")
	}

	onlyVisitors := workerCounts{Visitors: 1}
	if !livenessRequired(ScoreGETPage, onlyVisitors) {
		t.Error("visitors=1 なのに GET / (ページロード) に floor が課されていない")
	}
	if !livenessRequired(ScoreGETList, onlyVisitors) {
		t.Error("visitors=1 なのに GET /auctions に floor が課されていない")
	}
	if livenessRequired(ScorePOSTBid, onlyVisitors) {
		t.Error("bidders=0 なのに POST /auctions/:id/bids に floor が課されている")
	}
```

`noSellers` / `noBidders` / `noWatchers` の既存リテラルは `Visitors` を省いたまま
(= 0)でよい。それらが検査しているのは `ScoreGETPage` 以外のタグであり、
`ScoreGETList` / `ScoreGETDetail` は Bidders か Watchers で既に true になるため、
既存のアサーションはそのまま成立する。`none := workerCounts{}` のループも
`ScoreGETPage` が `Visitors == 0` で false を返すのでそのまま通る。

(c) `TestCheckLiveness` の `all` に `Visitors` を足し、`partial` を8本に増やす:

```go
	all := workerCounts{Bidders: 8, Watchers: 4, Notifiers: 2, Sellers: 2, Visitors: 2}
```

`all` に `Visitors: 2` を足さないと、`belowFloor` のケースで `ScoreGETPage` だけが
floor 判定の対象外になり、`len(dead) != len(scoredTags)` で FAIL する。

`partial`(「一覧経路が全滅し、通知と出品だけ生きている」形)にも `ScoreGETPage: 0` を
足す。ページロードは `GET /` と一覧の両方を含むので、一覧経路が全滅した走行では
ページロードも死んでいるのが自然である:

```go
	partial := map[score.ScoreTag]int64{
		ScoreGETList:          0,
		ScoreGETSearch:        0,
		ScoreGETDetail:        0,
		ScorePOSTBid:          0,
		ScoreGETFeed:          0,
		ScoreGETNotifications: 513,
		ScorePOSTAuction:      493,
		ScoreGETPage:          0,
	}
	dead := checkLiveness(partial, 6, all)
	if len(dead) != 6 {
		t.Fatalf("下回ったのが %d件, want 6件: %+v", len(dead), dead)
	}
	// 返る順序は scoredTags の順であること(出力の安定性のため)
	wantOrder := []score.ScoreTag{
		ScoreGETList, ScoreGETSearch, ScoreGETDetail, ScorePOSTBid, ScoreGETFeed, ScoreGETPage,
	}
```

`healthy` / `atFloor` / `belowFloor` / `zeroSeller` は `scoredTags` を回して作っているので、
新タグを自動的に含む。書き換え不要。

Run:
```bash
cd bench && go test -count=1 -run 'Liveness|ScoredTags' ./...
```
Expected: 全 PASS

- [ ] **Step 5: 訪問者シナリオを書く**

`bench/load.go` の末尾に追加:

```go
// visitorIteration は「サイトを訪れた閲覧者」を1人ぶん演じる。
// ページロード(HTML と全アセットの取得・照合)を1回行い、そのあとログインせずに
// 一覧と詳細を1つずつ見る。
//
// ログインしないのは意図的である。GET /api/auctions は未ログインでも見られるので、
// この worker が bcrypt(コスト12)を毎回踏むと、測っているものが静的配信ではなく
// ログイン処理になってしまう。
//
// イテレーションごとに新しい Client を作る = 毎回キャッシュが空の新規訪問者である。
// 参加者がキャッシュヘッダを付けても初回訪問は必ず実配信になるので、
// 静的配信の負荷が走行から消えることはない。
func (s *Scenario) visitorIteration(ctx context.Context, step *isucandar.BenchmarkStep) {
	c, err := NewClient(s.Target)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	pl, err := c.GetPage(ctx)
	if err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	// 注意: ここは ErrApplication であって ErrCritical ではない。
	// 静的配信はアプリプロセスと運命を共にするため、一過性の5xxで走行を即死
	// させてはいけない(設計文書のエラー分類の項を参照)。恒常的に壊れたビルドは
	// Prepare 側の VerifyAssets / liveness floor / エラー予算の三重で捕まる。
	if err := VerifyAssets(s.Assets, pl); err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	// ページロード1回につき1点。アセット1本ごとには加点しない(score.go のコメント参照)。
	step.AddScore(ScoreGETPage)

	// 入札者と同じく「終了が最も近い20件」= 1ページ目を見る。
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
		addErr(ctx, step, ErrCritical, fmt.Errorf("GET /api/auctions: 開催中オークションが0件"))
		return
	}
	if _, err := c.GetAuction(ctx, list[rand.Intn(len(list))].ID); err != nil {
		addErr(ctx, step, ErrApplication, err)
		return
	}
	step.AddScore(ScoreGETDetail)
}
```

新しい import は不要である(`fmt` / `math/rand` / `context` / `isucandar` は
`bench/load.go` に既にある)。

- [ ] **Step 6: worker を Load に足す**

`bench/scenario.go` の `Scenario` 構造体に `Visitors int` を足す(`Sellers` の直後)。

`Load` の中、`seller` worker を作った直後に足す:

```go
	visitor, err := worker.NewWorker(func(ctx context.Context, _ int) {
		s.visitorIteration(ctx, step)
	}, worker.WithInfinityLoop(), worker.WithMaxParallelism(int32(s.Visitors)))
	if err != nil {
		return err
	}
```

起動は `start` ヘルパー(4-E1 が入れた「ワーカー数0以下なら `Process` を呼ばない」ガード)を
使う。`start(s.Sellers, seller)` の直後に1行:

```go
	start(s.Visitors, visitor)
```

**`wg.Add(5)` のような書き方をしないこと。** `start` が内部で `wg.Add(1)` しており、
`n <= 0` のときは何もしない。ここを素の `go func(){ visitor.Process(ctx) }()` に戻すと、
`-visitors 0` が「ワーカーを止める」ではなく「無限ループ・無制限並列」になって
走行が全滅する(`docs/phase4-notes.md` 持ち越し23。isucandar の
`parallel.isLimitKept` が `limit < 1` を「上限なし」と解釈するため)。

`Load` 先頭のコメントを「4種の worker」から次に直す:

```go
// Load は入札者(bidderIteration)・ウォッチャー(watcherIteration)・
// 通知閲覧者(notifierIteration)・出品者(sellerIteration)・
// 訪問者(visitorIteration)の5種の worker を
// 無限ループで並行実行し、ctx(WithLoadTimeout)がキャンセルされるまで走らせる。
```

- [ ] **Step 7: フラグと `workerCounts` を足す**

`bench/main.go` のフラグの並びに(`sellers` の直後):
```go
	visitors := flag.Int("visitors", 2, "ページロードを行う閲覧者worker数")
```

`Scenario` の初期化に `Visitors: *visitors,` を足す。

**`workerCounts` の組み立てにも足す**(足し忘れると `livenessRequired(ScoreGETPage, ...)` が
常に false になり、ページロードが liveness 判定を黙ってすり抜ける。ここはテストが
守っていないので手で確認すること):

```go
	workers := workerCounts{
		Bidders: *bidders, Watchers: *watchers, Notifiers: *notifiers, Sellers: *sellers,
		Visitors: *visitors,
	}
```

既定を 2 にする根拠: 1イテレーションでバンドル全体(300KB未満)を1回転送する。
入札者8・ウォッチャー4に対して2なら、静的配信の負荷は走行に確実に乗るが支配的にはならない。
実際の重みは Task 9 の G4 で確認する。

- [ ] **Step 8: `s.Assets` が Load 開始時に必ず入っていることを確認する**

`Prepare` が `s.Assets` を設定する(Task 7 Step 6)。`-prepare-only` でない通常走行では
Prepare → Load の順に実行されるので nil にはならない。ただし将来 Prepare の順序が
変わったときに nil ポインタで落ちないよう、`Load` の冒頭(`s.PrepareOnly` の判定の直後)に
防御を1つ置く:

```go
	if s.Assets == nil {
		return fmt.Errorf("Load: アセットマニフェストが未ロード(Prepare が先に走っていない)")
	}
```

- [ ] **Step 9: 通常走行を確認する**

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
cd bench && go test -count=1 ./... && go vet ./...
go run . -target http://localhost:8080 -duration 60s -snapshot ../initial-data/out/snapshot.json
```
Expected:
- 単体テスト全 PASS
- breakdown が **8行**になり、末尾に `GET / (ページロード)` の行が出る
- **`LIVENESS: PASS (floor 6回、判定対象8/8本すべて到達)`**。`7/7` のままなら
  `livenessRequired` か `workerCounts` への `Visitors` の追加が漏れている
- 内訳の合計が `raw` と一致する(各行 `count × weight == points`、全行の合計 `== raw`)
- `RESULT: PASS`、critical 0件

実測値を報告に記録する。`GET / (ページロード)` の回数が floor(6回)に近い場合は、
`-visitors` の既定値かページロードの所要時間に問題があるので、そのまま Task 9 の
G4 へ持ち込まずに原因を調べること。

- [ ] **Step 10: `-visitors 0` で誤検知しないことを確認する**

`livenessRequired` の条件付けが効いていることの実証。

Run:
```bash
cd bench && go run . -target http://localhost:8080 -duration 30s -visitors 0 \
  -snapshot ../initial-data/out/snapshot.json | grep -E 'LIVENESS|RESULT|ページロード'
```
Expected: `GET / (ページロード)` が 0回でも `LIVENESS: PASS (floor 3回、判定対象7/8本すべて到達)`、
`RESULT: PASS`。ここが FAIL するなら `livenessRequired` の条件が間違っている。

- [ ] **Step 11: README にフラグを追記してコミット**

`README.md` のベンチのフラグ一覧に `-visitors`(既定2)を足す。
既存の記述(「worker 数を変える(既定: bidders 8 / watchers 4 / notifiers 2 / sellers 2)」)にも
`visitors 2` を加える。

```bash
git add bench README.md
git commit -m "feat: Load に訪問者workerを足し静的配信の負荷を走行に乗せる"
```

---

### Task 9: 受け入れゲートの実証と記録

**Files:**
- Modify: `docs/phase4-notes.md`, `dev/compose.yaml`

**Interfaces:**
- Consumes: Task 1〜8 のすべて

**このタスクは計測である。** 数値を作らず、観測したものだけを記録すること。
**`webapp/`・`bench/`・`webapp/frontend/` を変更しない**(ゲートのための一時的な破壊は必ず元に戻す)。

> **改訂(2026-08-31):** ゲート表を設計 §10 に合わせた。元との差分:
>
> - **G1 の判定手順が変わった(4-E1)。** 「採点対象すべてが0回でないことを目視で
>   明示的に確認する」という旧手順は、**`LIVENESS: PASS` の行を確認する**ことに
>   置き換わっている(`docs/phase4-notes.md` の「ゲート3の手順(4-E1 で差し替え)」)。
>   目視の脱落が 4-A の見逃しを生んだので、判定はベンチ側に持たせ、人間は出力行を読むだけにする
> - **G5(Prepare 3回連続、6秒基準)が増えた。** 元の計画には無かった
>
> | ゲート | 内容 | 期待 |
> |---|---|---|
> | G1 | 通常の 60 秒走行 | **`LIVENESS: PASS`**、`RESULT: PASS`、critical 0 件 |
> | G2 | `webapp/public/assets/*.js` を1バイト書き換える | Prepare が critical で FAIL |
> | G3 | `index.html` から `<script>` タグを消す | critical で FAIL |
> | G4 | nginx に `location /assets/ { root ...; gzip on; expires 1h; }` を足す | PASS のまま、**スコアが上がる** |
> | G5 | Prepare 3回連続 | `PREPARE: PASS`、6秒基準内 |

- [ ] **Step 1: G5 — Prepare 3回連続(6秒基準)**

番号は設計 §10 の表に合わせているが、**実行はこれを最初にする。** 最も安く、
Task 7 で Prepare に足したアセット検証が時間予算を壊していないかを先に確かめられるためである
(Task 7 Step 6 の「時間予算に注意する」を参照)。

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
cd bench && for i in 1 2 3; do
  /usr/bin/time -p go run . -target http://localhost:8080 -prepare-only
done
```
Expected: 3回とも `PREPARE: PASS`、`ERR:` 行なし、所要時間が**6秒基準内**。

3回分の所要時間をすべて記録し、**4-C 時点の実測 1.79〜1.81秒と比較する。**
1秒以上増えているなら、アセット検証の挿入位置を Prepare の末尾へ移すことを検討し、
その判断も記録する。6秒を超えたらこのゲートは FAIL であり、先へ進めてはならない。

- [ ] **Step 2: G1 — 通常走行**

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
cd bench && go run . -target http://localhost:8080 -duration 60s -snapshot ../initial-data/out/snapshot.json
```

判定は `docs/phase4-notes.md` の「ゲート3の手順(4-E1 で差し替え)」に従う。
**目視で「採点対象が0回でない」を数えない。**

1. **`LIVENESS: PASS` 行が出ていることを確認する。** 出ていなければ、続く行が
   floor を下回った採点対象を名指ししている
2. 判定対象の本数が **`8/8`** であることを確認する。`7/8` なら
   `livenessRequired` か `bench/main.go` の `workerCounts` への `Visitors` 追加が漏れている
   (Task 8 Step 2 / Step 7)
3. `RESULT: PASS` と `critical: 0件` を確認する
4. スコア内訳を検算する(各行 `count × weight == points`、全行の合計 `== raw`、
   `SCORE == raw − penalty`)。**内訳は8行**で、末尾が `GET / (ページロード)` である

`SCORE` と breakdown 全体をそのまま貼って記録する。

- [ ] **Step 3: G2 — アセットを1バイト壊すと FAIL する**

Run:
```bash
JS=$(cd webapp/public && ls assets/*.js | head -1)
cp "webapp/public/$JS" /tmp/isubid-asset-backup.js
printf '\n/* tamper */\n' >> "webapp/public/$JS"
docker compose -f dev/compose.yaml restart app
cd bench && go run . -target http://localhost:8080 -prepare-only ; echo "exit=$?"
```
Expected: `PREPARE: FAIL`。エラーに `中身がビルド成果物と異なる` が含まれる。**そのまま貼って記録する**

復元:
```bash
JS=$(cd webapp/public && ls assets/*.js | head -1)
cp /tmp/isubid-asset-backup.js "webapp/public/$JS"
docker compose -f dev/compose.yaml restart app
git diff --stat webapp/public   # 空であること
cd bench && go run . -target http://localhost:8080 -prepare-only
```
Expected: `git diff` が空、`PREPARE: PASS`

- [ ] **Step 4: G3 — `<script>` の参照を消すと FAIL する**

Run:
```bash
cp webapp/public/index.html /tmp/isubid-index-backup.html
perl -pi -e 's{<script[^>]*></script>}{}g' webapp/public/index.html
grep -c '<script' webapp/public/index.html   # 0 であること
docker compose -f dev/compose.yaml restart app
cd bench && go run . -target http://localhost:8080 -prepare-only ; echo "exit=$?"
```
Expected: `PREPARE: FAIL`。エラーに `HTML から辿れない` が含まれる

復元:
```bash
cp /tmp/isubid-index-backup.html webapp/public/index.html
docker compose -f dev/compose.yaml restart app
git diff --stat webapp/public   # 空であること
cd bench && go run . -target http://localhost:8080 -prepare-only
```
Expected: `git diff` が空、`PREPARE: PASS`

- [ ] **Step 5: G4 の前提を確認する — nginx から public が見えること**

Task 3 で `dev/compose.yaml` の `nginx` サービスに `../webapp/public:/webapp/public:ro` を
マウント済みのはずである。入っていなければここで足す(`dev/nginx.conf` は変更しない)。

Run:
```bash
docker compose -f dev/compose.yaml up -d
docker compose -f dev/compose.yaml exec nginx ls /webapp/public/assets
cd bench && go run . -target http://localhost:8080 -prepare-only
```
Expected: アセットが見える、`PREPARE: PASS`(挙動は変わっていない)

- [ ] **Step 6: G4 — 変更前のスコアを3回測る**

Run:
```bash
cd bench && for i in 1 2 3; do
  go run . -target http://localhost:8080 -duration 60s \
    -snapshot ../initial-data/out/snapshot.json | grep -E 'SCORE|RESULT|LIVENESS'
done
```
3回分の `SCORE` / `LIVENESS` / `RESULT` をすべて記録する。

- [ ] **Step 7: G4 — nginx 直配信に変えて3回測る**

`dev/nginx.conf` を一時的に次に差し替える(**計測後に必ず戻す**):

```
server {
    listen 80;

    gzip on;
    gzip_types text/css text/javascript application/javascript image/svg+xml;

    location /assets/ {
        root /webapp/public;
        expires 1h;
        access_log off;
    }

    location = /favicon.svg {
        root /webapp/public;
        expires 1h;
    }

    location / {
        proxy_pass http://app:8000;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

Run:
```bash
docker compose -f dev/compose.yaml restart nginx
curl -s -D- -o /dev/null -H 'Accept-Encoding: gzip' "http://localhost:8080/$(cd webapp/public && ls assets/*.js | head -1)" | head -12
cd bench && for i in 1 2 3; do
  go run . -target http://localhost:8080 -duration 60s \
    -snapshot ../initial-data/out/snapshot.json | grep -E 'SCORE|RESULT|LIVENESS'
done
```
Expected: アセットが `Content-Encoding: gzip` と `Expires` 付きで返る。3回とも
`LIVENESS: PASS` と `RESULT: PASS`
(**304 も gzip も受理する設計なので、ここで FAIL したらそれは設計の欠陥である。取り繕わず記録すること**)

`GET / (ページロード)` の回数が変更前より減っていないことも確認する。減っているなら、
304 の扱いか `VerifyAssets` の受理条件に問題がある。

- [ ] **Step 8: G4 の判定**

判定基準: **変更後3回の中央値が、変更前3回の(最大値 − 最小値)の幅を超えて改善していること。**
このマシンは同一セッション内でもスコアが約7〜12%振れるため、この幅を超えない改善は
ノイズと区別できない。6回分の生の数値をすべて記録し、判定の計算過程も書く。

上回らなかった場合は「改善がノイズに埋もれた」と正直に記録し、静的配信の重み
(`-visitors` の既定値、バンドルサイズ)が足りていない可能性として 4-E へ送る。
**数値を都合よく解釈しないこと。**

- [ ] **Step 9: nginx.conf を戻す**

Run:
```bash
git checkout dev/nginx.conf
docker compose -f dev/compose.yaml restart nginx
git diff --stat dev/nginx.conf   # 空であること
cd bench && go run . -target http://localhost:8080 -prepare-only
```
Expected: `git diff` が空、`PREPARE: PASS`

- [ ] **Step 10: `docs/phase4-notes.md` に 4-D の節を足す**

既存の内容の末尾に追記する(4-A の節は消さない)。`<...>` は実際に観測した値で埋める。

```markdown
## 4-D SPAフロントエンドとアセット追従検証

### 設計判断

- **API を `/api` 配下へ移した**: SPA のクライアントルート `/auctions/123` と
  API の `GET /auctions/:id` が同一パスになり、chi が API を先にマッチさせるため
  ディープリンクが JSON を返す。ISUCON11/12/13 が同じ理由で同じ形を採っている
- **静的配信をアプリ経由のままにした**: `dev/nginx.conf` は全部をアプリへ流す。
  ファイルは nginx コンテナからも見えるようにマウントしてあり(改善路を塞がないため)、
  設定だけが使っていない状態が正しい初期状態である
- **`ScoreGETPage` はページロード1回につき1点**: アセット1本ごとに加点すると、
  キャッシュを効かせて取得回数を減らす正しい最適化がスコアを下げる
- **アセット検証は 200 と 304 を両方受理し、ハッシュはデコード後のバイト列で取る**:
  gzip とキャッシュという正しい改善を罰しないため。Content-Type は
  「index.html が text/html」「アセットが text/html でない」だけを見る
  (`application/javascript` と `text/javascript` の食い違いで false-FAIL しないため)
- **マニフェストはベンチに `go:embed`**: 参加者が差し替えられないことが検証の前提
- **ユーザーアイコンはマニフェストの照合対象にしない**: 内容が DB 由来であり、
  `/api/users/:id/icon` は API ルートなので静的ハンドラにもマニフェスト
  (`webapp/public` を歩いて作る)にも載らない。4-C が「アイコンはコストであって
  報酬ではない」として採点タグを作っていないのと二重にならないようにした
- **`ScoreGETPage` を `scoredTags` と `livenessRequired` の両方に登録した**(4-E1 の契約):
  前者を忘れると内訳の合計が `raw` と一致しなくなり、後者を忘れるとページロードが
  liveness 判定を黙ってすり抜ける。`livenessRequired` では
  `ScoreGETList` / `ScoreGETDetail` の条件にも `Visitors > 0` を加えた
  (訪問者 worker がこの2本も加点するため)
- **一覧画面の検索条件とページ番号を `useSearchParams` で URL に載せた**:
  コンポーネント内の状態に留めるとリロードと「戻る」が壊れ、共有リンクが再現しない
- **カテゴリ3件はフロントにハードコードした**: `/initialize` が必ず同じ3件へ戻す固定値であり、
  `GET /api/categories` を足すとベンチ検証も足す必要が出る

### 受け入れゲートの実測(<日付>)

| ゲート | 内容 | 結果 |
|---|---|---|
| G1 | 通常の60秒走行 | <`LIVENESS: PASS` / `RESULT: PASS` / critical件数 / 判定対象8/8> |
| G2 | アセットを1バイト改変 | <...> |
| G3 | index.html から script を除去 | <...> |
| G4 | nginx 直配信 + gzip | <...> |
| G5 | Prepare 3回連続(6秒基準) | <3回分の所要時間。4-C 時点の 1.79〜1.81秒との比較> |

### G4 の生データ

変更前3回: <...>
変更後3回: <...>
判定: <計算過程>

### 4-E / 4-F への持ち越し

- **採点タグの表示名に `/api` を反映するかは 4-F で決める。** `bench/score.go` の
  タグ文字列と `bench/liveness.go` の `scoredTags` の表示名は本フェーズで触っていない。
  必ず対で直す必要があり、直すと本ファイルに残る 4-A〜4-E1 の実測ログとの文字列突合が
  壊れるため、レギュレーション文書を書くときに他の表記と一緒に判断する
- <その他、実測から出たもの>
```

- [ ] **Step 11: 最終確認**

Run:
```bash
cd webapp/go && go test -count=1 ./...
cd ../../bench && go test -count=1 ./... && go vet ./...
cd ../initial-data && go test -count=1 ./... && go vet ./...
cd .. && gofmt -l webapp/go bench initial-data
git status --short
```
Expected: すべて PASS、`gofmt -l` は無出力、作業ツリーは `docs/phase4-notes.md` 以外クリーン
(`dev/nginx.conf` が Step 9 で戻っていること、`webapp/public` に差分が無いことを特に確認する)

`bench` の単体テストには `TestScoredTagsCoversScoreTable` /
`TestLivenessRequiredCoversAllTags` / `TestCheckLiveness` /
`TestManifestMatchesPublicDir` / `TestVerifyAssets*` が含まれる。
ここが全部 PASS していることが、Task 8 の2箇所登録と Task 6 のマニフェスト同期の
最終確認になっている。

- [ ] **Step 12: コミット**

```bash
git add docs/phase4-notes.md
git commit -m "docs: 4-D の設計判断と受け入れゲートの実測を記録"
```
