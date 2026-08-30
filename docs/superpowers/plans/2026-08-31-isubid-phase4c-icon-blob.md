# Phase 4-C 実装計画: ユーザーアイコン BLOB

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `GET /users/:id/icon` を「リクエストのたびに DB から LONGBLOB を読む」形で実装し、当初設計が名指しした「静的配信化ポイント」を出題に載せる。

**Architecture:** ジェネレータが user id から決定論的に 64×64 の PNG を作り、SQL ダンプへ16進リテラルで埋め込む。参照実装はキャッシュヘッダを一切付けずに毎回 DB から読む。ベンチは sha256 でアイコンの同一性を照合するが、**アイコン自体は採点しない**(コストであって報酬ではない)。

**Tech Stack:** Go 1.22 / `image/png` 標準ライブラリ / chi v5 / sqlx / MySQL 8 / isucandar

**Spec:** `docs/superpowers/specs/2026-08-31-isubid-phase4c-icon-blob-design.md`

## Global Constraints

- アイコンは **64×64 の PNG**。`iconSize = 64`
- **1人ずつ異なる画像**であること。全員同じだと「1枚だけ返して使い回す」改悪を検出できない
- 生成ユーザーの **約1割を `icon NULL`**。`iconlessRate = 0.1`
- **シード20人(id 1〜20)は全員 NULL のまま。** `webapp/sql/90_seed_phase1.sql` を変更しない
- `GET /users/:id/icon` の応答: アイコンあり → **200 + `Content-Type: image/png`** / `icon IS NULL` → **404** / ユーザー不在 → **404** / 非数値 id → **400**
- **アイコンの採点タグを作らない**(設計 §4)
- **キャッシュヘッダを報酬にする設計を入れない**(設計 §7)
- コミットする生成物は `small` のみ
- コメント・エラーメッセージ・テストメッセージは日本語
- `// 意図的に遅い実装: ...` のコメントは出題に必要。削除・弱体化しない
- **ベンチマーカーが正しいアプリを誤って FAIL させること(false-FAIL)が、このプロジェクトで最悪の結果**

## 決定論性についての最重要事項

`initial-data` は「同じ scale と seed なら生成物は完全に一致する」ことを設計要件にしている。

`Generate(cfg)` は `rng := rand.New(rand.NewSource(cfg.Seed))` を作ってから
`generateUsers(cfg)` を呼ぶが、**`generateUsers` は `rng` を受け取っていない。**
`rng` を最初に消費するのは `generateAuctions` である。

したがって **アイコンの有無を決める乱数は、共有 `rng` から引いてはならない。** 引くと
乱数の消費順序が変わり、auctions / bids / notifications が**すべて別物になる**
(コミット済みダンプの全ファイルが変わり、4-A/4-B の実測値の前提も崩れる)。

**独立した乱数源を使うこと。** そうすれば `92_auctions.sql` / `93_bids.sql` /
`94_notifications.sql` は**バイト単位で不変**に保たれ、それ自体が決定論性の証拠になる。

## 前提環境

- MySQL: `docker compose -f dev/compose.yaml up -d mysql`(`webapp/go` のテストが接続する)
- アプリ: `docker compose -f dev/compose.yaml up -d` で `http://localhost:8080`
- アプリのコードを変えたら `docker compose -f dev/compose.yaml build app && docker compose -f dev/compose.yaml up -d`
- ベンチ: `cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json`
- 3つの独立した Go モジュール: `webapp/go` / `bench` / `initial-data`

---

### Task 1: 決定論的な PNG アイコン生成

**Files:**
- Create: `initial-data/icon.go`
- Create: `initial-data/icon_test.go`

**Interfaces:**
- Consumes: なし(最初のタスク)
- Produces:
  - `const iconSize = 64`
  - `func GenerateIcon(id int64) []byte` — id から決定論的に PNG バイト列を返す
  - Task 2 が使う

- [ ] **Step 1: 失敗するテストを書く**

`initial-data/icon_test.go` を新規作成:

```go
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
```

- [ ] **Step 2: テストが失敗することを確認する**

Run: `cd initial-data && go test -count=1 -run TestGenerateIcon ./...`
Expected: FAIL(`GenerateIcon` と `iconSize` が未定義でコンパイルエラー)

- [ ] **Step 3: `initial-data/icon.go` を実装する**

```go
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
```

- [ ] **Step 4: テストが通ることを確認する**

Run: `cd initial-data && go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: PASS / vet クリーン / gofmt 出力なし

- [ ] **Step 5: 実サイズを測って控える**

Task 6 でドキュメントに記録するので、ここで実測値を出しておく。一時的にテストへ
`t.Logf` を足して実行し、確認したら**必ず元に戻す**こと。

```bash
cd initial-data
cat >> icon_test.go <<'EOF'

