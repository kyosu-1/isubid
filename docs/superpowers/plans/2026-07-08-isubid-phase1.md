# ISUBID Phase 1(骨格疎通)実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ISUBID(ライブオークション)の最小Go参照実装 + Docker Compose環境 + Prepareフェーズのみのベンチマーカーを疎通させ、「ベンチが整合性チェッカーとして動く」状態を作る。

**Architecture:** モノレポ。`webapp/go`(参照実装、わざと遅い初期実装)、`webapp/sql`(スキーマ+Phase 1用小型シード)、`dev/`(compose: nginx→app→mysql)、`bench/`(isucandarベース、Prepareのみ)。アプリとベンチは独立したGoモジュール。

**Tech Stack:** Go 1.24+, chi v5, sqlx, go-sql-driver/mysql, gorilla/sessions, bcrypt, MySQL 8, nginx, isucandar

## Global Constraints

- リポジトリルート: `/Users/abe/ghq/github.com/kyosu-1/isubid`(以下、パスはすべてルート相対)
- Goモジュール名: アプリ=`github.com/kyosu-1/isubid/webapp/go`、ベンチ=`github.com/kyosu-1/isubid/bench`
- **意図的なボトルネックは最適化しないこと**(N+1、bcryptコスト12、`SELECT MAX()`全件走査、FOR UPDATEでの直列化、インデックス欠如)。これらは出題の仕込みであり、コメント `// 意図的に遅い実装` を付けて保護する
- スキーマにはPK・`users.name` のUNIQUE以外のインデックスを張らない(仕込み)
- DB接続env: `ISUBID_DB_HOST`(default `127.0.0.1`), `ISUBID_DB_PORT`(`3306`), `ISUBID_DB_USER`(`isucon`), `ISUBID_DB_PASSWORD`(`isucon`), `ISUBID_DB_NAME`(`isubid`)
- アプリlisten: `ISUBID_PORT`(default `8000`)。nginxはホスト `:8080` で受ける
- 日時: MySQLは `DATETIME(6)`、DSNに `parseTime=true&loc=UTC`、JSONは `time.Time` デフォルト(RFC3339)
- APIのエラーレスポンスは常に `{"error": "<message>"}`
- アプリのテストは `dev/compose.yaml` のmysqlが起動している前提(`docker compose -f dev/compose.yaml up -d mysql`)。テストはアプリと同じDB `isubid` を使い、各テストが `POST /initialize` で状態をリセットする
- コミットメッセージ末尾に `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: スキーマ・シードデータ・MySQL compose

**Files:**
- Create: `webapp/sql/00_schema.sql`
- Create: `webapp/sql/90_seed_phase1.sql`
- Create: `dev/compose.yaml`(mysqlサービスのみ。app/nginxはTask 6で追記)
- Create: `.gitignore`

**Interfaces:**
- Produces: DB `isubid` のスキーマ(users/categories/auctions/bids/notifications)とPhase 1シード。シードの正解値(後続タスクとベンチが依存): liveオークション10件、auction id=1 は title=`ヘリテージ・ウィングチェア`・starting_price=1000・入札3件(1000/1200/1500)・現在価格1500、auction id=2〜4 は入札1件ずつ(それぞれ 2100, 3100, 4100)、id=5〜10 は入札0件で現在価格=starting_price(500×id)。全シードユーザーのパスワードは `password`

- [ ] **Step 1: .gitignore を作成**

```gitignore
*.test
/bench/bench
/webapp/go/isubid
.DS_Store
```

- [ ] **Step 2: スキーマを作成**

`webapp/sql/00_schema.sql`:

```sql
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS bids;
DROP TABLE IF EXISTS auctions;
DROP TABLE IF EXISTS categories;
DROP TABLE IF EXISTS users;