func TestReportIconSize(t *testing.T) {
	for _, id := range []int64{21, 100, 520} {
		t.Logf("id=%d: %d bytes", id, len(GenerateIcon(id)))
	}
}
EOF
go test -count=1 -run TestReportIconSize -v ./...
```

出力の bytes 値を控えたら、追加した `TestReportIconSize` を `icon_test.go` から
削除する(`git diff initial-data/icon_test.go` で消えたことを確認)。**この一時テストを
コミットしないこと。**

- [ ] **Step 6: コミット**

```bash
git add initial-data/icon.go initial-data/icon_test.go
git commit -m "feat: user id から決定論的に 64x64 PNG アイコンを生成する"
```

---

### Task 2: ジェネレータへの組み込みと生成物の再生成

**Files:**
- Modify: `initial-data/generate.go`(`User` 型、`generateUsers`)
- Modify: `initial-data/sqlout.go`(`writeUsers`、バッチ行数)
- Modify: `initial-data/snapshot.go`(`SnapshotUser`)
- Modify: `initial-data/generate_test.go`、`initial-data/sqlout_test.go`、`initial-data/snapshot_test.go`
- Regenerate: `initial-data/out/91_users.sql`、`initial-data/out/snapshot.json`

**Interfaces:**
- Consumes: Task 1 の `GenerateIcon(id int64) []byte`
- Produces:
  - `User.Icon []byte`(nil ならアイコン未設定)
  - `type SnapshotUser struct { ID int64; Name string; IconSHA256 string }`(JSON: `id` / `name` / `icon_sha256`)
  - `Snapshot.Users []SnapshotUser`
  - Task 4・5 がスナップショットを読む

**この Task の最重要事項:** 再生成後に `92_auctions.sql` / `93_bids.sql` / `94_notifications.sql` が**バイト単位で不変**であること。変わっていたら乱数の消費順序を壊しているので、原因を突き止めるまで先へ進まないこと。

- [ ] **Step 1: 失敗するテストを書く**

`initial-data/generate_test.go` に追記:

```go
// TestGeneratedUsersHaveIcons はアイコンの付与規則を固定する。
func TestGeneratedUsersHaveIcons(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	withIcon, without := 0, 0
	for _, u := range ds.Users {
		if u.Icon == nil {
			without++
			continue
		}
		withIcon++
		if len(u.Icon) == 0 {
			t.Errorf("user %d: Icon が空スライス(未設定は nil で表すこと)", u.ID)
		}
	}
	if withIcon+without != cfg.Users {
		t.Fatalf("合計が %d, want %d", withIcon+without, cfg.Users)
	}
	// 約1割が未設定。生成数が500なので 25〜75件のレンジに入っていれば妥当とみなす。
	if without < 25 || without > 75 {
		t.Errorf("アイコン未設定が %d件 (期待: 25〜75件、約1割)", without)
	}
}

// TestIconAssignmentIsDeterministic は、どのユーザーが未設定になるかが
// seed から決まることを固定する。
func TestIconAssignmentIsDeterministic(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	a := Generate(cfg)
	b := Generate(cfg)
	for i := range a.Users {
		if (a.Users[i].Icon == nil) != (b.Users[i].Icon == nil) {
			t.Fatalf("user %d: アイコンの有無が2回の生成で異なる", a.Users[i].ID)
		}
		if !bytes.Equal(a.Users[i].Icon, b.Users[i].Icon) {
			t.Fatalf("user %d: アイコンのバイト列が2回の生成で異なる", a.Users[i].ID)
		}
	}
}

// TestIconRNGDoesNotDisturbMainStream は、アイコン用の乱数がメインの乱数列を
// 乱していないことを固定する。
//
// generateUsers が共有 rng を消費すると auctions / bids / notifications が
// すべて別物になり、コミット済みダンプの全ファイルが変わってしまう。
// アイコンは独立した乱数源から引かなければならない。
func TestIconRNGDoesNotDisturbMainStream(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	// 生成物の指紋。この値は「アイコン導入前と同じ」であることに意味がある。
	// 値そのものは Task 2 の実装後に一度実行して埋めること(実測値を使う)。
	if len(ds.Auctions) == 0 || len(ds.Bids) == 0 {
		t.Fatal("生成データが空")
	}
	first, last := ds.Auctions[0], ds.Auctions[len(ds.Auctions)-1]
	t.Logf("auctions[0]: id=%d seller=%d title=%q", first.ID, first.SellerID, first.Title)
	t.Logf("auctions[last]: id=%d seller=%d title=%q", last.ID, last.SellerID, last.Title)
	t.Logf("bids: %d件, notifications: %d件", len(ds.Bids), len(ds.Notifications))
}
```

`initial-data/generate_test.go` の import に `bytes` を足すこと。

**`TestIconRNGDoesNotDisturbMainStream` は現状 `t.Logf` で観測するだけの弱いテスト。** 本当の証拠は Step 6 の「92〜94 のダンプが無変更」であり、そちらを正とする。このテストは将来デバッグする人への手がかりとして残す。

`initial-data/snapshot_test.go` に追記:

```go
// TestSnapshotCarriesUserIcons は生成ユーザーのアイコン sha256 が
// スナップショットに載ることを固定する。
func TestSnapshotCarriesUserIcons(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)
	snap := BuildSnapshot(ds)

	if len(snap.Users) != len(ds.Users) {
		t.Fatalf("snapshot.Users が %d件, want %d件", len(snap.Users), len(ds.Users))
	}
	byID := map[int64]SnapshotUser{}
	for _, su := range snap.Users {
		byID[su.ID] = su
	}
	withIcon, without := 0, 0
	for _, u := range ds.Users {
		su, ok := byID[u.ID]
		if !ok {
			t.Fatalf("user %d が snapshot に無い", u.ID)
		}
		if su.Name != u.Name {
			t.Errorf("user %d: name が %q (期待: %q)", u.ID, su.Name, u.Name)
		}
		if u.Icon == nil {
			without++
			if su.IconSHA256 != "" {
				t.Errorf("user %d: アイコン未設定なのに icon_sha256 が %q", u.ID, su.IconSHA256)
			}
			continue
		}
		withIcon++
		want := fmt.Sprintf("%x", sha256.Sum256(u.Icon))
		if su.IconSHA256 != want {
			t.Errorf("user %d: icon_sha256 が %q (期待: %q)", u.ID, su.IconSHA256, want)
		}
	}
	if withIcon == 0 || without == 0 {
		t.Errorf("アイコンあり %d件 / なし %d件 — どちらも1件以上あること", withIcon, without)
	}
}
```

`initial-data/snapshot_test.go` の import に `crypto/sha256` と `fmt` を足すこと。

`initial-data/sqlout_test.go` に追記:

```go
// TestUsersSQLCarriesIconHex は users の INSERT にアイコンが16進リテラルで
// 載り、未設定は NULL になることを固定する。
func TestUsersSQLCarriesIconHex(t *testing.T) {
	dir := t.TempDir()
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)
	if err := WriteSQL(dir, ds); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "91_users.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sql := string(b)

	if !strings.Contains(sql, "INSERT INTO users (id, name, password_hash, icon) VALUES") {
		t.Error("users の INSERT に icon 列が無い")
	}
	if !strings.Contains(sql, ", NULL)") {
		t.Error("アイコン未設定の NULL が書かれていない")
	}
	// アイコンを持つ最初のユーザーの16進リテラルが含まれること
	for _, u := range ds.Users {
		if u.Icon == nil {
			continue
		}
		want := "0x" + hex.EncodeToString(u.Icon)
		if !strings.Contains(sql, want) {
			t.Errorf("user %d のアイコンが16進リテラルとして見つからない", u.ID)
		}
		break
	}
}
```

`initial-data/sqlout_test.go` の import に `encoding/hex`、`os`、`path/filepath`、`strings` を足すこと(既にあるものは重複させない)。

- [ ] **Step 2: テストが失敗することを確認する**

Run: `cd initial-data && go test -count=1 ./...`
Expected: FAIL(`User.Icon`、`SnapshotUser`、`Snapshot.Users` が未定義でコンパイルエラー)

- [ ] **Step 3: `initial-data/generate.go` を変更する**

`User` 型に `Icon` を追加:

```go
type User struct {
	ID   int64
	Name string
	// Icon はアイコン PNG のバイト列。nil ならアイコン未設定(DB では NULL)。
	Icon []byte
}
```

`generateUsers` を差し替え:

```go
// iconlessRate はアイコンを持たない生成ユーザーの割合。
//
// 実サイトに即しているだけでなく、参照実装が「icon IS NULL のユーザーに
// 404 ではなく 200 を返す」改悪をしたときにベンチが検出できるようにするため、
// 未設定のユーザーを必ず作る。
const iconlessRate = 0.1

// newIconRNG はアイコン専用の乱数源を作る。
//
// **共有の rng から引いてはならない。** generateUsers は Generate の中で
// generateAuctions より先に呼ばれるので、共有 rng を消費すると乱数の順序が
// ずれ、auctions / bids / notifications がすべて別物になる(コミット済み
// ダンプの全ファイルが変わり、4-A/4-B の実測値の前提も崩れる)。
// 独立した源から引くことで 92〜94 のダンプはバイト単位で不変に保たれる。
func newIconRNG(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed ^ 0x1c04))
}

func generateUsers(cfg Config) []User {
	iconRNG := newIconRNG(cfg.Seed)
	users := make([]User, 0, cfg.Users)
	for i := 0; i < cfg.Users; i++ {
		id := int64(SeedMaxUserID + 1 + i)
		u := User{ID: id, Name: "gen_user_" + pad5(id)}
		if iconRNG.Float64() >= iconlessRate {
			u.Icon = GenerateIcon(id)
		}
		users = append(users, u)
	}
	return users
}
```

- [ ] **Step 4: `initial-data/sqlout.go` を変更する**

`writeChunked` を行数指定できる形に一般化し、既存の呼び出しは挙動を変えない:

```go
// writeChunked は n 行を rowsPerStatement ごとに区切って INSERT 文を書く。
func writeChunked(w *bufio.Writer, header string, n int, row func(i int) string) error {
	return writeChunkedN(w, header, n, rowsPerStatement, row)
}

// writeChunkedN は1文あたりの行数を指定できる writeChunked。
// アイコンのように1行が大きいテーブルで行数を下げるために使う。
func writeChunkedN(w *bufio.Writer, header string, n, perStatement int, row func(i int) string) error {
	if n == 0 {
		// 空でも構文として成立するファイルにしておく(mysql が読んでも何もしない)
		_, err := fmt.Fprintf(w, "-- no rows\nSELECT 1;\n")
		return err
	}
	for start := 0; start < n; start += perStatement {
		end := start + perStatement
		if end > n {
			end = n
		}
		if _, err := fmt.Fprint(w, header); err != nil {
			return err
		}
		for i := start; i < end; i++ {
			sep := ",\n"
			if i == end-1 {
				sep = ";\n"
			}
			if _, err := fmt.Fprint(w, row(i), sep); err != nil {
				return err
			}
		}
	}
	return nil
}
```

`writeUsers` を差し替え:

```go
// usersPerStatement は users の INSERT 1文に載せる行数。
//
// アイコンが入ると1行が数百バイト〜数KB(バイナリが16進で2倍)になるため、
// 他テーブルの rowsPerStatement(1000)のままだと1文が肥大する。
// 100行なら1文あたり数百KBに収まり、MySQL の max_allowed_packet に余裕を持てる。
const usersPerStatement = 100

func writeUsers(w *bufio.Writer, ds *Dataset) error {
	return writeChunkedN(w, "INSERT INTO users (id, name, password_hash, icon) VALUES\n",
		len(ds.Users), usersPerStatement, func(i int) string {
			u := ds.Users[i]
			return fmt.Sprintf("(%d, '%s', '%s', %s)",
				u.ID, u.Name, GeneratedPasswordHash, iconSQL(u.Icon))
		})
}