CREATE TABLE users (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(64) NOT NULL UNIQUE,
  password_hash VARCHAR(255) NOT NULL,
  icon LONGBLOB,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE categories (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(64) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE auctions (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  seller_id BIGINT NOT NULL,
  category_id BIGINT NOT NULL,
  title VARCHAR(255) NOT NULL,
  description TEXT NOT NULL,
  starting_price BIGINT NOT NULL,
  starts_at DATETIME(6) NOT NULL,
  ends_at DATETIME(6) NOT NULL,
  status ENUM('upcoming','live','closed') NOT NULL DEFAULT 'upcoming',
  winner_id BIGINT NULL,
  winning_price BIGINT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE bids (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  auction_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  amount BIGINT NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE notifications (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT NOT NULL,
  type VARCHAR(32) NOT NULL,
  auction_id BIGINT NOT NULL,
  message VARCHAR(255) NOT NULL,
  is_read TINYINT(1) NOT NULL DEFAULT 0,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

意図: PKと `users.name` UNIQUE以外のインデックスなし(仕込み)。notificationsはPhase 3で使うが、スキーマ変更の手戻りを避けるため今定義する。

- [ ] **Step 3: シードユーザー用bcryptハッシュを生成**

一時ディレクトリ(セッションのscratchpad)で以下を実行し、出力ハッシュを控える:

```bash
cd $(mktemp -d) && cat > main.go <<'EOF'
package main

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	h, err := bcrypt.GenerateFromPassword([]byte("password"), 12)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(h))
}
EOF
go mod init tmphash && go get golang.org/x/crypto/bcrypt && go run main.go
```

Expected: `$2a$12$` で始まるハッシュが1行出力される。

- [ ] **Step 4: シードSQLを作成**

`webapp/sql/90_seed_phase1.sql`(`<BCRYPT_HASH>` はStep 3の出力で置換すること):

```sql
INSERT INTO categories (id, name) VALUES
  (1, 'オフィスチェア'),
  (2, 'ゲーミングチェア'),
  (3, 'アンティーク');

-- 全ユーザーのパスワードは 'password'(bcrypt cost 12)
INSERT INTO users (id, name, password_hash) VALUES
  (1,  'seed_user_01', '<BCRYPT_HASH>'),
  (2,  'seed_user_02', '<BCRYPT_HASH>'),
  (3,  'seed_user_03', '<BCRYPT_HASH>'),
  (4,  'seed_user_04', '<BCRYPT_HASH>'),
  (5,  'seed_user_05', '<BCRYPT_HASH>'),
  (6,  'seed_user_06', '<BCRYPT_HASH>'),
  (7,  'seed_user_07', '<BCRYPT_HASH>'),
  (8,  'seed_user_08', '<BCRYPT_HASH>'),
  (9,  'seed_user_09', '<BCRYPT_HASH>'),
  (10, 'seed_user_10', '<BCRYPT_HASH>'),
  (11, 'seed_user_11', '<BCRYPT_HASH>'),
  (12, 'seed_user_12', '<BCRYPT_HASH>'),
  (13, 'seed_user_13', '<BCRYPT_HASH>'),
  (14, 'seed_user_14', '<BCRYPT_HASH>'),
  (15, 'seed_user_15', '<BCRYPT_HASH>'),
  (16, 'seed_user_16', '<BCRYPT_HASH>'),
  (17, 'seed_user_17', '<BCRYPT_HASH>'),
  (18, 'seed_user_18', '<BCRYPT_HASH>'),
  (19, 'seed_user_19', '<BCRYPT_HASH>'),
  (20, 'seed_user_20', '<BCRYPT_HASH>');

-- liveオークション10件。ends_atは遠い未来(Phase 1はPrepareのみで時間進行なし)
INSERT INTO auctions (id, seller_id, category_id, title, description, starting_price, starts_at, ends_at, status) VALUES
  (1,  1, 3, 'ヘリテージ・ウィングチェア',       '英国アンティークの本革ウィングチェア', 1000, '2026-01-01 00:00:00', '2030-01-01 00:00:00', 'live'),
  (2,  2, 1, 'エルゴホスト Model E',             '長時間作業向けエルゴノミクスチェア',   2000, '2026-01-01 00:00:00', '2030-01-01 00:00:00', 'live'),
  (3,  3, 2, 'ISUレーサー GT',                   'フルバケット型ゲーミングチェア',       3000, '2026-01-01 00:00:00', '2030-01-01 00:00:00', 'live'),
  (4,  4, 1, 'メッシュフロー 40',                '通気性メッシュのタスクチェア',         4000, '2026-01-01 00:00:00', '2030-01-01 00:00:00', 'live'),
  (5,  5, 3, 'ミッドセンチュリー・ラウンジ',     '1960年代のラウンジチェア',             2500, '2026-01-01 00:00:00', '2030-01-01 00:00:00', 'live'),
  (6,  6, 2, 'ネオンストライク Z',               'RGBライト内蔵ゲーミングチェア',        3000, '2026-01-01 00:00:00', '2030-01-01 00:00:00', 'live'),
  (7,  7, 1, 'スタンドフレックス',               '昇降デスク対応ハイチェア',             3500, '2026-01-01 00:00:00', '2030-01-01 00:00:00', 'live'),
  (8,  8, 3, 'チャーチチェア 1920',              '教会で使われていた木製チェア',         4000, '2026-01-01 00:00:00', '2030-01-01 00:00:00', 'live'),
  (9,  9, 2, 'プロシート・エディション',         'eスポーツチーム監修モデル',            4500, '2026-01-01 00:00:00', '2030-01-01 00:00:00', 'live'),
  (10, 10, 1, 'コンパクトワーク 01',             '省スペース設計のワークチェア',         5000, '2026-01-01 00:00:00', '2030-01-01 00:00:00', 'live');

-- auction 1: 3件(現在価格1500) / auction 2〜4: 1件ずつ / 5〜10: 0件
INSERT INTO bids (id, auction_id, user_id, amount, created_at) VALUES
  (1, 1, 2, 1000, '2026-07-01 00:00:00'),
  (2, 1, 3, 1200, '2026-07-01 01:00:00'),
  (3, 1, 4, 1500, '2026-07-01 02:00:00'),
  (4, 2, 5, 2100, '2026-07-01 03:00:00'),
  (5, 3, 6, 3100, '2026-07-01 04:00:00'),
  (6, 4, 7, 4100, '2026-07-01 05:00:00');
```

注: ベンチの初期データ期待値は本ファイルを正とする(bench/validate.go の期待値テーブルと一致させること)。

- [ ] **Step 5: composeファイル(mysqlのみ)を作成**

`dev/compose.yaml`:

```yaml
services:
  mysql:
    image: mysql:8
    environment:
      MYSQL_ROOT_PASSWORD: isucon
      MYSQL_DATABASE: isubid
      MYSQL_USER: isucon
      MYSQL_PASSWORD: isucon
    ports:
      - "3306:3306"
    volumes:
      - mysql-data:/var/lib/mysql
    healthcheck:
      test: ["CMD", "mysqladmin", "ping", "-h", "127.0.0.1", "-uisucon", "-pisucon"]
      interval: 2s
      timeout: 2s
      retries: 30

volumes:
  mysql-data:
```

- [ ] **Step 6: mysqlを起動しスキーマ+シードを適用して検証**

```bash
docker compose -f dev/compose.yaml up -d mysql
# healthyになるまで待つ
until docker compose -f dev/compose.yaml exec mysql mysqladmin ping -h 127.0.0.1 -uisucon -pisucon --silent; do sleep 2; done
docker compose -f dev/compose.yaml exec -T mysql mysql -uisucon -pisucon isubid < webapp/sql/00_schema.sql
docker compose -f dev/compose.yaml exec -T mysql mysql -uisucon -pisucon isubid < webapp/sql/90_seed_phase1.sql
docker compose -f dev/compose.yaml exec -T mysql mysql -uisucon -pisucon isubid -e "SELECT COUNT(*) FROM auctions WHERE status='live'; SELECT MAX(amount) FROM bids WHERE auction_id=1;"
```

Expected: `COUNT(*)` = 10、`MAX(amount)` = 1500。

- [ ] **Step 7: Commit**

```bash
git add .gitignore webapp/sql dev/compose.yaml
git commit -m "feat: DBスキーマとPhase 1シードデータ、MySQL compose環境を追加"
```

---

### Task 2: Goアプリ骨格と POST /initialize

**Files:**
- Create: `webapp/go/go.mod`(`go mod init github.com/kyosu-1/isubid/webapp/go`)
- Create: `webapp/go/main.go`
- Create: `webapp/go/db.go`
- Create: `webapp/go/initialize.go`
- Test: `webapp/go/initialize_test.go`

**Interfaces:**
- Consumes: Task 1のDB(compose mysql起動済み)と `webapp/sql/*.sql`
- Produces:
  - `newRouter(db *sqlx.DB) http.Handler` — 全ハンドラ登録済みルーター(後続タスクはここにルートを足す)
  - `writeJSON(w http.ResponseWriter, code int, v any)` / `writeError(w http.ResponseWriter, code int, msg string)`
  - `getEnv(key, def string) string` / `connectDB() (*sqlx.DB, error)` / `dbDSN(multiStatements bool) string`
  - `handler` 構造体(フィールド `db *sqlx.DB`)。ハンドラはすべて `(h *handler)` メソッド
  - API: `POST /initialize` → 200 `{"lang":"go"}`(schema+seedを再適用)

- [ ] **Step 1: モジュール初期化と依存取得**

```bash
cd webapp/go
go mod init github.com/kyosu-1/isubid/webapp/go
go get github.com/go-chi/chi/v5 github.com/jmoiron/sqlx github.com/go-sql-driver/mysql github.com/gorilla/sessions golang.org/x/crypto/bcrypt
```

- [ ] **Step 2: 失敗するテストを書く**

`webapp/go/initialize_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer はテスト用サーバーを起動する。compose の mysql が起動している前提。
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := connectDB()
	if err != nil {
		t.Fatalf("connectDB: %v (dev/compose.yaml の mysql は起動していますか?)", err)
	}
	t.Cleanup(func() { db.Close() })
	ts := httptest.NewServer(newRouter(db))
	t.Cleanup(ts.Close)
	return ts
}

// initApp は POST /initialize でDBを初期状態に戻す。
func initApp(t *testing.T, ts *httptest.Server) {
	t.Helper()
	res, err := http.Post(ts.URL+"/initialize", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST /initialize: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST /initialize status = %d, want 200", res.StatusCode)
	}
}

func TestInitialize(t *testing.T) {
	ts := newTestServer(t)
	res, err := http.Post(ts.URL+"/initialize", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body struct {
		Lang string `json:"lang"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Lang != "go" {
		t.Errorf("lang = %q, want %q", body.Lang, "go")
	}
}
```

- [ ] **Step 3: テストが失敗することを確認**

Run: `cd webapp/go && go test ./... -run TestInitialize -v`
Expected: コンパイルエラー(`newRouter` 未定義)。

- [ ] **Step 4: 実装を書く**

`webapp/go/db.go`:

```go
package main

import (
	"fmt"
	"os"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func dbDSN(multiStatements bool) string {
	user := getEnv("ISUBID_DB_USER", "isucon")
	pass := getEnv("ISUBID_DB_PASSWORD", "isucon")
	host := getEnv("ISUBID_DB_HOST", "127.0.0.1")
	port := getEnv("ISUBID_DB_PORT", "3306")
	name := getEnv("ISUBID_DB_NAME", "isubid")
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=UTC&multiStatements=%t",
		user, pass, host, port, name, multiStatements)
}

func connectDB() (*sqlx.DB, error) {
	db, err := sqlx.Open("mysql", dbDSN(false))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
```

`webapp/go/main.go`:

```go
package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"
)

type handler struct {
	db *sqlx.DB
}

func newRouter(db *sqlx.DB) http.Handler {
	h := &handler{db: db}
	r := chi.NewRouter()
	r.Post("/initialize", h.postInitialize)
	return r
}

func main() {
	db, err := connectDB()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	addr := ":" + getEnv("ISUBID_PORT", "8000")
	log.Printf("isubid listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, newRouter(db)))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
```

`webapp/go/initialize.go`:

```go
package main

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/jmoiron/sqlx"
)

var initSQLFiles = []string{"00_schema.sql", "90_seed_phase1.sql"}

func (h *handler) postInitialize(w http.ResponseWriter, r *http.Request) {
	sqlDir := getEnv("ISUBID_SQL_DIR", "../sql")
	db, err := sqlx.Open("mysql", dbDSN(true))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer db.Close()
	for _, f := range initSQLFiles {
		b, err := os.ReadFile(filepath.Join(sqlDir, f))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := db.ExecContext(r.Context(), string(b)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"lang": "go"})
}
```

注(スペックからの軽微な逸脱): 本家は `init.sh` をシェル実行するが、mysqlクライアントへの依存を避けるためGoから直接SQLを適用する。`init.sh` はPhase 2の初期データジェネレータ導入時に必要なら追加する。

- [ ] **Step 5: テストが通ることを確認**

Run: `cd webapp/go && go test ./... -run TestInitialize -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add webapp/go
git commit -m "feat: Goアプリ骨格と POST /initialize を実装"
```

---

### Task 3: 認証(register / login / セッション)

**Files:**
- Create: `webapp/go/session.go`
- Create: `webapp/go/auth.go`
- Modify: `webapp/go/main.go`(ルート追加)
- Test: `webapp/go/auth_test.go`

**Interfaces:**
- Consumes: `newRouter` / `handler` / `writeJSON` / `writeError` / `initApp`(Task 2)
- Produces:
  - `setLogin(w http.ResponseWriter, r *http.Request, userID int64) error`
  - `currentUserID(r *http.Request) (int64, bool)` — 後続の入札APIが使う
  - `userResponse` 構造体(`ID int64 \"json:id\"`, `Name string \"json:name\"`)
  - API: `POST /register` `{name, password}` → 201 `{id, name}` +セッションCookie / name重複は409
  - API: `POST /login` `{name, password}` → 200 `{id, name}` / 失敗は401

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/auth_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

func postJSON(t *testing.T, client *http.Client, url, body string) *http.Response {
	t.Helper()
	res, err := client.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func newClientWithJar(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func TestRegisterAndLogin(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := newClientWithJar(t)

	res := postJSON(t, client, ts.URL+"/register", `{"name":"alice","password":"secretpw"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want 201", res.StatusCode)
	}
	var u struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(res.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.Name != "alice" || u.ID == 0 {
		t.Errorf("unexpected user: %+v", u)
	}

	res2 := postJSON(t, client, ts.URL+"/login", `{"name":"alice","password":"secretpw"}`)
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", res2.StatusCode)
	}
}

func TestRegisterDuplicateName(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := newClientWithJar(t)

	res := postJSON(t, client, ts.URL+"/register", `{"name":"bob","password":"secretpw"}`)
	res.Body.Close()
	res2 := postJSON(t, client, ts.URL+"/register", `{"name":"bob","password":"secretpw"}`)
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate register status = %d, want 409", res2.StatusCode)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := newClientWithJar(t)

	res := postJSON(t, client, ts.URL+"/login", `{"name":"seed_user_01","password":"wrong"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want 401", res.StatusCode)
	}
}

func TestLoginSeedUser(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := newClientWithJar(t)

	// シードユーザーのパスワードは全員 'password'(90_seed_phase1.sql)
	res := postJSON(t, client, ts.URL+"/login", `{"name":"seed_user_01","password":"password"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body := make([]byte, 256)
		n, _ := res.Body.Read(body)
		t.Fatalf("seed login status = %d, want 200 (body: %s)", res.StatusCode, string(body[:n]))
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd webapp/go && go test ./... -run 'TestRegister|TestLogin' -v`
Expected: 404(ルート未登録)によるFAIL、またはコンパイルエラー。

- [ ] **Step 3: 実装を書く**

`webapp/go/session.go`:

```go
package main

import (
	"net/http"

	"github.com/gorilla/sessions"
)

const sessionName = "isubid_session"

var store = sessions.NewCookieStore([]byte(getEnv("ISUBID_SESSION_SECRET", "isubid-secret")))

func setLogin(w http.ResponseWriter, r *http.Request, userID int64) error {
	sess, _ := store.Get(r, sessionName)
	sess.Values["user_id"] = userID
	return sess.Save(r, w)
}

func currentUserID(r *http.Request) (int64, bool) {
	sess, err := store.Get(r, sessionName)
	if err != nil {
		return 0, false
	}
	v, ok := sess.Values["user_id"].(int64)
	return v, ok
}
```

`webapp/go/auth.go`:

```go
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12 // 意図的に遅い実装: コスト過剰

type authRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type userResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (h *handler) postRegister(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := h.db.ExecContext(r.Context(),
		"INSERT INTO users (name, password_hash) VALUES (?, ?)", req.Name, string(hash))
	if err != nil {
		writeError(w, http.StatusConflict, "name already taken")
		return
	}
	id, _ := res.LastInsertId()
	if err := setLogin(w, r, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, userResponse{ID: id, Name: req.Name})
}

func (h *handler) postLogin(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var u struct {
		ID           int64  `db:"id"`
		Name         string `db:"name"`
		PasswordHash string `db:"password_hash"`
	}
	err := h.db.GetContext(r.Context(), &u,
		"SELECT id, name, password_hash FROM users WHERE name = ?", req.Name)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "invalid name or password")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "invalid name or password")
		return
	}
	if err := setLogin(w, r, u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, userResponse{ID: u.ID, Name: u.Name})
}
```

`webapp/go/main.go` の `newRouter` にルートを追加:

```go
	r.Post("/initialize", h.postInitialize)
	r.Post("/register", h.postRegister)
	r.Post("/login", h.postLogin)
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd webapp/go && go test ./... -run 'TestRegister|TestLogin|TestInitialize' -v`
Expected: 全PASS(bcryptコスト12なので数秒かかる)。

- [ ] **Step 5: Commit**

```bash
git add webapp/go
git commit -m "feat: 認証(register/login/セッション)を実装"
```

---

### Task 4: オークション一覧・詳細

**Files:**
- Create: `webapp/go/auctions.go`
- Modify: `webapp/go/main.go`(ルート追加)
- Test: `webapp/go/auctions_test.go`

**Interfaces:**
- Consumes: `handler` / `userResponse` / `initApp` / シード期待値(Task 1)
- Produces:
  - `auctionRow` 構造体(DB行)、`auctionSummary` / `auctionDetail` / `bidResponse`(JSON)
  - `(h *handler) summarize(r *http.Request, a *auctionRow) (*auctionSummary, error)` — 入札APIのレスポンスでは使わないが詳細・一覧で共有
  - API: `GET /auctions` → 200 `[auctionSummary]`(status=liveのみ、ends_at昇順)
  - API: `GET /auctions/{id}` → 200 `auctionDetail`(bidsはcreated_at降順・同時刻はid降順) / 404
  - JSON形: `auctionSummary = {id, title, category_id, seller:{id,name}, current_price, bid_count, starts_at, ends_at, status}`、`auctionDetail = auctionSummary + {description, starting_price, bids:[{id, user:{id,name}, amount, created_at}]}`

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/auctions_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type auctionSummaryJSON struct {
	ID           int64     `json:"id"`
	Title        string    `json:"title"`
	CategoryID   int64     `json:"category_id"`
	Seller       userJSON  `json:"seller"`
	CurrentPrice int64     `json:"current_price"`
	BidCount     int64     `json:"bid_count"`
	StartsAt     time.Time `json:"starts_at"`
	EndsAt       time.Time `json:"ends_at"`
	Status       string    `json:"status"`
}

type userJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type bidJSON struct {
	ID        int64     `json:"id"`
	User      userJSON  `json:"user"`
	Amount    int64     `json:"amount"`
	CreatedAt time.Time `json:"created_at"`
}

type auctionDetailJSON struct {
	auctionSummaryJSON
	Description   string    `json:"description"`
	StartingPrice int64     `json:"starting_price"`
	Bids          []bidJSON `json:"bids"`
}

func TestGetAuctions(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var list []auctionSummaryJSON
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 10 {
		t.Fatalf("len = %d, want 10", len(list))
	}
	byID := map[int64]auctionSummaryJSON{}
	for _, a := range list {
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

func TestGetAuctionDetail(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions/1")
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
	if d.StartingPrice != 1000 {
		t.Errorf("starting_price = %d, want 1000", d.StartingPrice)
	}
	if len(d.Bids) != 3 {
		t.Fatalf("bids len = %d, want 3", len(d.Bids))
	}
	// created_at降順(新しい順)
	if d.Bids[0].Amount != 1500 || d.Bids[2].Amount != 1000 {
		t.Errorf("bids order unexpected: %+v", d.Bids)
	}
	if d.Bids[0].User.Name != "seed_user_04" {
		t.Errorf("top bid user = %+v", d.Bids[0].User)
	}
}

func TestGetAuctionNotFound(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/auctions/99999")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd webapp/go && go test ./... -run TestGetAuction -v`
Expected: 404によるFAIL(ルート未登録)。

- [ ] **Step 3: 実装を書く**

`webapp/go/auctions.go`:

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

type auctionRow struct {
	ID            int64     `db:"id"`
	SellerID      int64     `db:"seller_id"`
	CategoryID    int64     `db:"category_id"`
	Title         string    `db:"title"`
	Description   string    `db:"description"`
	StartingPrice int64     `db:"starting_price"`
	StartsAt      time.Time `db:"starts_at"`
	EndsAt        time.Time `db:"ends_at"`
	Status        string    `db:"status"`
}

const auctionColumns = "id, seller_id, category_id, title, description, starting_price, starts_at, ends_at, status"

type auctionSummary struct {
	ID           int64        `json:"id"`
	Title        string       `json:"title"`
	CategoryID   int64        `json:"category_id"`
	Seller       userResponse `json:"seller"`
	CurrentPrice int64        `json:"current_price"`
	BidCount     int64        `json:"bid_count"`
	StartsAt     time.Time    `json:"starts_at"`
	EndsAt       time.Time    `json:"ends_at"`
	Status       string       `json:"status"`
}

type bidResponse struct {
	ID        int64        `json:"id"`
	User      userResponse `json:"user"`
	Amount    int64        `json:"amount"`
	CreatedAt time.Time    `json:"created_at"`
}

type auctionDetail struct {
	auctionSummary
	Description   string        `json:"description"`
	StartingPrice int64         `json:"starting_price"`
	Bids          []bidResponse `json:"bids"`
}

// summarize は1オークションあたり3クエリを発行する。意図的に遅い実装(N+1)。
func (h *handler) summarize(r *http.Request, a *auctionRow) (*auctionSummary, error) {
	var maxAmount sql.NullInt64
	if err := h.db.GetContext(r.Context(), &maxAmount,
		"SELECT MAX(amount) FROM bids WHERE auction_id = ?", a.ID); err != nil {
		return nil, err
	}
	price := a.StartingPrice
	if maxAmount.Valid {
		price = maxAmount.Int64
	}
	var bidCount int64
	if err := h.db.GetContext(r.Context(), &bidCount,
		"SELECT COUNT(*) FROM bids WHERE auction_id = ?", a.ID); err != nil {
		return nil, err
	}
	var seller userResponse
	if err := h.db.GetContext(r.Context(), &seller,
		"SELECT id, name FROM users WHERE id = ?", a.SellerID); err != nil {
		return nil, err
	}
	return &auctionSummary{
		ID: a.ID, Title: a.Title, CategoryID: a.CategoryID, Seller: seller,
		CurrentPrice: price, BidCount: bidCount,
		StartsAt: a.StartsAt, EndsAt: a.EndsAt, Status: a.Status,
	}, nil
}

func (h *handler) getAuctions(w http.ResponseWriter, r *http.Request) {
	var rows []auctionRow
	if err := h.db.SelectContext(r.Context(), &rows,
		"SELECT "+auctionColumns+" FROM auctions WHERE status = 'live' ORDER BY ends_at ASC, id ASC"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	summaries := make([]auctionSummary, 0, len(rows))
	for i := range rows {
		s, err := h.summarize(r, &rows[i]) // 意図的に遅い実装(N+1)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		summaries = append(summaries, *s)
	}
	writeJSON(w, http.StatusOK, summaries)
}

func (h *handler) getAuction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid auction id")
		return
	}
	var a auctionRow
	err = h.db.GetContext(r.Context(), &a,
		"SELECT "+auctionColumns+" FROM auctions WHERE id = ?", id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "auction not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s, err := h.summarize(r, &a)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var bidRows []struct {
		ID        int64     `db:"id"`
		UserID    int64     `db:"user_id"`
		Amount    int64     `db:"amount"`
		CreatedAt time.Time `db:"created_at"`
	}
	if err := h.db.SelectContext(r.Context(), &bidRows,
		"SELECT id, user_id, amount, created_at FROM bids WHERE auction_id = ? ORDER BY created_at DESC, id DESC", id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bids := make([]bidResponse, 0, len(bidRows))
	for _, b := range bidRows {
		var u userResponse
		// 意図的に遅い実装(N+1): 入札ごとにユーザーを引く
		if err := h.db.GetContext(r.Context(), &u,
			"SELECT id, name FROM users WHERE id = ?", b.UserID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		bids = append(bids, bidResponse{ID: b.ID, User: u, Amount: b.Amount, CreatedAt: b.CreatedAt})
	}
	writeJSON(w, http.StatusOK, auctionDetail{
		auctionSummary: *s,
		Description:    a.Description,
		StartingPrice:  a.StartingPrice,
		Bids:           bids,
	})
}
```

`webapp/go/main.go` の `newRouter` にルートを追加:

```go
	r.Get("/auctions", h.getAuctions)
	r.Get("/auctions/{id}", h.getAuction)
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd webapp/go && go test ./... -run TestGetAuction -v`
Expected: 全PASS

- [ ] **Step 5: Commit**

```bash
git add webapp/go
git commit -m "feat: オークション一覧・詳細APIを実装"
```

---

### Task 5: 入札API

**Files:**
- Create: `webapp/go/bids.go`
- Modify: `webapp/go/main.go`(ルート追加)
- Test: `webapp/go/bids_test.go`

**Interfaces:**
- Consumes: `currentUserID`(Task 3)、`auctionRow` / `auctionColumns`(Task 4)
- Produces:
  - API: `POST /auctions/{id}/bids` `{amount}` →
    - 201 `{id, auction_id, user_id, amount, created_at}`
    - 400 `{"error":"bid amount is too low","current_price":<n>}`(現在価格以下)
    - 400(liveでない) / 401(未ログイン) / 404(存在しない)
  - 整合性保証: オークション行の `FOR UPDATE` で入札を直列化(意図的に遅いが正しい実装)

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/bids_test.go`:

```go
package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func loginSeedUser(t *testing.T, tsURL string, n string) *http.Client {
	t.Helper()
	client := newClientWithJar(t)
	res := postJSON(t, client, tsURL+"/login", `{"name":"`+n+`","password":"password"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("seed login status = %d", res.StatusCode)
	}
	return client
}

func TestPostBid(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := loginSeedUser(t, ts.URL, "seed_user_05")

	// auction 1 の現在価格は1500(シード)
	res := postJSON(t, client, ts.URL+"/auctions/1/bids", `{"amount":1600}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", res.StatusCode)
	}
	var b struct {
		ID        int64     `json:"id"`
		AuctionID int64     `json:"auction_id"`
		UserID    int64     `json:"user_id"`
		Amount    int64     `json:"amount"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.NewDecoder(res.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	if b.AuctionID != 1 || b.UserID != 5 || b.Amount != 1600 || b.ID == 0 {
		t.Errorf("unexpected bid: %+v", b)
	}

	// 詳細に反映されている
	res2, err := http.Get(ts.URL + "/auctions/1")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	var d auctionDetailJSON
	if err := json.NewDecoder(res2.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.CurrentPrice != 1600 || d.BidCount != 4 {
		t.Errorf("current_price = %d / bid_count = %d, want 1600 / 4", d.CurrentPrice, d.BidCount)
	}
}

func TestPostBidTooLow(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := loginSeedUser(t, ts.URL, "seed_user_05")

	res := postJSON(t, client, ts.URL+"/auctions/1/bids", `{"amount":1500}`) // 現在価格と同額はNG
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
	var body struct {
		Error        string `json:"error"`
		CurrentPrice int64  `json:"current_price"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.CurrentPrice != 1500 {
		t.Errorf("current_price = %d, want 1500", body.CurrentPrice)
	}
}

func TestPostBidUnauthorized(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := newClientWithJar(t)

	res := postJSON(t, client, ts.URL+"/auctions/1/bids", `{"amount":9999}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
}

func TestPostBidAuctionNotFound(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := loginSeedUser(t, ts.URL, "seed_user_05")

	res := postJSON(t, client, ts.URL+"/auctions/99999/bids", `{"amount":9999}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd webapp/go && go test ./... -run TestPostBid -v`
Expected: 404/405によるFAIL(ルート未登録)。

- [ ] **Step 3: 実装を書く**

`webapp/go/bids.go`:

```go
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

type bidCreated struct {
	ID        int64     `json:"id"`
	AuctionID int64     `json:"auction_id"`
	UserID    int64     `json:"user_id"`
	Amount    int64     `json:"amount"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *handler) postBid(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	auctionID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid auction id")
		return
	}
	var req struct {
		Amount int64 `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tx, err := h.db.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	// 意図的に遅い実装: オークション行ロックで全入札を直列化し、
	// ロックを握ったまま bids 全件走査で現在価格を計算する。
	var a auctionRow
	err = tx.GetContext(r.Context(), &a,
		"SELECT "+auctionColumns+" FROM auctions WHERE id = ? FOR UPDATE", auctionID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "auction not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if a.Status != "live" {
		writeError(w, http.StatusBadRequest, "auction is not live")
		return
	}
	var maxAmount sql.NullInt64
	if err := tx.GetContext(r.Context(), &maxAmount,
		"SELECT MAX(amount) FROM bids WHERE auction_id = ?", auctionID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	current := a.StartingPrice
	if maxAmount.Valid {
		current = maxAmount.Int64
	}
	if req.Amount <= current {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":         "bid amount is too low",
			"current_price": current,
		})
		return
	}
	res, err := tx.ExecContext(r.Context(),
		"INSERT INTO bids (auction_id, user_id, amount) VALUES (?, ?, ?)",
		auctionID, userID, req.Amount)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bidID, _ := res.LastInsertId()
	var createdAt time.Time
	if err := tx.GetContext(r.Context(), &createdAt,
		"SELECT created_at FROM bids WHERE id = ?", bidID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, bidCreated{
		ID: bidID, AuctionID: auctionID, UserID: userID,
		Amount: req.Amount, CreatedAt: createdAt,
	})
}
```

`webapp/go/main.go` の `newRouter` にルートを追加:

```go
	r.Post("/auctions/{id}/bids", h.postBid)
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd webapp/go && go test ./... -v`
Expected: 全PASS

- [ ] **Step 5: Commit**

```bash
git add webapp/go
git commit -m "feat: 入札APIを実装(行ロックによる直列化つき)"
```

---

### Task 6: Dockerfile・nginx・compose統合

**Files:**
- Create: `webapp/go/Dockerfile`
- Create: `dev/nginx.conf`
- Modify: `dev/compose.yaml`(app / nginx サービス追加)

**Interfaces:**
- Consumes: Task 1〜5のアプリとcompose(mysql)
- Produces: `docker compose -f dev/compose.yaml up -d --build` でフルスタック起動、`http://localhost:8080` がアプリに到達(ベンチのデフォルトターゲット)

- [ ] **Step 1: Dockerfileを書く**

`webapp/go/Dockerfile`:

```dockerfile
FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /isubid .

FROM debian:bookworm-slim
COPY --from=build /isubid /usr/local/bin/isubid
ENV ISUBID_PORT=8000
EXPOSE 8000
CMD ["isubid"]
```

- [ ] **Step 2: nginx設定を書く**

`dev/nginx.conf`:

```nginx
server {
    listen 80;

    location / {
        proxy_pass http://app:8000;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }
}
```

- [ ] **Step 3: composeにapp/nginxを追加**

`dev/compose.yaml` を以下の全文に更新:

```yaml
services:
  mysql:
    image: mysql:8
    environment:
      MYSQL_ROOT_PASSWORD: isucon
      MYSQL_DATABASE: isubid
      MYSQL_USER: isucon
      MYSQL_PASSWORD: isucon
    ports:
      - "3306:3306"
    volumes:
      - mysql-data:/var/lib/mysql
    healthcheck:
      test: ["CMD", "mysqladmin", "ping", "-h", "127.0.0.1", "-uisucon", "-pisucon"]
      interval: 2s
      timeout: 2s
      retries: 30

  app:
    build: ../webapp/go
    environment:
      ISUBID_DB_HOST: mysql
      ISUBID_SQL_DIR: /webapp/sql
    volumes:
      - ../webapp/sql:/webapp/sql:ro
    depends_on:
      mysql:
        condition: service_healthy

  nginx:
    image: nginx:1.27
    ports:
      - "8080:80"
    volumes:
      - ./nginx.conf:/etc/nginx/conf.d/default.conf:ro
    depends_on:
      - app

volumes:
  mysql-data:
```

- [ ] **Step 4: フルスタック起動してスモーク確認**

```bash
docker compose -f dev/compose.yaml up -d --build
sleep 5
curl -fsS -X POST http://localhost:8080/initialize
curl -fsS http://localhost:8080/auctions | head -c 300
```

Expected: `{"lang":"go"}` と、liveオークション10件のJSON配列冒頭。

- [ ] **Step 5: Commit**

```bash
git add webapp/go/Dockerfile dev/nginx.conf dev/compose.yaml
git commit -m "feat: Dockerfile/nginx/composeでフルスタック起動できるようにする"
```

---

### Task 7: ベンチマーカー骨格(クライアント+検証ロジック)

**Files:**
- Create: `bench/go.mod`(`go mod init github.com/kyosu-1/isubid/bench`)
- Create: `bench/client.go`
- Create: `bench/model.go`
- Create: `bench/validate.go`
- Test: `bench/validate_test.go`

**Interfaces:**
- Consumes: アプリのJSON形(Task 3〜5のProduces)、シード期待値(Task 1)
- Produces:
  - `Client` 構造体: `NewClient(target string) (*Client, error)`、メソッド
    `Initialize(ctx) (string, error)` / `Register(ctx, name, password string) (*User, error)` / `Login(ctx, name, password string) (*User, error)` / `GetAuctions(ctx) ([]AuctionSummary, error)` / `GetAuction(ctx, id int64) (*AuctionDetail, error)` / `PostBid(ctx, auctionID, amount int64) (*BidCreated, int, error)`(最後のintはHTTPステータス。400系は err=nil で返す)
  - 型: `User{ID,Name}` / `AuctionSummary` / `AuctionDetail` / `Bid` / `BidCreated`(アプリのJSONに対応)
  - `ValidateInitialAuctionList(list []AuctionSummary) error` — シード期待値との照合
  - `ValidateBidReflected(d *AuctionDetail, bid *BidCreated) error` — 入札が詳細に反映されているか

- [ ] **Step 1: モジュール初期化と依存取得**

```bash
mkdir -p bench && cd bench
go mod init github.com/kyosu-1/isubid/bench
go get github.com/isucon/isucandar
```

- [ ] **Step 2: 型定義を書く**

`bench/model.go`:

```go
package main

import "time"

type User struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type AuctionSummary struct {
	ID           int64     `json:"id"`
	Title        string    `json:"title"`
	CategoryID   int64     `json:"category_id"`
	Seller       User      `json:"seller"`
	CurrentPrice int64     `json:"current_price"`
	BidCount     int64     `json:"bid_count"`
	StartsAt     time.Time `json:"starts_at"`
	EndsAt       time.Time `json:"ends_at"`
	Status       string    `json:"status"`
}

type Bid struct {
	ID        int64     `json:"id"`
	User      User      `json:"user"`
	Amount    int64     `json:"amount"`
	CreatedAt time.Time `json:"created_at"`
}

type AuctionDetail struct {
	AuctionSummary
	Description   string `json:"description"`
	StartingPrice int64  `json:"starting_price"`
	Bids          []Bid  `json:"bids"`
}

type BidCreated struct {
	ID        int64     `json:"id"`
	AuctionID int64     `json:"auction_id"`
	UserID    int64     `json:"user_id"`
	Amount    int64     `json:"amount"`
	CreatedAt time.Time `json:"created_at"`
}
```

- [ ] **Step 3: 失敗するテストを書く**

`bench/validate_test.go`:

```go
package main

import (
	"strings"
	"testing"
	"time"
)

func seedList() []AuctionSummary {
	base := AuctionSummary{
		CategoryID: 1,
		StartsAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		EndsAt:     time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		Status:     "live",
	}
	mk := func(id int64, title string, price, count int64, seller int64) AuctionSummary {
		a := base
		a.ID = id
		a.Title = title
		a.CurrentPrice = price
		a.BidCount = count
		a.Seller = User{ID: seller, Name: "seed_user_" + pad2(seller)}
		return a
	}
	return []AuctionSummary{
		mk(1, "ヘリテージ・ウィングチェア", 1500, 3, 1),
		mk(2, "エルゴホスト Model E", 2100, 1, 2),
		mk(3, "ISUレーサー GT", 3100, 1, 3),
		mk(4, "メッシュフロー 40", 4100, 1, 4),
		mk(5, "ミッドセンチュリー・ラウンジ", 2500, 0, 5),
		mk(6, "ネオンストライク Z", 3000, 0, 6),
		mk(7, "スタンドフレックス", 3500, 0, 7),
		mk(8, "チャーチチェア 1920", 4000, 0, 8),
		mk(9, "プロシート・エディション", 4500, 0, 9),
		mk(10, "コンパクトワーク 01", 5000, 0, 10),
	}
}

func TestValidateInitialAuctionListOK(t *testing.T) {
	if err := ValidateInitialAuctionList(seedList()); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestValidateInitialAuctionListWrongPrice(t *testing.T) {
	list := seedList()
	list[0].CurrentPrice = 9999
	err := ValidateInitialAuctionList(list)
	if err == nil || !strings.Contains(err.Error(), "current_price") {
		t.Errorf("want current_price error, got %v", err)
	}
}

func TestValidateInitialAuctionListWrongCount(t *testing.T) {
	err := ValidateInitialAuctionList(seedList()[:9])
	if err == nil {
		t.Error("want error for missing auction, got nil")
	}
}

func TestValidateBidReflected(t *testing.T) {
	d := &AuctionDetail{
		AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1600},
		Bids: []Bid{
			{ID: 100, User: User{ID: 5}, Amount: 1600},
			{ID: 3, User: User{ID: 4}, Amount: 1500},
		},
	}
	bid := &BidCreated{ID: 100, AuctionID: 1, UserID: 5, Amount: 1600}
	if err := ValidateBidReflected(d, bid); err != nil {
		t.Errorf("want nil, got %v", err)
	}

	missing := &BidCreated{ID: 999, AuctionID: 1, UserID: 5, Amount: 1700}
	if err := ValidateBidReflected(d, missing); err == nil {
		t.Error("want error for missing bid, got nil")
	}
}
```

- [ ] **Step 4: テストが失敗することを確認**

Run: `cd bench && go test ./... -v`
Expected: コンパイルエラー(`ValidateInitialAuctionList` / `pad2` 未定義)。

- [ ] **Step 5: 検証ロジックを実装**

`bench/validate.go`:

```go
package main

import (
	"fmt"
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

func pad2(n int64) string {
	return fmt.Sprintf("%02d", n)
}

func ValidateInitialAuctionList(list []AuctionSummary) error {
	if len(list) != len(expectedInitialAuctions) {
		return fmt.Errorf("GET /auctions: 件数が %d (期待: %d)", len(list), len(expectedInitialAuctions))
	}
	for _, a := range list {
		want, ok := expectedInitialAuctions[a.ID]
		if !ok {
			return fmt.Errorf("GET /auctions: 未知のオークション id=%d", a.ID)
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
		if a.Seller.ID != want.SellerID || a.Seller.Name != "seed_user_"+pad2(want.SellerID) {
			return fmt.Errorf("auction %d: seller が %+v (期待: id=%d)", a.ID, a.Seller, want.SellerID)
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

- [ ] **Step 6: クライアントを実装**

`bench/client.go`:

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/isucon/isucandar/agent"
)

type Client struct {
	ag *agent.Agent
}

func NewClient(target string) (*Client, error) {
	ag, err := agent.NewAgent(
		agent.WithBaseURL(target),
		agent.WithTimeout(10*time.Second),
	)
	if err != nil {
		return nil, err
	}
	return &Client{ag: ag}, nil
}

// doJSON はJSONリクエストを送り、ステータスとボディを返す。
func (c *Client) doJSON(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := c.ag.NewRequest(method, path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.ag.Do(ctx, req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, nil, err
	}
	return res.StatusCode, b, nil
}

func (c *Client) Initialize(ctx context.Context) (string, error) {
	code, b, err := c.doJSON(ctx, http.MethodPost, "/initialize", map[string]string{})
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("POST /initialize: status %d (body: %s)", code, b)
	}
	var body struct {
		Lang string `json:"lang"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return "", fmt.Errorf("POST /initialize: 不正なJSON: %w", err)
	}
	return body.Lang, nil
}

func (c *Client) auth(ctx context.Context, path, name, password string, wantCode int) (*User, error) {
	code, b, err := c.doJSON(ctx, http.MethodPost, path, map[string]string{
		"name": name, "password": password,
	})
	if err != nil {
		return nil, err
	}
	if code != wantCode {
		return nil, fmt.Errorf("POST %s: status %d (期待: %d, body: %s)", path, code, wantCode, b)
	}
	var u User
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, fmt.Errorf("POST %s: 不正なJSON: %w", path, err)
	}
	return &u, nil
}

func (c *Client) Register(ctx context.Context, name, password string) (*User, error) {
	return c.auth(ctx, "/register", name, password, http.StatusCreated)
}

func (c *Client) Login(ctx context.Context, name, password string) (*User, error) {
	return c.auth(ctx, "/login", name, password, http.StatusOK)
}

func (c *Client) GetAuctions(ctx context.Context) ([]AuctionSummary, error) {
	code, b, err := c.doJSON(ctx, http.MethodGet, "/auctions", nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET /auctions: status %d", code)
	}
	var list []AuctionSummary
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("GET /auctions: 不正なJSON: %w", err)
	}
	return list, nil
}

func (c *Client) GetAuction(ctx context.Context, id int64) (*AuctionDetail, error) {
	path := fmt.Sprintf("/auctions/%d", id)
	code, b, err := c.doJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", path, code)
	}
	var d AuctionDetail
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("GET %s: 不正なJSON: %w", path, err)
	}
	return &d, nil
}

// PostBid は入札する。4xxはエラーではなくステータスコードで返す(検証側で判断)。
func (c *Client) PostBid(ctx context.Context, auctionID, amount int64) (*BidCreated, int, error) {
	path := fmt.Sprintf("/auctions/%d/bids", auctionID)
	code, b, err := c.doJSON(ctx, http.MethodPost, path, map[string]int64{"amount": amount})
	if err != nil {
		return nil, 0, err
	}
	if code != http.StatusCreated {
		return nil, code, nil
	}
	var bid BidCreated
	if err := json.Unmarshal(b, &bid); err != nil {
		return nil, code, fmt.Errorf("POST %s: 不正なJSON: %w", path, err)
	}
	return &bid, code, nil
}
```

- [ ] **Step 7: テストが通ることを確認**

Run: `cd bench && go test ./... -v`
Expected: 全PASS(client.goはユニットテスト対象外、コンパイルが通ればよい)

- [ ] **Step 8: Commit**

```bash
git add bench
git commit -m "feat: ベンチマーカー骨格(APIクライアント+初期データ検証ロジック)を追加"
```

---

### Task 8: Prepareシナリオとベンチ実行、README

**Files:**
- Create: `bench/scenario.go`
- Create: `bench/main.go`
- Modify: `README.md`(新規作成: クイックスタート)

**Interfaces:**
- Consumes: `Client` / `Validate*`(Task 7)、フルスタックcompose(Task 6)
- Produces: `cd bench && go run . -target http://localhost:8080` でPrepare走行、成功時 `PREPARE: PASS` を出力し exit 0、失敗時は理由と `PREPARE: FAIL` を出力し exit 1

- [ ] **Step 1: Prepareシナリオを書く**

`bench/scenario.go`:

```go
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/isucon/isucandar"
)

// Scenario はISUBIDベンチのシナリオ。Phase 1ではPrepareのみ実装。
type Scenario struct {
	Target string
}

func randomName(prefix string) string {
	b := make([]byte, 4)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (s *Scenario) Prepare(ctx context.Context, step *isucandar.BenchmarkStep) error {
	c, err := NewClient(s.Target)
	if err != nil {
		return err
	}

	// 1. initialize
	lang, err := c.Initialize(ctx)
	if err != nil {
		return err
	}
	if lang == "" {
		return fmt.Errorf("POST /initialize: lang が空")
	}

	// 2. 初期データの検証
	list, err := c.GetAuctions(ctx)
	if err != nil {
		return err
	}
	if err := ValidateInitialAuctionList(list); err != nil {
		return err
	}

	// 3. 新規ユーザー登録と、シードユーザーのログイン
	name := randomName("bench_")
	if _, err := c.Register(ctx, name, "benchpassword"); err != nil {
		return err
	}
	seedClient, err := NewClient(s.Target)
	if err != nil {
		return err
	}
	seedUser, err := seedClient.Login(ctx, "seed_user_05", "password")
	if err != nil {
		return err
	}

	// 4. 入札の検証: 低すぎる入札は400、正しい入札は201で詳細に反映される
	const auctionID = 1
	detail, err := seedClient.GetAuction(ctx, auctionID)
	if err != nil {
		return err
	}
	if _, code, err := seedClient.PostBid(ctx, auctionID, detail.CurrentPrice); err != nil {
		return err
	} else if code != 400 {
		return fmt.Errorf("POST /auctions/%d/bids: 現在価格以下の入札が status %d (期待: 400)", auctionID, code)
	}
	bid, code, err := seedClient.PostBid(ctx, auctionID, detail.CurrentPrice+100)
	if err != nil {
		return err
	}
	if code != 201 {
		return fmt.Errorf("POST /auctions/%d/bids: status %d (期待: 201)", auctionID, code)
	}
	if bid.UserID != seedUser.ID {
		return fmt.Errorf("POST /auctions/%d/bids: user_id が %d (期待: %d)", auctionID, bid.UserID, seedUser.ID)
	}
	after, err := seedClient.GetAuction(ctx, auctionID)
	if err != nil {
		return err
	}
	if err := ValidateBidReflected(after, bid); err != nil {
		return err
	}

	// 5. 未ログインの入札は401
	anon, err := NewClient(s.Target)
	if err != nil {
		return err
	}
	if _, code, err := anon.PostBid(ctx, auctionID, 999999); err != nil {
		return err
	} else if code != 401 {
		return fmt.Errorf("POST /auctions/%d/bids: 未ログイン入札が status %d (期待: 401)", auctionID, code)
	}

	// 後続の負荷走行に備えてデータを初期状態に戻す
	if _, err := c.Initialize(ctx); err != nil {
		return err
	}
	return nil
}
```

- [ ] **Step 2: mainを書く**

`bench/main.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/isucon/isucandar"
)

func main() {
	target := flag.String("target", "http://localhost:8080", "ベンチ対象のベースURL")
	flag.Parse()

	b, err := isucandar.NewBenchmark(isucandar.WithoutPanicRecovery())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b.AddScenario(&Scenario{Target: *target})

	result := b.Start(context.Background())
	errs := result.Errors.All()
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "ERR: %v\n", e)
		}
		fmt.Println("PREPARE: FAIL")
		os.Exit(1)
	}
	fmt.Println("PREPARE: PASS")
}
```

- [ ] **Step 3: フルスタックに対してベンチを実行**

```bash
docker compose -f dev/compose.yaml up -d --build
sleep 5
cd bench && go run . -target http://localhost:8080
```

Expected: `PREPARE: PASS`、exit 0。

- [ ] **Step 4: 壊れた対象でFAILすることを確認(ベンチのベンチの初歩)**

```bash
cd bench && go run . -target http://localhost:9999
echo "exit: $?"
```

Expected: `ERR:` 行(接続失敗)と `PREPARE: FAIL`、exit 1。

- [ ] **Step 5: READMEを書く**

`README.md`(リポジトリルート、全文):

```markdown
# ISUBID

ISUCON形式の性能チューニング競技問題。題材は椅子専門のライブオークションサイト。

- お題アプリ(参照実装): `webapp/go`(わざと遅い初期実装)
- ベンチマーカー: `bench`(isucandarベース)
- 設計ドキュメント: `docs/superpowers/specs/2026-07-08-isubid-design.md`

## クイックスタート

```bash
# 1. フルスタック起動(nginx :8080 → app :8000 → mysql :3306)
docker compose -f dev/compose.yaml up -d --build

# 2. ベンチ実行(現在はPrepare=整合性チェックのみ)
cd bench && go run . -target http://localhost:8080
```

`PREPARE: PASS` が出れば疎通完了。

## 開発

```bash
# アプリのテスト(compose の mysql が必要)
docker compose -f dev/compose.yaml up -d mysql
cd webapp/go && go test ./...

# ベンチのテスト
cd bench && go test ./...
```

## ステータス

Phase 1(骨格疎通)。負荷走行・スコアリング・pub/sub要素・フロントエンドは今後のPhaseで追加予定。
```

- [ ] **Step 6: Commit**

```bash
git add bench README.md
git commit -m "feat: Prepareシナリオとベンチ実行、READMEを追加"
```

---

## 完了条件(Phase 1)

- `docker compose -f dev/compose.yaml up -d --build` → `cd bench && go run . -target http://localhost:8080` で `PREPARE: PASS`
- `webapp/go` / `bench` の `go test ./...` が全PASS
- 意図的なボトルネック(bcrypt 12 / N+1 / MAX全件走査 / FOR UPDATE直列化 / インデックスなし)がコメント付きで残っている