// iconSQL はアイコンを MySQL の16進リテラルへ変換する。nil は NULL。
func iconSQL(b []byte) string {
	if b == nil {
		return "NULL"
	}
	return "0x" + hex.EncodeToString(b)
}
```

`initial-data/sqlout.go` の import に `encoding/hex` を足すこと。

- [ ] **Step 5: `initial-data/snapshot.go` を変更する**

```go
// SnapshotUser は生成ユーザーの正解値。
// IconSHA256 が空文字ならアイコン未設定(GET /users/:id/icon は 404 を返すべき)。
type SnapshotUser struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	IconSHA256 string `json:"icon_sha256"`
}
```

`Snapshot` に追加:

```go
type Snapshot struct {
	Seed             int64             `json:"seed"`
	Scale            string            `json:"scale"`
	Counts           Counts            `json:"counts"`
	SampleAuctionIDs []int64           `json:"sample_auction_ids"`
	Users            []SnapshotUser    `json:"users"`
	Auctions         []SnapshotAuction `json:"auctions"`
}
```

`BuildSnapshot` に users の組み立てを追加(既存の auctions の処理はそのまま):

```go
	snap.Users = make([]SnapshotUser, 0, len(ds.Users))
	for _, u := range ds.Users {
		su := SnapshotUser{ID: u.ID, Name: u.Name}
		if u.Icon != nil {
			su.IconSHA256 = fmt.Sprintf("%x", sha256.Sum256(u.Icon))
		}
		snap.Users = append(snap.Users, su)
	}
```

`initial-data/snapshot.go` の import に `crypto/sha256` と `fmt` を足すこと。

- [ ] **Step 6: テストが通ることを確認する**

Run: `cd initial-data && go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: PASS / vet クリーン / gofmt 出力なし

- [ ] **Step 7: 生成物を作り直し、決定論性を確認する**

```bash
cd /Users/abe/ghq/github.com/kyosu-1/isubid/initial-data
git status --short out/          # 変更が無いことを確認
go run . -scale small -out out
cd ..
git status --short initial-data/out/
```

Expected: **`91_users.sql` と `snapshot.json` だけが変更されている。**

**`92_auctions.sql` / `93_bids.sql` / `94_notifications.sql` に差分が出たら、アイコン用の乱数が共有 `rng` を消費している。** その場合は原因を突き止めるまで先へ進まないこと。これがこのタスクの最重要の検査である。

```bash
git diff --stat initial-data/out/
```

- [ ] **Step 8: 生成物のサイズを記録する**

```bash
du -h initial-data/out/*
du -sh initial-data/out
```

出力を控えておくこと(Task 6 で `docs/phase4-notes.md` に記録する)。

- [ ] **Step 9: コミット**

```bash
git add initial-data/generate.go initial-data/sqlout.go initial-data/snapshot.go \
        initial-data/generate_test.go initial-data/sqlout_test.go initial-data/snapshot_test.go \
        initial-data/out/91_users.sql initial-data/out/snapshot.json
git commit -m "feat: 生成ユーザーにアイコンを付与しスナップショットにsha256を載せる"
```

---

### Task 3: GET /users/:id/icon の実装

**Files:**
- Create: `webapp/go/users.go`
- Create: `webapp/go/users_test.go`
- Modify: `webapp/go/main.go`(ルーティング)

**Interfaces:**
- Consumes: なし(`webapp/go` は他タスクから独立)
- Produces: `GET /users/{id}/icon` エンドポイント。Task 4・5 のベンチが叩く

**注意:** `webapp/go` のテストは生成データ無しで走る(`initApp` はスキーマとシードのみ投入する)。**シード20人は全員 `icon` が NULL** なので、200 の経路をテストするにはテスト内で DB を直接更新する。

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/users_test.go` を新規作成:

```go
package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"testing"
)

func TestGetUserIcon(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	// シードユーザーは全員 icon が NULL なので、200 の経路を見るために
	// テスト内で直接 BLOB を入れる。initApp が毎テスト前に戻すので他へ影響しない。
	db, err := connectDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x01, 0x02, 0x03}
	if _, err := db.Exec("UPDATE users SET icon = ? WHERE id = 1", want); err != nil {
		t.Fatal(err)
	}

	res, err := http.Get(ts.URL + "/users/1/icon")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("本文が %d bytes (期待: %d bytes、内容不一致)", len(got), len(want))
	}
}

func TestGetUserIconNotSet(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	// シードユーザーは全員 icon が NULL
	res, err := http.Get(ts.URL + "/users/2/icon")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("アイコン未設定の status = %d, want 404", res.StatusCode)
	}
}

func TestGetUserIconUnknownUser(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	res, err := http.Get(ts.URL + "/users/99999/icon")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("存在しないユーザーの status = %d, want 404", res.StatusCode)
	}
}

func TestGetUserIconInvalidID(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	for _, id := range []string{"notanumber", "1.5", "99999999999999999999"} {
		res, err := http.Get(fmt.Sprintf("%s/users/%s/icon", ts.URL, id))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("id=%q の status = %d, want 400", id, res.StatusCode)
		}
	}
}
```

- [ ] **Step 2: テストが失敗することを確認する**

Run: `cd webapp/go && go test -count=1 -run TestGetUserIcon ./...`
Expected: FAIL(ルートが未登録で 404 が返る。`TestGetUserIcon` は「status = 404, want 200」で落ちる)

- [ ] **Step 3: `webapp/go/users.go` を実装する**

```go
package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// getUserIcon はユーザーアイコンを返す。
//
// アイコンあり → 200 + image/png、icon IS NULL → 404、ユーザー不在 → 404、
// 非数値 id → 400。
func (h *handler) getUserIcon(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	// 意図的に遅い実装: リクエストのたびに LONGBLOB を丸ごと DB から読む。
	// 画像を配るのに毎回 MySQL へ往復している。一覧1ページ(20件)を開くと
	// 最大20回この経路を通るため、静的配信へ外出しするのが想定の攻略線。
	var icon []byte
	err = h.db.GetContext(r.Context(), &icon, "SELECT icon FROM users WHERE id = ?", id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(icon) == 0 {
		// icon IS NULL。アイコン未設定のユーザーは 404 を返す。
		writeError(w, http.StatusNotFound, "icon not found")
		return
	}

	// 意図的に遅い実装: Cache-Control / ETag / Last-Modified を一切付けない。
	// クライアントは毎回取り直すことになる。
	w.Header().Set("Content-Type", "image/png")
	w.WriteHeader(http.StatusOK)
	w.Write(icon)
}
```

- [ ] **Step 4: ルーティングを追加する**

`webapp/go/main.go` の `routerFor` に1行足す(`/stats/me` の直前):

```go
	r.Get("/users/{id}/icon", h.getUserIcon)
```

- [ ] **Step 5: テストが通ることを確認する**

Run: `cd webapp/go && go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: PASS / vet クリーン / gofmt 出力なし

- [ ] **Step 6: コミット**

```bash
git add webapp/go/users.go webapp/go/users_test.go webapp/go/main.go
git commit -m "feat: GET /users/:id/icon をDBのBLOBから毎回読む形で実装する"
```

---

### Task 4: ベンチのクライアントと Prepare 検証

**Files:**
- Modify: `bench/snapshot.go`(`SnapshotUser` の読み取り側)
- Modify: `bench/client.go`(`GetUserIcon`)
- Modify: `bench/validate.go`(アイコン検証)
- Modify: `bench/validate_test.go`
- Modify: `bench/scenario.go`(Prepare への組み込み)

**Interfaces:**
- Consumes: Task 2 の `snapshot.json` の `users` 配列、Task 3 のエンドポイント
- Produces:
  - `type SnapshotUser struct { ID int64; Name string; IconSHA256 string }`(bench 側)
  - `func (s *Snapshot) UserByID(id int64) (*SnapshotUser, bool)`
  - `func (c *Client) GetUserIcon(ctx context.Context, id int64) (int, string, []byte, error)`
  - `func ValidateUserIcon(id int64, code int, contentType string, body []byte, su *SnapshotUser) error`
  - Task 5 が `GetUserIcon` と `ValidateUserIcon` を使う

- [ ] **Step 1: 失敗するテストを書く**

`bench/validate_test.go` に追記:

```go
func TestValidateUserIcon(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47, 1, 2, 3}
	sum := fmt.Sprintf("%x", sha256.Sum256(png))
	withIcon := &SnapshotUser{ID: 100, Name: "gen_user_00100", IconSHA256: sum}
	without := &SnapshotUser{ID: 101, Name: "gen_user_00101"}

	// 正常系: アイコンあり
	if err := ValidateUserIcon(100, 200, "image/png", png, withIcon); err != nil {
		t.Errorf("正常なアイコンが拒否された: %v", err)
	}
	// 正常系: アイコン未設定は 404
	if err := ValidateUserIcon(101, 404, "", nil, without); err != nil {
		t.Errorf("アイコン未設定の404が拒否された: %v", err)
	}
	// 正常系: スナップショットに無いユーザー(ベンチが走行中に作った新規ユーザー等)は 404
	if err := ValidateUserIcon(99999, 404, "", nil, nil); err != nil {
		t.Errorf("未知ユーザーの404が拒否された: %v", err)
	}

	// 異常系
	for _, tt := range []struct {
		name        string
		id          int64
		code        int
		contentType string
		body        []byte
		su          *SnapshotUser
	}{
		{"アイコンありなのに404", 100, 404, "", nil, withIcon},
		{"別人のアイコンが返る", 100, 200, "image/png", []byte("other"), withIcon},
		{"バイト列が途中で切れている", 100, 200, "image/png", png[:3], withIcon},
		{"Content-Type が image/png でない", 100, 200, "application/octet-stream", png, withIcon},
		{"アイコン未設定なのに200", 101, 200, "image/png", png, without},
		{"未知ユーザーなのに200", 99999, 200, "image/png", png, nil},
		{"予期しないステータス", 100, 500, "", nil, withIcon},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateUserIcon(tt.id, tt.code, tt.contentType, tt.body, tt.su); err == nil {
				t.Error("検出されない")
			}
		})
	}
}
```

`bench/validate_test.go` の import に `crypto/sha256` を足すこと(`fmt` は既にあるはず)。

- [ ] **Step 2: テストが失敗することを確認する**

Run: `cd bench && go test -count=1 -run TestValidateUserIcon ./...`
Expected: FAIL(`ValidateUserIcon` と `SnapshotUser` が未定義でコンパイルエラー)

- [ ] **Step 3: `bench/snapshot.go` に読み取り側を足す**

```go
// SnapshotUser は生成ユーザーの正解値。initial-data/snapshot.go の同名型と対になる。
// IconSHA256 が空文字ならアイコン未設定(GET /users/:id/icon は 404 を返すべき)。
type SnapshotUser struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	IconSHA256 string `json:"icon_sha256"`
}
```

`Snapshot` に `Users []SnapshotUser` と索引を追加:

```go
type Snapshot struct {
	Seed             int64             `json:"seed"`
	Scale            string            `json:"scale"`
	Counts           SnapshotCounts    `json:"counts"`
	SampleAuctionIDs []int64           `json:"sample_auction_ids"`
	Users            []SnapshotUser    `json:"users"`
	Auctions         []SnapshotAuction `json:"auctions"`

	byID     map[int64]*SnapshotAuction
	userByID map[int64]*SnapshotUser
}
```

`LoadSnapshot` の索引作成に追加:

```go
	s.userByID = make(map[int64]*SnapshotUser, len(s.Users))
	for i := range s.Users {
		s.userByID[s.Users[i].ID] = &s.Users[i]
	}
```

```go
// UserByID は生成ユーザーの正解値を返す。シードユーザーやベンチが走行中に
// 作ったユーザーは載っていないので、ok が false になる。
func (s *Snapshot) UserByID(id int64) (*SnapshotUser, bool) {
	u, ok := s.userByID[id]
	return u, ok
}
```

- [ ] **Step 4: `bench/client.go` に `GetUserIcon` を足す**

既存の `doJSONWith` はレスポンスヘッダを捨てるので、生の応答を返すヘルパーを足す:

```go
// doRaw は生のGETを送り、ステータス・Content-Type・本文を返す。
// 画像のようにJSONでない応答を扱うために使う。
func (c *Client) doRaw(ctx context.Context, path string) (int, string, []byte, error) {
	req, err := c.ag.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return 0, "", nil, err
	}
	res, err := c.ag.Do(ctx, req)
	if err != nil {
		return 0, "", nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, "", nil, err
	}
	return res.StatusCode, res.Header.Get("Content-Type"), b, nil
}

// GetUserIcon はアイコンを取得し、ステータス・Content-Type・本文を返す。
// 404 はアイコン未設定・ユーザー不在の正常な応答なので、エラーにはしない。
func (c *Client) GetUserIcon(ctx context.Context, id int64) (int, string, []byte, error) {
	return c.doRaw(ctx, fmt.Sprintf("/users/%d/icon", id))
}
```

- [ ] **Step 5: `bench/validate.go` に `ValidateUserIcon` を足す**

```go
// ValidateUserIcon はアイコンの応答を照合する。
//
// su が nil なら「スナップショットに無いユーザー」= シードユーザーか、ベンチが
// 走行中に作った新規ユーザー。どちらもアイコンを持たないので 404 が正しい。
//
// sha256 を照合するのは「全ユーザーに同じ画像を返す」改悪を捕まえるためである。
// アイコンの内容は走行中に変化しないので、ここに false-FAIL の余地は無い。
func ValidateUserIcon(id int64, code int, contentType string, body []byte, su *SnapshotUser) error {
	wantIcon := su != nil && su.IconSHA256 != ""

	if !wantIcon {
		if code != http.StatusNotFound {
			return fmt.Errorf("GET /users/%d/icon: status %d (期待: 404、アイコン未設定のユーザー)", id, code)
		}
		return nil
	}

	if code != http.StatusOK {
		return fmt.Errorf("GET /users/%d/icon: status %d (期待: 200)", id, code)
	}
	if contentType != "image/png" {
		return fmt.Errorf("GET /users/%d/icon: Content-Type が %q (期待: image/png)", id, contentType)
	}
	got := fmt.Sprintf("%x", sha256.Sum256(body))
	if got != su.IconSHA256 {
		return fmt.Errorf("GET /users/%d/icon: 内容が不一致 (%d bytes, sha256 %s、期待: %s)",
			id, len(body), got[:16], su.IconSHA256[:16])
	}
	return nil
}
```

`bench/validate.go` の import に `crypto/sha256` と `net/http` を足すこと(既にあるものは重複させない)。

- [ ] **Step 6: テストが通ることを確認する**

Run: `cd bench && go test -count=1 -run TestValidateUserIcon ./...`
Expected: PASS

- [ ] **Step 7: Prepare に組み込む**

`bench/scenario.go` の Prepare、**代表サンプルの詳細照合ループの直後**(検索・不正値ブロックより前)に追加:

```go
		// アイコン。スナップショットからアイコンあり3人・なし2人をサンプルする。
		var withIcon, withoutIcon []int64
		for i := range s.Snapshot.Users {
			u := &s.Snapshot.Users[i]
			if u.IconSHA256 != "" {
				if len(withIcon) < 3 {
					withIcon = append(withIcon, u.ID)
				}
			} else if len(withoutIcon) < 2 {
				withoutIcon = append(withoutIcon, u.ID)
			}
			if len(withIcon) == 3 && len(withoutIcon) == 2 {
				break
			}
		}
		if len(withIcon) == 0 || len(withoutIcon) == 0 {
			return fmt.Errorf("スナップショットが不整合: アイコンあり %d人 / なし %d人 (どちらも1人以上必要)",
				len(withIcon), len(withoutIcon))
		}
		for _, id := range append(append([]int64{}, withIcon...), withoutIcon...) {
			su, _ := s.Snapshot.UserByID(id)
			code, ct, body, err := c.GetUserIcon(ctx, id)
			if err != nil {
				return err
			}
			if err := ValidateUserIcon(id, code, ct, body, su); err != nil {
				return err
			}
		}
		// シードユーザー(アイコン未設定)と存在しないユーザーは 404
		for _, id := range []int64{1, 999999} {
			code, ct, body, err := c.GetUserIcon(ctx, id)
			if err != nil {
				return err
			}
			if err := ValidateUserIcon(id, code, ct, body, nil); err != nil {
				return err
			}
		}
		// 非数値の id は 400
		code, _, _, err := c.doRaw(ctx, "/users/notanumber/icon")
		if err != nil {
			return err
		}
		if code != 400 {
			return fmt.Errorf("GET /users/notanumber/icon: status %d (期待: 400)", code)
		}
```

- [ ] **Step 8: 実機で Prepare を通す**

```bash
docker compose -f dev/compose.yaml build app && docker compose -f dev/compose.yaml up -d
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json -prepare-only
```

Expected: `PREPARE: PASS`。所要時間も記録する(6秒基準内であること)。

- [ ] **Step 9: 全テストとビルド**

Run: `cd bench && go build ./... && go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: PASS / vet クリーン / gofmt 出力なし

- [ ] **Step 10: コミット**

```bash
git add bench/snapshot.go bench/client.go bench/validate.go bench/validate_test.go bench/scenario.go
git commit -m "feat: Prepareにアイコンのsha256照合と404/400検証を追加する"
```

---

### Task 5: Load へのアイコン取得の組み込み

**Files:**
- Modify: `bench/load.go`(watcherIteration)

**Interfaces:**
- Consumes: Task 4 の `GetUserIcon` / `ValidateUserIcon` / `Snapshot.UserByID`
- Produces: なし(最終の実装タスク)

**設計上の要点:** **アイコンに採点タグを作らない。** アイコンはコストであって報酬ではない。速く返せるようになると watcher の1周が速くなり、その結果 `GET /auctions` と `GET /auctions/:id` の回数が増えて**そちらの加点が増える**。これが正しいインセンティブで、アイコン取得回数に加点すると「重いアイコンを大量に配るほど点が入る」という逆向きの誘導になる。

- [ ] **Step 1: watcherIteration にアイコン取得を足す**

`bench/load.go` の `watcherIteration` で、一覧の述語検査が終わり `len(list) == 0` の早期 return を抜けた**直後**に追加する(詳細取得の前):

```go
	// ブラウザと同じように、一覧に出た出品者のアイコンを取得する。
	// ベンチはイテレーションごとに新しいクライアントを作る(= 毎回キャッシュが空の
	// 新規訪問者)ので、アイコンは必ず再取得される。これが「リクエストのたびに
	// LONGBLOB を DB から読む」仕込みを持続的な負荷にしている。
	//
	// アイコンには採点タグを作らない。アイコンはコストであって報酬ではなく、
	// 速く返せるようになった見返りは「1周が速くなって一覧と詳細の回数が増える」
	// という形で既存の採点に現れる。
	seenSeller := make(map[int64]bool, len(list))
	for _, a := range list {
		if seenSeller[a.Seller.ID] {
			continue
		}
		seenSeller[a.Seller.ID] = true
		code, ct, body, err := c.GetUserIcon(ctx, a.Seller.ID)
		if err != nil {
			addErr(ctx, step, ErrApplication, err)
			return
		}
		if s.Snapshot == nil {
			// 生成データ非搭載モード。全ユーザーがアイコンを持たず正解値も無いので、
			// 取得して負荷はかけるが照合はしない。
			continue
		}
		su, _ := s.Snapshot.UserByID(a.Seller.ID)
		if err := ValidateUserIcon(a.Seller.ID, code, ct, body, su); err != nil {
			addErr(ctx, step, ErrCritical, err)
			return
		}
	}
```

**`s.Snapshot == nil`(生成データ非搭載モード)では照合しない。** そのモードでは全ユーザーがアイコンを持たず、正解値も無い。取得だけ行って負荷はかける。

- [ ] **Step 2: ビルドとテスト**

Run: `cd bench && go build ./... && go test -count=1 ./... && go vet ./... && gofmt -l .`
Expected: PASS / vet クリーン / gofmt 出力なし

- [ ] **Step 3: 実機で60秒走らせる**

```bash
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json
```

Expected: `LIVENESS: PASS`、`RESULT: PASS`、critical 0件。

**`GET /auctions` と `GET /auctions/:id` の回数が 4-E1 の水準から下がるはず**(watcher が最大20回のアイコン取得を挟むため1周が重くなる)。下がり幅を記録すること。

- [ ] **Step 4: コミット**

```bash
git add bench/load.go
git commit -m "feat: watcherが一覧の出品者アイコンを取得しsha256を照合する"
```

---

### Task 6: ゲート測定と文書化

**Files:**
- Modify: `docs/phase4-notes.md`

**Interfaces:**
- Consumes: Task 1〜5 の全て
- Produces: なし(最終タスク)

**注意:** 測定値は**必ずツールの実出力から転記**すること。数字を辻褄合わせで再構成してはならない。出力を確実に復元できない場合は「復元できない」と正直に書く。**このプロジェクトでは過去に、報告書のスコア内訳が算術的に成立しない事故と、存在しないコミットハッシュが報告される事故があった。**

- [ ] **Step 1: G1(Prepare 3回連続、6秒基準)**

```bash
docker compose -f dev/compose.yaml up -d
cd bench
for i in 1 2 3; do
  echo "=== run $i ==="
  ( time go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json -prepare-only ) 2>&1 | grep -E 'PREPARE|real'
done
```

Expected: 3回とも `PREPARE: PASS`、`real` が6秒未満。

- [ ] **Step 2: G2(60秒走行)**

```bash
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json
```

Expected: `LIVENESS: PASS`、`RESULT: PASS`、critical 0件。出力全文を記録する。

**4-E1 で入った `LIVENESS: PASS` の確認が、旧手順の「採点対象すべてが0回でないことを目視で確認」を置き換えている。** `RESULT: PASS` だけでなくこの行も見ること。

- [ ] **Step 3: G3(改悪5種がすべて FAIL すること)**

各改悪を1つずつ `webapp/go/users.go` に入れ、`docker compose -f dev/compose.yaml build app && docker compose -f dev/compose.yaml up -d` してからベンチを走らせ、FAIL することを確認する。**改悪は1つずつ。** 各改悪の後は必ず `git checkout webapp/go/users.go` で戻し、アプリを再ビルドすること。

| # | 改悪 | 期待 |
|---|---|---|
| 1 | 全ユーザーに同じ画像を返す(`WHERE id = ?` を `WHERE id = 21` に固定) | sha256 照合で FAIL |
| 2 | `len(icon) == 0` の 404 を消して 200 + 空ボディを返す | アイコン未設定の 404 検証で FAIL |
| 3 | `sql.ErrNoRows` の 404 を消して 200 + 空ボディを返す | 存在しないユーザーの 404 検証で FAIL |
| 4 | `Content-Type` を `application/octet-stream` にする | Content-Type 検証で FAIL |
| 5 | `w.Write(icon)` を `w.Write(icon[:len(icon)/2])` にする | sha256 照合で FAIL |

各改悪について、**何をどう書き換えたか(diff)と、FAIL したときの実際のエラーメッセージ**を記録すること。

**最後に `git status` がクリーンであることを確認すること。**

- [ ] **Step 4: G4(非回帰)**

G2 のスコア内訳を 4-E1 のゲート1(`docs/phase4-notes.md` に記録がある)と比較する。

見るべきもの:

1. **`GET /auctions` と `GET /auctions/:id` の回数は下がるはず。** watcher が最大20回のアイコン取得を挟むため1周が重くなる。下がり幅を記録する
2. **`GET /notifications` と `POST /auctions` の回数は大きく動かないはず。** これらのワーカーはアイコン取得の影響を受けない。ここが大きく動いていたら意図しない副作用が起きている
3. `LIVENESS: PASS` で採点7本すべてが floor に到達していること

**4-E1 のスコアと絶対値で比較して「下がったから悪い」と判断しないこと。** アイコン取得というコストを足したので下がるのが正しい。

- [ ] **Step 5: G5(生成物サイズ)**

```bash
du -h initial-data/out/*
du -sh initial-data/out
```

Task 2 Step 8 で控えた値と一致することを確認し、記録する。

**アイコン1枚あたりの実サイズ**は Task 1 Step 5 で控えた値を使う。あわせて、
ダンプの増分からの逆算が整合することも確認する:

```bash
cd /Users/abe/ghq/github.com/kyosu-1/isubid
git show HEAD~4:initial-data/out/91_users.sql | wc -c   # アイコン導入前(コミット数は実際に合わせる)
wc -c initial-data/out/91_users.sql                      # 導入後
```

増分をアイコンを持つユーザー数で割り、**16進なので2で割る**と1枚あたりの
おおよその実バイト数になる。Task 1 Step 5 の実測値と桁が合っていることを確認する
(区切り文字や `0x` の分だけ逆算のほうがやや大きく出る)。合わない場合は
原因を突き止めてから記録すること。

- [ ] **Step 6: `docs/phase4-notes.md` に 4-C のセクションを追記する**

以下を含めること。

- **設計判断**: アイコンを採点しない理由(コストであって報酬ではない。加点すると「重いアイコンを大量に配るほど点が入る」逆向きの誘導になる)、約1割を NULL にした理由(404 を 200 にする改悪の検出)、独立した乱数源を使った理由(共有 rng を消費すると 92〜94 のダンプが全部変わる)
- G1〜G5 の実測ログ(**ツールの実出力を転記**)
- 改悪5種それぞれの検出結果(実際のエラーメッセージつき)
- **生成物サイズの実測**(設計文書 §1 の表を埋める)
- **アイコン1枚あたりの実サイズ**
- 4-E への持ち越しがあれば追記

あわせて `/Users/abe/ghq/github.com/kyosu-1/isubid/.superpowers/sdd/2026-08-31-isubid-phase4c-icon-blob/progress.md`(コントローラーの台帳)を読み、`minor (deferred)` の行を軽微リストとして拾うこと。**台帳はセッションが終わると失われる。**

- [ ] **Step 7: 全モジュールの最終確認**

```bash
cd /Users/abe/ghq/github.com/kyosu-1/isubid
gofmt -l ./webapp/go ./bench ./initial-data
(cd webapp/go && go vet ./... && go test -count=1 ./...)
(cd bench && go vet ./... && go test -count=1 ./...)
(cd initial-data && go vet ./... && go test -count=1 ./...)
git status --short
```

Expected: gofmt 出力なし、vet クリーン、3モジュールとも `ok`、作業ツリーがクリーン(G3 の改悪を戻し忘れていないこと)。

- [ ] **Step 8: コミット**

```bash
git add docs/phase4-notes.md
git commit -m "docs: 4-Cのゲート実測と生成物サイズを記録する"
```

---

## Self-Review メモ

**Spec カバレッジ:**

| Spec セクション | 実装タスク |
|---|---|
| §1 画像(64×64・決定論的・id ごとに異なる) | Task 1 |
| §1 アイコンを持たないユーザー(約1割、シードは NULL のまま) | Task 2 |
| §1 SQL ダンプへの埋め込み・バッチ行数 | Task 2 |
| §1 生成物サイズへの影響 | Task 2 Step 8、Task 6 Step 5 |
| §2 エンドポイントと応答表 | Task 3 |
| §2 仕込み3点 | Task 3(1・2)、Task 5(3 = 一覧1ページで最大20回) |
| §3 スナップショットの拡張 | Task 2(生成側)、Task 4(読み取り側) |
| §3 Prepare | Task 4 |
| §3 Load | Task 5 |
| §4 アイコンを採点しない | Task 5(採点タグを作らない) |
| §5 影響範囲 | Task 1〜6 で全ファイルを網羅 |
| §6 完了ゲート G1〜G5 | Task 6 |
| §7 やらないこと | Task 5・Task 6 Step 6 |

**型の一貫性:** `iconSize` / `GenerateIcon` は Task 1 で定義し Task 2 が使う。`User.Icon` / `SnapshotUser` / `Snapshot.Users` は Task 2(生成側)と Task 4(読み取り側)で JSON タグを一致させる。`GetUserIcon` / `ValidateUserIcon` / `UserByID` は Task 4 で定義し Task 5 が使う。`doRaw` は Task 4 で定義し、Task 4 の Prepare(非数値 id の 400 検証)と Task 5 が間接的に使う。
