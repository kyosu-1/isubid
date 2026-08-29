# ISUBID Phase 4-A（初期データジェネレータ）実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 乱数シード固定・規模パラメータ化された初期データジェネレータを作り、フルサイズのデータでベンチが完走することを実測で確かめ、採用規模を決める。

**Architecture:** `initial-data/` に独立した Go モジュールを置き、SQL ダンプと正解スナップショット JSON を出力する。生成データは Phase 1 シード（id 1〜12）を置き換えず id 13 から continue する。読み込みは `webapp/sql/init.sh` が `mysql` クライアントで行い、環境変数による明示的オプトインとする。ベンチは Prepare の一覧検証をスナップショット照合へ移行する。

**Tech Stack:** Go 1.26 / MySQL 8 / isucandar / Docker Compose

**Spec:** `docs/superpowers/specs/2026-08-30-isubid-phase4a-initial-data-design.md`

## Global Constraints

- **意図的な遅さは仕様である。** インデックスを張らない、N+1 を解消しない、バルク化しない。生成データは「仕込みに走査対象を与える」ためのものであり、遅さを緩和するために使わない
- **生成データは Phase 1 シードを置き換えない。** id 1〜12（auctions）、1〜20（users）、1〜8（bids）はシードのまま。生成は必ずその次から採番する
- **生成データの読み込みは環境変数 `ISUBID_INITIAL_DATA_DIR` による明示的オプトイン。** `go test` はこれを設定しないため、`webapp/go` の既存テスト（27本）はシードのみで従来どおり通る必要がある
- **ベンチの `-snapshot` は既定値が空文字（生成データ非搭載モード）。** アプリ側の環境変数と対になる明示的オプトイン
- **生成データはベンチの不変条件を満たすこと。** 各オークション内で id 昇順に amount が厳密単調増加、`created_at` も id と同順、closed の `winning_price` = そのオークションの最大 amount かつ `winner_id` = その入札者
- **生成 live オークションの `ends_at` 順は id 順と相関しないこと。** `ORDER BY ends_at ASC` を `ORDER BY id ASC` に書き換える改変の検出力を、生成データの重みで薄めないため
- **シードユーザー（id 1〜20）を出品者にせず、宛先の通知も作らない。** `GET /stats/me` のシード期待値と `validateNotifications` の下限比較を汚さないため
- ベンチの入札者・出品者は引き続きシードユーザー 1〜20 のみを使う（プール拡大は 4-E）
- Error response bodies は `{"error": "..."}`（既存の `writeError`）
- テスト実行前提: `docker compose -f dev/compose.yaml up -d mysql`

## 仕様からの意図的な差分

計画を起こす過程で、spec の記述より堅牢にできると判明した2点。実装はこちらに従う。

1. **一覧の順序検証を「完全一致」から「2つの独立した性質」に変える。** Phase 3 は `initialAuctionOrder`（`4,2,8,6,10,1,3,5,7,9`）との完全一致で照合していた。生成データが入ると live は約260件になり、シードと生成分が `ends_at` 順で交互に並ぶため、完全一致は同着やミリ秒単位のズレで壊れる。代わりに (a)`ends_at` が非減少であること、(b) リストが id 昇順にソートされて**いない**こと、の2性質を検証する。`ORDER BY id ASC` への書き換えは (a) と (b) の両方に引っかかるため、検出力は落ちない
2. **スナップショットは全オークションを載せない。** spec は「各オークションの現在価格など」を持つとしていたが、Prepare が照合するのは一覧1ページ目・代表サンプル・総件数の3つだけである。全 live + 全 upcoming + サンプル対象の closed のみを載せれば足り、`full` でも約350エントリ（数百KB）に収まる。1万件を載せると JSON が数MBになり、ベンチの起動が重くなるだけで得るものがない

3. **`POST /initialize` は生成データ搭載時のみ `init.sh` へ委譲する。** spec は無条件に `init.sh` を実行する想定だったが、**このマシンのホストには `mysql` クライアントが無い**（`which mysql` で確認済み）。`webapp/go` のテスト27本は全て `initApp` 経由で `POST /initialize` を呼ぶため、無条件委譲にするとテストスイートが全滅する。`ISUBID_INITIAL_DATA_DIR` が設定されているときだけ `init.sh`（`mysql` クライアントでのバルクロード、コンテナ内で実行）を使い、未設定なら従来どおり Go でスキーマとシードを流す。生成データの有無を切り替える変数がそのまま読み込み経路も切り替えるので、スイッチは1つで済む

## File Structure

**新規作成**

| ファイル | 責務 |
|---|---|
| `initial-data/go.mod` | 独立モジュール（`bench/` と同じ構成） |
| `initial-data/config.go` | 規模定義（`Config`・`Scales`）、シード id 範囲の定数 |
| `initial-data/generate.go` | 生成ロジック（`Generate` と各テーブルの生成関数） |
| `initial-data/generate_test.go` | 生成データの不変条件テスト |
| `initial-data/sqlout.go` | マルチバリュー INSERT の書き出し |
| `initial-data/sqlout_test.go` | 出力形式のテスト |
| `initial-data/snapshot.go` | スナップショット JSON の構築と書き出し |
| `initial-data/snapshot_test.go` | スナップショットと生成データの整合テスト |
| `initial-data/main.go` | CLI（`-scale` `-seed` `-out`） |
| `initial-data/out/` | 生成物（コミットする） |
| `webapp/sql/init.sh` | schema + シード + （オプトイン時）生成データの投入 |
| `bench/snapshot.go` | スナップショットの読み込みと参照 |
| `bench/snapshot_test.go` | 同テスト |
| `docs/phase4-notes.md` | 4-A の設計判断とサイジング実測の記録 |

**変更**

| ファイル | 変更内容 |
|---|---|
| `webapp/go/initialize.go` | `init.sh` 実行方式へ刷新、生成データ向け一括 UPDATE |
| `webapp/go/Dockerfile` | `mysql` クライアントと `init.sh` の同梱 |
| `dev/compose.yaml` | `ISUBID_INITIAL_DATA_DIR` の設定、`initial-data/out` のマウント |
| `bench/main.go` | `-snapshot` フラグ |
| `bench/scenario.go` | Prepare のスナップショット照合分岐 |
| `bench/validate.go` | 一覧検証を2性質方式へ、スナップショット照合関数を追加 |

---

## 4-A-1: ジェネレータ本体

### Task 1: モジュール骨格と users 生成

**Files:**
- Create: `initial-data/go.mod`, `initial-data/config.go`, `initial-data/generate.go`, `initial-data/generate_test.go`

**Interfaces:**
- Consumes: なし
- Produces: `type Config`, `var Scales map[string]Config`, `const DefaultSeed`, `const SeedMaxUserID/SeedMaxAuctionID/SeedMaxBidID`, `const GeneratedPasswordHash`, `type User`, `type Dataset`, `func Generate(cfg Config) *Dataset`

- [ ] **Step 1: モジュールを作る**

```bash
mkdir -p initial-data
cd initial-data
go mod init github.com/kyosu-1/isubid/initial-data
go mod edit -go=1.26
```

- [ ] **Step 2: 失敗するテストを書く**

`initial-data/generate_test.go`:

```go
package main

import "testing"

func TestGenerateUsers(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	if len(ds.Users) != cfg.Users {
		t.Fatalf("users = %d, want %d", len(ds.Users), cfg.Users)
	}
	// 生成ユーザーは必ずシードの次から採番する
	if ds.Users[0].ID != SeedMaxUserID+1 {
		t.Errorf("最初の user id = %d, want %d", ds.Users[0].ID, SeedMaxUserID+1)
	}
	// id は連番で、name は id から決まる
	for i, u := range ds.Users {
		wantID := int64(SeedMaxUserID + 1 + i)
		if u.ID != wantID {
			t.Fatalf("users[%d].ID = %d, want %d", i, u.ID, wantID)
		}
		if u.Name != "gen_user_"+pad5(u.ID) {
			t.Fatalf("users[%d].Name = %q, want %q", i, u.Name, "gen_user_"+pad5(u.ID))
		}
	}
}

// 同じ scale と seed なら生成結果は完全に一致する。
func TestGenerateIsDeterministic(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	a := Generate(cfg)
	b := Generate(cfg)

	if len(a.Users) != len(b.Users) {
		t.Fatalf("users 件数が不一致: %d vs %d", len(a.Users), len(b.Users))
	}
	for i := range a.Users {
		if a.Users[i] != b.Users[i] {
			t.Fatalf("users[%d] が不一致: %+v vs %+v", i, a.Users[i], b.Users[i])
		}
	}
}
```

- [ ] **Step 3: テストが落ちることを確認**

Run: `cd initial-data && go test -count=1 ./...`
Expected: コンパイルエラー（`Scales` 等が未定義）

- [ ] **Step 4: 実装する**

`initial-data/config.go`:

```go
package main

// SeedMax* は webapp/sql/90_seed_phase1.sql が占める id 範囲の上端。
// 生成データは必ずこの次から採番する。シードを置き換えないのは、
// webapp/go のテスト27本がシードの具体値に依存しているため(spec 論点4)。
const (
	SeedMaxUserID    = 20
	SeedMaxAuctionID = 12
	SeedMaxBidID     = 8
)

// DefaultSeed は乱数シードの既定値。同じ scale と seed なら生成物は完全に一致する。
const DefaultSeed = int64(20260830)

// GeneratedPasswordHash は生成ユーザー全員で共有する bcrypt ハッシュ(平文 "password", cost 12)。
// 5,000回の bcrypt cost 12 は生成に数十分かかるうえ、ハッシュが個別であることに
// 検証上の意味がない。webapp/sql/90_seed_phase1.sql のシードユーザーと同じ値。
const GeneratedPasswordHash = "$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK"

// Config は生成する初期データの規模。
type Config struct {
	Name             string
	Users            int
	ClosedAuctions   int
	LiveAuctions     int
	UpcomingAuctions int
	Bids             int
	Seed             int64
}

// Scales は段階的サイジング用の名前付き規模。full が親設計ドキュメントの目標値。
// 採用規模は実測ゲート(計画末尾の 4-A-4)で決める。
var Scales = map[string]Config{
	"small":  {Name: "small", Users: 500, ClosedAuctions: 1000, LiveAuctions: 50, UpcomingAuctions: 25, Bids: 30000},
	"medium": {Name: "medium", Users: 2000, ClosedAuctions: 4000, LiveAuctions: 100, UpcomingAuctions: 50, Bids: 120000},
	"full":   {Name: "full", Users: 5000, ClosedAuctions: 10000, LiveAuctions: 200, UpcomingAuctions: 100, Bids: 300000},
}
```

`initial-data/generate.go`:

```go
package main

import (
	"fmt"
	"math/rand"
)

type User struct {
	ID   int64
	Name string
}

// Dataset は1回の生成で得られる全データ。
type Dataset struct {
	Config Config
	Users  []User
}

func pad5(n int64) string {
	return fmt.Sprintf("%05d", n)
}

// Generate は cfg に従って初期データを生成する。
// cfg.Seed で初期化した単一の *rand.Rand を全生成で共有するため、
// 同じ cfg なら結果は完全に一致する。
func Generate(cfg Config) *Dataset {
	rng := rand.New(rand.NewSource(cfg.Seed))
	ds := &Dataset{Config: cfg}
	ds.Users = generateUsers(cfg)
	_ = rng // 後続タスクで auctions / bids の生成に使う
	return ds
}

func generateUsers(cfg Config) []User {
	users := make([]User, 0, cfg.Users)
	for i := 0; i < cfg.Users; i++ {
		id := int64(SeedMaxUserID + 1 + i)
		users = append(users, User{ID: id, Name: "gen_user_" + pad5(id)})
	}
	return users
}
```

- [ ] **Step 5: テストが通ることを確認**

Run: `cd initial-data && go test -count=1 ./...`
Expected: PASS

- [ ] **Step 6: コミット**

```bash
git add initial-data/
git commit -m "feat: 初期データジェネレータのモジュール骨格とusers生成を追加"
```

---

### Task 2: auctions 生成

**Files:**
- Modify: `initial-data/generate.go`, `initial-data/generate_test.go`

**Interfaces:**
- Consumes: `Config`, `User`, `SeedMaxAuctionID`, `Dataset`
- Produces: `type Auction`, `var generatedEpoch time.Time`, `func generateAuctions(cfg Config, rng *rand.Rand, users []User) []Auction`、`Dataset.Auctions []Auction`

- [ ] **Step 1: 失敗するテストを書く**

`initial-data/generate_test.go` に追記:

```go
// live の ends_at 順が id 順と相関していると、一覧の ORDER BY ends_at ASC を
// ORDER BY id ASC に書き換える改変を検出できなくなる。Phase 3 が
// initialAuctionOrder で作った性質を、生成データでも保つ必要がある。
func TestGeneratedLiveEndsAtNotCorrelatedWithID(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	var live []Auction
	for _, a := range ds.Auctions {
		if a.Status == "live" {
			live = append(live, a)
		}
	}
	if len(live) != cfg.LiveAuctions {
		t.Fatalf("live = %d件, want %d", len(live), cfg.LiveAuctions)
	}
	// live は id 昇順で並んでいる前提。その並びで ends_at が昇順ソート済みなら相関している
	sorted := sort.SliceIsSorted(live, func(i, j int) bool {
		return live[i].EndsAt.Before(live[j].EndsAt)
	})
	if sorted {
		t.Error("live の ends_at が id 順と相関している(ORDER BY id ASC を検出できなくなる)")
	}
}

// ends_at のオフセットが重複すると一覧の順序が一意に決まらない。
func TestGeneratedLiveEndsAtAreUnique(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	seen := map[int64]bool{}
	for _, a := range ds.Auctions {
		if a.Status != "live" {
			continue
		}
		off := int64(a.EndsAt.Sub(generatedEpoch).Seconds())
		if seen[off] {
			t.Fatalf("ends_at オフセット %d が重複している", off)
		}
		seen[off] = true
	}
}

func TestGeneratedAuctionStatusesAndIDs(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	want := cfg.ClosedAuctions + cfg.LiveAuctions + cfg.UpcomingAuctions
	if len(ds.Auctions) != want {
		t.Fatalf("auctions = %d件, want %d", len(ds.Auctions), want)
	}
	if ds.Auctions[0].ID != SeedMaxAuctionID+1 {
		t.Errorf("最初の auction id = %d, want %d", ds.Auctions[0].ID, SeedMaxAuctionID+1)
	}

	counts := map[string]int{}
	sellerIDs := map[int64]bool{}
	for i, a := range ds.Auctions {
		if a.ID != int64(SeedMaxAuctionID+1+i) {
			t.Fatalf("auctions[%d].ID = %d, want %d", i, a.ID, SeedMaxAuctionID+1+i)
		}
		counts[a.Status]++
		sellerIDs[a.SellerID] = true
		if a.StartingPrice < 1 {
			t.Fatalf("auctions[%d].StartingPrice = %d", i, a.StartingPrice)
		}
		if a.CategoryID < 1 || a.CategoryID > 3 {
			t.Fatalf("auctions[%d].CategoryID = %d (シードのカテゴリは1〜3)", i, a.CategoryID)
		}
	}
	if counts["closed"] != cfg.ClosedAuctions || counts["live"] != cfg.LiveAuctions || counts["upcoming"] != cfg.UpcomingAuctions {
		t.Errorf("status 内訳 = %+v", counts)
	}
	// シードユーザーを出品者にすると GET /stats/me のシード期待値が壊れる
	for id := range sellerIDs {
		if id <= SeedMaxUserID {
			t.Errorf("シードユーザー %d が出品者になっている", id)
		}
	}
}

// closed は過去の絶対時刻。走行時刻に依存しないため initialize で書き換えない。
func TestGeneratedClosedAuctionsAreInThePast(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, a := range ds.Auctions {
		if a.Status != "closed" {
			continue
		}
		if !a.EndsAt.Before(cutoff) {
			t.Fatalf("closed auction %d の ends_at が %v (期待: %v より前)", a.ID, a.EndsAt, cutoff)
		}
	}
}
```

`import` に `"sort"` と `"time"` を追加。

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd initial-data && go test -count=1 -run TestGeneratedAuction ./...`
Expected: コンパイルエラー（`Auction` / `generatedEpoch` が未定義）

- [ ] **Step 3: 実装する**

`initial-data/generate.go` に追記（`import` に `"time"` を追加）:

```go
// generatedEpoch は live / upcoming の時刻を保持する固定基準。
// POST /initialize がこの起点からのオフセットを現在時刻へ付け替える(spec 論点2)。
// closed は過去データなので書き換えず、絶対時刻のまま置く。
var generatedEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// closedBaseTime は closed オークションの ends_at を配置する基準。
// 走行時刻に依存しない過去の絶対時刻。
var closedBaseTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

type Auction struct {
	ID            int64
	SellerID      int64
	CategoryID    int64
	Title         string
	Description   string
	StartingPrice int64
	StartsAt      time.Time
	EndsAt        time.Time
	Status        string // "closed" | "live" | "upcoming"
	WinnerID      *int64 // closed かつ入札ありのときのみ。Task 3 で埋める
	WinningPrice  *int64
}

// chairNames / chairDescs は生成タイトルの素材。
// SQL リテラルへ素で埋め込むため、クォートやバックスラッシュを含めないこと
// (sqlout_test.go がこれを検証する)。
var chairNames = []string{
	"ヘリテージ・ラウンジ", "エルゴフロー", "ISUクラシック", "メッシュワークス",
	"アンティーク・ウィング", "ゲーミングエッジ", "スタンドアップ", "コンパクトシート",
	"ベルベット・オットマン", "スカンジ・ダイニング",
}

var chairDescs = []string{
	"長時間の作業に向いた一脚", "職人による手作業の仕上げ", "通気性に優れた背面メッシュ",
	"程よい硬さの座面", "折りたたみ可能な省スペース設計",
}

// generateAuctions は closed / live / upcoming を id 昇順で連続採番して返す。
//
// live の ends_at は generatedEpoch からのオフセットで表す。オフセットは
// 「走行中に閉じる短いもの」と「走行後も残る長いもの」を混ぜたうえでシャッフルし、
// id 順と相関しないようにする。相関していると一覧の ORDER BY ends_at ASC を
// ORDER BY id ASC に書き換える改変を検出できなくなる。
func generateAuctions(cfg Config, rng *rand.Rand, users []User) []Auction {
	total := cfg.ClosedAuctions + cfg.LiveAuctions + cfg.UpcomingAuctions
	auctions := make([]Auction, 0, total)

	// live の ends_at オフセット。重複しないよう等差で作ってからシャッフルする。
	liveOffsets := make([]int, cfg.LiveAuctions)
	for i := range liveOffsets {
		if i%2 == 0 {
			liveOffsets[i] = 15 + i*3 // 走行中(60秒)に一部が閉じる
		} else {
			liveOffsets[i] = 3600 + i*7 // 走行を通して live のまま
		}
	}
	rng.Shuffle(len(liveOffsets), func(i, j int) {
		liveOffsets[i], liveOffsets[j] = liveOffsets[j], liveOffsets[i]
	})

	statuses := make([]string, 0, total)
	for i := 0; i < cfg.ClosedAuctions; i++ {
		statuses = append(statuses, "closed")
	}
	for i := 0; i < cfg.LiveAuctions; i++ {
		statuses = append(statuses, "live")
	}
	for i := 0; i < cfg.UpcomingAuctions; i++ {
		statuses = append(statuses, "upcoming")
	}

	liveIdx := 0
	for i, status := range statuses {
		id := int64(SeedMaxAuctionID + 1 + i)
		a := Auction{
			ID:            id,
			SellerID:      users[rng.Intn(len(users))].ID,
			CategoryID:    int64(1 + rng.Intn(3)),
			Title:         chairNames[rng.Intn(len(chairNames))] + " " + pad5(id),
			Description:   chairDescs[rng.Intn(len(chairDescs))],
			StartingPrice: int64(1000 + rng.Intn(19)*500),
			Status:        status,
		}
		switch status {
		case "closed":
			// 過去に分散配置。走行時刻に依存しない絶対時刻。
			end := closedBaseTime.Add(time.Duration(rng.Intn(180*24)) * time.Hour)
			a.StartsAt = end.Add(-72 * time.Hour)
			a.EndsAt = end
		case "live":
			off := liveOffsets[liveIdx]
			liveIdx++
			a.StartsAt = generatedEpoch.Add(-1 * time.Hour)
			a.EndsAt = generatedEpoch.Add(time.Duration(off) * time.Second)
		case "upcoming":
			// 走行中ずっと upcoming のまま
			a.StartsAt = generatedEpoch.Add(time.Duration(3600+i) * time.Second)
			a.EndsAt = generatedEpoch.Add(time.Duration(7200+i) * time.Second)
		}
		auctions = append(auctions, a)
	}
	return auctions
}
```

`Dataset` に `Auctions []Auction` を追加し、`Generate` を更新:

```go
	ds.Auctions = generateAuctions(cfg, rng, ds.Users)
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd initial-data && go test -count=1 ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add initial-data/
git commit -m "feat: ジェネレータのauctions生成(ends_atとidの非相関を含む)を追加"
```

---

### Task 3: bids 生成と落札結果のバックフィル

**Files:**
- Modify: `initial-data/generate.go`, `initial-data/generate_test.go`

**Interfaces:**
- Consumes: `Auction`, `User`, `SeedMaxBidID`
- Produces: `type Bid`, `func generateBids(cfg Config, rng *rand.Rand, auctions []Auction, users []User) []Bid`, `func backfillWinners(auctions []Auction, bids []Bid)`, `Dataset.Bids []Bid`

- [ ] **Step 1: 失敗するテストを書く**

`initial-data/generate_test.go` に追記:

```go
// ベンチの ValidateBidsInvariant / ValidateFeedPage は
// 「各オークション内で id 昇順に amount が厳密単調増加」を検証する。
// 生成データがこれを破ると Prepare とウォッチャーが即 critical を出す。
func TestGeneratedBidsAreMonotonicPerAuction(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	byAuction := map[int64][]Bid{}
	for _, b := range ds.Bids {
		byAuction[b.AuctionID] = append(byAuction[b.AuctionID], b)
	}
	for auctionID, bids := range byAuction {
		for i := 1; i < len(bids); i++ {
			prev, cur := bids[i-1], bids[i]
			if cur.ID <= prev.ID {
				t.Fatalf("auction %d: bid id が昇順でない (%d の次が %d)", auctionID, prev.ID, cur.ID)
			}
			if cur.Amount <= prev.Amount {
				t.Fatalf("auction %d: amount が厳密単調増加でない (id=%d amount=%d の次が id=%d amount=%d)",
					auctionID, prev.ID, prev.Amount, cur.ID, cur.Amount)
			}
			if !cur.CreatedAt.After(prev.CreatedAt) {
				t.Fatalf("auction %d: created_at が id と同順でない (id=%d %v の次が id=%d %v)",
					auctionID, prev.ID, prev.CreatedAt, cur.ID, cur.CreatedAt)
			}
			if cur.UserID == prev.UserID {
				t.Fatalf("auction %d: 同一ユーザーが連続して入札している (user=%d)", auctionID, cur.UserID)
			}
		}
	}
}

// ベンチの reconcileClosedAuction は winner = argmax(bids) を厳密に検証する。
func TestGeneratedClosedAuctionWinnersMatchMaxBid(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	maxBid := map[int64]Bid{}
	for _, b := range ds.Bids {
		if cur, ok := maxBid[b.AuctionID]; !ok || b.Amount > cur.Amount {
			maxBid[b.AuctionID] = b
		}
	}
	for _, a := range ds.Auctions {
		if a.Status != "closed" {
			if a.WinnerID != nil || a.WinningPrice != nil {
				t.Fatalf("auction %d (%s) に落札者が設定されている", a.ID, a.Status)
			}
			continue
		}
		top, hasBid := maxBid[a.ID]
		if !hasBid {
			if a.WinnerID != nil || a.WinningPrice != nil {
				t.Fatalf("auction %d: 入札0件なのに落札者がいる", a.ID)
			}
			continue
		}
		if a.WinnerID == nil || *a.WinnerID != top.UserID {
			t.Fatalf("auction %d: winner_id = %v, want %d", a.ID, a.WinnerID, top.UserID)
		}
		if a.WinningPrice == nil || *a.WinningPrice != top.Amount {
			t.Fatalf("auction %d: winning_price = %v, want %d", a.ID, a.WinningPrice, top.Amount)
		}
	}
}

func TestGeneratedBidsCountAndIDs(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	if len(ds.Bids) != cfg.Bids {
		t.Fatalf("bids = %d件, want %d", len(ds.Bids), cfg.Bids)
	}
	if ds.Bids[0].ID != SeedMaxBidID+1 {
		t.Errorf("最初の bid id = %d, want %d", ds.Bids[0].ID, SeedMaxBidID+1)
	}
	// upcoming には入札しない
	status := map[int64]string{}
	for _, a := range ds.Auctions {
		status[a.ID] = a.Status
	}
	for _, b := range ds.Bids {
		if status[b.AuctionID] == "upcoming" {
			t.Fatalf("upcoming の auction %d に入札がある", b.AuctionID)
		}
		if b.UserID <= SeedMaxUserID {
			t.Fatalf("bid %d の入札者がシードユーザー %d", b.ID, b.UserID)
		}
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd initial-data && go test -count=1 -run TestGeneratedBids ./...`
Expected: コンパイルエラー（`Bid` が未定義）

- [ ] **Step 3: 実装する**

`initial-data/generate.go` に追記:

```go
type Bid struct {
	ID        int64
	AuctionID int64
	UserID    int64
	Amount    int64
	CreatedAt time.Time
}

// generateBids は closed / live オークションへ入札を分配する。
//
// 各オークション内では id 昇順に amount が厳密単調増加し、created_at も同順になる。
// これはベンチの ValidateBidsInvariant / ValidateFeedPage が検証する不変条件であり、
// 破ると Prepare とウォッチャーが即 critical を出す。
// 連続する入札は必ず別ユーザーにする(現実的であり、通知ファンアウトの重みも生む)。
func generateBids(cfg Config, rng *rand.Rand, auctions []Auction, users []User) []Bid {
	// 入札対象は closed と live のみ
	targets := make([]int, 0, len(auctions))
	for i, a := range auctions {
		if a.Status == "closed" || a.Status == "live" {
			targets = append(targets, i)
		}
	}
	if len(targets) == 0 {
		return nil
	}

	// 各対象への入札数を決める。均等割りしたうえで剰余を先頭から配ることで、
	// 合計が必ず cfg.Bids に一致する。
	counts := make([]int, len(targets))
	base := cfg.Bids / len(targets)
	rest := cfg.Bids % len(targets)
	for i := range counts {
		counts[i] = base
		if i < rest {
			counts[i]++
		}
	}
	// 偏りを作る: 隣接するペアで入札数をやり取りする(合計は保存される)
	for i := 0; i+1 < len(counts); i += 2 {
		move := rng.Intn(counts[i]/2 + 1)
		counts[i] -= move
		counts[i+1] += move
	}

	bids := make([]Bid, 0, cfg.Bids)
	nextID := int64(SeedMaxBidID + 1)
	for k, idx := range targets {
		a := &auctions[idx]
		amount := a.StartingPrice
		created := a.StartsAt
		var prevUser int64
		for j := 0; j < counts[k]; j++ {
			amount += int64(100 + rng.Intn(400))
			created = created.Add(time.Duration(1+rng.Intn(60)) * time.Second)

			u := users[rng.Intn(len(users))].ID
			for u == prevUser {
				u = users[rng.Intn(len(users))].ID
			}
			prevUser = u

			bids = append(bids, Bid{
				ID: nextID, AuctionID: a.ID, UserID: u,
				Amount: amount, CreatedAt: created,
			})
			nextID++
		}
	}
	return bids
}

// backfillWinners は closed オークションの winner_id / winning_price を、
// そのオークションの最大 amount の入札から埋める。
// ベンチの reconcileClosedAuction が winner = argmax(bids) を厳密に検証するため、
// 生成データもこの不変条件を満たす必要がある。
func backfillWinners(auctions []Auction, bids []Bid) {
	top := map[int64]Bid{}
	for _, b := range bids {
		if cur, ok := top[b.AuctionID]; !ok || b.Amount > cur.Amount {
			top[b.AuctionID] = b
		}
	}
	for i := range auctions {
		a := &auctions[i]
		if a.Status != "closed" {
			continue
		}
		t, ok := top[a.ID]
		if !ok {
			continue // 入札0件のまま closed
		}
		winner, price := t.UserID, t.Amount
		a.WinnerID = &winner
		a.WinningPrice = &price
	}
}
```

`Dataset` に `Bids []Bid` を追加し、`Generate` を更新（`_ = rng` は削除）:

```go
	ds.Auctions = generateAuctions(cfg, rng, ds.Users)
	ds.Bids = generateBids(cfg, rng, ds.Auctions, ds.Users)
	backfillWinners(ds.Auctions, ds.Bids)
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd initial-data && go test -count=1 ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add initial-data/
git commit -m "feat: ジェネレータのbids生成と落札結果のバックフィルを追加"
```

---

### Task 4: notifications 生成

**Files:**
- Modify: `initial-data/generate.go`, `initial-data/generate_test.go`

**Interfaces:**
- Consumes: `Auction`, `Bid`, `SeedMaxUserID`
- Produces: `type Notification`, `func generateNotifications(auctions []Auction, bids []Bid) []Notification`, `func truncateForNotification(title string) string`, `Dataset.Notifications []Notification`

- [ ] **Step 1: 失敗するテストを書く**

`initial-data/generate_test.go` に追記:

```go
// 通知は生成ユーザー宛のみ。シードユーザー宛を作ると
// GET /notifications の応答が初手から巨大になり、通知確認シナリオのコストが
// 実データではなく生成データに支配される。
func TestGeneratedNotificationsTargetGeneratedUsersOnly(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	if len(ds.Notifications) == 0 {
		t.Fatal("通知が1件も生成されていない(user_id フルスキャンに走査対象を与える目的が果たせない)")
	}
	for _, n := range ds.Notifications {
		if n.UserID <= SeedMaxUserID {
			t.Fatalf("シードユーザー %d 宛の通知が生成されている (notification %d)", n.UserID, n.ID)
		}
		if n.Type != "outbid" && n.Type != "won" {
			t.Fatalf("notification %d: type = %q", n.ID, n.Type)
		}
	}
}

// won 通知は「落札者が確定した closed オークション」にだけ、その落札者宛に1件。
func TestGeneratedWonNotificationsMatchWinners(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	wonBy := map[int64][]int64{} // auctionID -> userIDs
	for _, n := range ds.Notifications {
		if n.Type == "won" {
			wonBy[n.AuctionID] = append(wonBy[n.AuctionID], n.UserID)
		}
	}
	for _, a := range ds.Auctions {
		got := wonBy[a.ID]
		if a.Status != "closed" || a.WinnerID == nil {
			if len(got) != 0 {
				t.Fatalf("auction %d (status=%s winner=%v) に won 通知が %d件", a.ID, a.Status, a.WinnerID, len(got))
			}
			continue
		}
		if len(got) != 1 || got[0] != *a.WinnerID {
			t.Fatalf("auction %d: won 通知が %v (期待: [%d] の1件)", a.ID, got, *a.WinnerID)
		}
	}
}

// notifications.message は VARCHAR(255)。Phase 3 で、溢れると closeAuction の
// トランザクションがロールバックしオークションが永久に live のまま残る問題を修正した。
// 生成データも同じ制約を満たす必要がある。
func TestGeneratedNotificationMessagesFitColumn(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	for _, n := range ds.Notifications {
		if r := len([]rune(n.Message)); r > 255 {
			t.Fatalf("notification %d の message が %d文字 (VARCHAR(255) を超える)", n.ID, r)
		}
		if n.Message == "" {
			t.Fatalf("notification %d の message が空", n.ID)
		}
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd initial-data && go test -count=1 -run TestGeneratedNotification ./...`
Expected: コンパイルエラー（`Notification` が未定義）

- [ ] **Step 3: 実装する**

`initial-data/generate.go` に追記:

```go
type Notification struct {
	ID        int64
	UserID    int64
	Type      string // "outbid" | "won"
	AuctionID int64
	Message   string
	CreatedAt time.Time
}

// notificationTitleMaxRunes は通知文面に埋め込むタイトルの上限。
// notifications.message は VARCHAR(255) で、最も長い接尾辞
// 「」で他のユーザーに競り負けました は17文字。webapp/go 側の同名定数と揃えること。
const notificationTitleMaxRunes = 200

func truncateForNotification(title string) string {
	r := []rune(title)
	if len(r) <= notificationTitleMaxRunes {
		return title
	}
	return string(r[:notificationTitleMaxRunes])
}

// generateNotifications は closed オークションの落札結果から過去の通知を作る。
//
// 目的は notifications テーブルの user_id フルスキャン(インデックス無し)に
// 走査対象を与えることであり、宛先は生成ユーザーに限る。シードユーザー宛を作ると
// GET /notifications の応答が初手から巨大になり、通知確認シナリオのコストが
// 実データではなく生成データに支配される。
//
// 1オークションにつき、落札者へ won を1件、他の入札者(最大3人)へ outbid を1件ずつ。
func generateNotifications(auctions []Auction, bids []Bid) []Notification {
	bidders := map[int64][]int64{} // auctionID -> 入札した user_id(重複除去済み、初出順)
	seen := map[[2]int64]bool{}
	for _, b := range bids {
		key := [2]int64{b.AuctionID, b.UserID}
		if seen[key] {
			continue
		}
		seen[key] = true
		bidders[b.AuctionID] = append(bidders[b.AuctionID], b.UserID)
	}

	const maxOutbidPerAuction = 3
	var out []Notification
	nextID := int64(1) // シードは notifications を持たない
	for i := range auctions {
		a := &auctions[i]
		if a.Status != "closed" || a.WinnerID == nil {
			continue
		}
		title := truncateForNotification(a.Title)

		out = append(out, Notification{
			ID: nextID, UserID: *a.WinnerID, Type: "won", AuctionID: a.ID,
			Message: "「" + title + "」を落札しました", CreatedAt: a.EndsAt,
		})
		nextID++

		n := 0
		for _, uid := range bidders[a.ID] {
			if uid == *a.WinnerID {
				continue
			}
			if n >= maxOutbidPerAuction {
				break
			}
			out = append(out, Notification{
				ID: nextID, UserID: uid, Type: "outbid", AuctionID: a.ID,
				Message: "「" + title + "」で他のユーザーに競り負けました", CreatedAt: a.EndsAt,
			})
			nextID++
			n++
		}
	}
	return out
}
```

`Dataset` に `Notifications []Notification` を追加し、`Generate` を更新:

```go
	ds.Notifications = generateNotifications(ds.Auctions, ds.Bids)
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd initial-data && go test -count=1 ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add initial-data/
git commit -m "feat: ジェネレータのnotifications生成を追加"
```

---

### Task 5: SQL ダンプの書き出し

**Files:**
- Create: `initial-data/sqlout.go`, `initial-data/sqlout_test.go`

**Interfaces:**
- Consumes: `Dataset`, `User`, `Auction`, `Bid`, `Notification`, `GeneratedPasswordHash`
- Produces: `func WriteSQL(dir string, ds *Dataset) error`、出力ファイル `91_users.sql` / `92_auctions.sql` / `93_bids.sql` / `94_notifications.sql`

- [ ] **Step 1: 失敗するテストを書く**

`initial-data/sqlout_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 生成文字列を SQL リテラルへ素で埋め込むため、クォートやバックスラッシュを
// 含まないことを保証する。含む素材を足したらこのテストが落ちる。
func TestGeneratedStringsAreSQLSafe(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	bad := func(s string) bool {
		return strings.ContainsAny(s, "'\"\\")
	}
	for _, u := range ds.Users {
		if bad(u.Name) {
			t.Fatalf("user %d の name にクォート/バックスラッシュ: %q", u.ID, u.Name)
		}
	}
	for _, a := range ds.Auctions {
		if bad(a.Title) || bad(a.Description) {
			t.Fatalf("auction %d の title/description にクォート/バックスラッシュ: %q / %q", a.ID, a.Title, a.Description)
		}
	}
	for _, n := range ds.Notifications {
		if bad(n.Message) {
			t.Fatalf("notification %d の message にクォート/バックスラッシュ: %q", n.ID, n.Message)
		}
	}
}

func TestWriteSQLProducesLoadableFiles(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	dir := t.TempDir()
	if err := WriteSQL(dir, ds); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"91_users.sql", "92_auctions.sql", "93_bids.sql", "94_notifications.sql"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		s := string(b)
		if !strings.HasPrefix(s, "INSERT INTO ") {
			t.Errorf("%s: INSERT で始まっていない", name)
		}
		if !strings.HasSuffix(strings.TrimSpace(s), ";") {
			t.Errorf("%s: セミコロンで終わっていない", name)
		}
		// マルチバリュー INSERT であること(1行1文だと読み込みが桁違いに遅い)
		stmts := strings.Count(s, "INSERT INTO ")
		rows := strings.Count(s, "),\n(") + stmts
		if rows <= stmts {
			t.Errorf("%s: 1文あたり1行しかない (stmts=%d rows=%d)", name, stmts, rows)
		}
	}

	// closed の winner は NULL でない値として、live/upcoming は NULL として出る
	b, err := os.ReadFile(filepath.Join(dir, "92_auctions.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "NULL") {
		t.Error("92_auctions.sql に NULL(未落札の winner_id) が現れない")
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd initial-data && go test -count=1 -run TestWriteSQL ./...`
Expected: コンパイルエラー（`WriteSQL` が未定義）

- [ ] **Step 3: 実装する**

`initial-data/sqlout.go`:

```go
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// rowsPerStatement は1つの INSERT 文に載せる行数。
// 1行1文にすると30万行の読み込みが桁違いに遅くなる。
const rowsPerStatement = 1000

const mysqlTimeLayout = "2006-01-02 15:04:05.000000"

// WriteSQL は生成データを dir 配下の SQL ファイルへ書き出す。
func WriteSQL(dir string, ds *Dataset) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	type job struct {
		name string
		fn   func(*bufio.Writer, *Dataset) error
	}
	for _, j := range []job{
		{"91_users.sql", writeUsers},
		{"92_auctions.sql", writeAuctions},
		{"93_bids.sql", writeBids},
		{"94_notifications.sql", writeNotifications},
	} {
		f, err := os.Create(filepath.Join(dir, j.name))
		if err != nil {
			return err
		}
		w := bufio.NewWriterSize(f, 1<<20)
		if err := j.fn(w, ds); err != nil {
			f.Close()
			return err
		}
		if err := w.Flush(); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

// writeChunked は n 行を rowsPerStatement ごとに区切って INSERT 文を書く。
// row(i) は i 番目の行の "(...)" 部分を返す。
func writeChunked(w *bufio.Writer, header string, n int, row func(i int) string) error {
	if n == 0 {
		// 空でも構文として成立するファイルにしておく(mysql が読んでも何もしない)
		_, err := fmt.Fprintf(w, "-- no rows\nSELECT 1;\n")
		return err
	}
	for start := 0; start < n; start += rowsPerStatement {
		end := start + rowsPerStatement
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

func writeUsers(w *bufio.Writer, ds *Dataset) error {
	return writeChunked(w, "INSERT INTO users (id, name, password_hash) VALUES\n",
		len(ds.Users), func(i int) string {
			u := ds.Users[i]
			return fmt.Sprintf("(%d, '%s', '%s')", u.ID, u.Name, GeneratedPasswordHash)
		})
}

func nullInt64SQL(v *int64) string {
	if v == nil {
		return "NULL"
	}
	return fmt.Sprintf("%d", *v)
}

func writeAuctions(w *bufio.Writer, ds *Dataset) error {
	header := "INSERT INTO auctions (id, seller_id, category_id, title, description, starting_price, starts_at, ends_at, status, winner_id, winning_price) VALUES\n"
	return writeChunked(w, header, len(ds.Auctions), func(i int) string {
		a := ds.Auctions[i]
		return fmt.Sprintf("(%d, %d, %d, '%s', '%s', %d, '%s', '%s', '%s', %s, %s)",
			a.ID, a.SellerID, a.CategoryID, a.Title, a.Description, a.StartingPrice,
			a.StartsAt.Format(mysqlTimeLayout), a.EndsAt.Format(mysqlTimeLayout), a.Status,
			nullInt64SQL(a.WinnerID), nullInt64SQL(a.WinningPrice))
	})
}

func writeBids(w *bufio.Writer, ds *Dataset) error {
	header := "INSERT INTO bids (id, auction_id, user_id, amount, created_at) VALUES\n"
	return writeChunked(w, header, len(ds.Bids), func(i int) string {
		b := ds.Bids[i]
		return fmt.Sprintf("(%d, %d, %d, %d, '%s')",
			b.ID, b.AuctionID, b.UserID, b.Amount, b.CreatedAt.Format(mysqlTimeLayout))
	})
}

func writeNotifications(w *bufio.Writer, ds *Dataset) error {
	header := "INSERT INTO notifications (id, user_id, type, auction_id, message, is_read, created_at) VALUES\n"
	return writeChunked(w, header, len(ds.Notifications), func(i int) string {
		n := ds.Notifications[i]
		return fmt.Sprintf("(%d, %d, '%s', %d, '%s', 0, '%s')",
			n.ID, n.UserID, n.Type, n.AuctionID, n.Message, n.CreatedAt.Format(mysqlTimeLayout))
	})
}

var _ = time.Time{}
```

（`var _ = time.Time{}` は `time` が未使用になった場合の保険。実際に使っていれば削除してよい。）

- [ ] **Step 4: テストが通ることを確認**

Run: `cd initial-data && go test -count=1 ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add initial-data/
git commit -m "feat: ジェネレータのSQLダンプ出力(マルチバリューINSERT)を追加"
```

---

### Task 6: スナップショット JSON と CLI

**Files:**
- Create: `initial-data/snapshot.go`, `initial-data/snapshot_test.go`, `initial-data/main.go`

**Interfaces:**
- Consumes: `Dataset`, `generatedEpoch`
- Produces: `type Snapshot`, `type SnapshotAuction`, `func BuildSnapshot(ds *Dataset) *Snapshot`, `func WriteSnapshot(dir string, snap *Snapshot) error`。**このJSON形式は `bench/snapshot.go`（Task 9）が読む側として対になる**

- [ ] **Step 1: 失敗するテストを書く**

`initial-data/snapshot_test.go`:

```go
package main

import "testing"

// スナップショットは全オークションを載せない。Prepare が照合するのは
// 一覧1ページ目・代表サンプル・総件数の3つだけなので、
// 全 live + 全 upcoming + サンプル対象の closed のみで足りる。
func TestBuildSnapshotContents(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)
	snap := BuildSnapshot(ds)

	if snap.Scale != cfg.Name || snap.Seed != cfg.Seed {
		t.Errorf("scale/seed = %q/%d, want %q/%d", snap.Scale, snap.Seed, cfg.Name, cfg.Seed)
	}
	if snap.Counts.Auctions != int64(len(ds.Auctions)) ||
		snap.Counts.Bids != int64(len(ds.Bids)) ||
		snap.Counts.Users != int64(len(ds.Users)) {
		t.Errorf("counts = %+v", snap.Counts)
	}
	if snap.Counts.LiveAuctions != int64(cfg.LiveAuctions) {
		t.Errorf("counts.live_auctions = %d, want %d", snap.Counts.LiveAuctions, cfg.LiveAuctions)
	}

	byID := map[int64]SnapshotAuction{}
	statuses := map[string]int{}
	for _, a := range snap.Auctions {
		byID[a.ID] = a
		statuses[a.Status]++
	}
	if statuses["live"] != cfg.LiveAuctions {
		t.Errorf("snapshot の live = %d件, want %d (全件載せる)", statuses["live"], cfg.LiveAuctions)
	}
	if statuses["upcoming"] != cfg.UpcomingAuctions {
		t.Errorf("snapshot の upcoming = %d件, want %d (全件載せる)", statuses["upcoming"], cfg.UpcomingAuctions)
	}
	if statuses["closed"] == 0 || statuses["closed"] >= cfg.ClosedAuctions {
		t.Errorf("snapshot の closed = %d件 (期待: 1件以上、かつ全件(%d)未満のサンプル)", statuses["closed"], cfg.ClosedAuctions)
	}
	if len(snap.SampleAuctionIDs) == 0 {
		t.Fatal("sample_auction_ids が空")
	}
	for _, id := range snap.SampleAuctionIDs {
		if _, ok := byID[id]; !ok {
			t.Fatalf("sample_auction_ids の %d が snapshot.auctions に無い", id)
		}
	}
}

// スナップショットの current_price / bid_count が、生成した bids から
// 導かれる値と一致すること。別ロジックで作られていないことの確認。
func TestSnapshotMatchesGeneratedBids(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)
	snap := BuildSnapshot(ds)

	count := map[int64]int64{}
	max := map[int64]int64{}
	for _, b := range ds.Bids {
		count[b.AuctionID]++
		if b.Amount > max[b.AuctionID] {
			max[b.AuctionID] = b.Amount
		}
	}
	start := map[int64]int64{}
	for _, a := range ds.Auctions {
		start[a.ID] = a.StartingPrice
	}

	for _, sa := range snap.Auctions {
		if sa.BidCount != count[sa.ID] {
			t.Fatalf("auction %d: snapshot bid_count = %d, 生成bidsからは %d", sa.ID, sa.BidCount, count[sa.ID])
		}
		want := start[sa.ID]
		if m := max[sa.ID]; m > want {
			want = m
		}
		if sa.CurrentPrice != want {
			t.Fatalf("auction %d: snapshot current_price = %d, 生成bidsからは %d", sa.ID, sa.CurrentPrice, want)
		}
	}
}

// live/upcoming は generatedEpoch からのオフセットを持ち、closed は 0。
func TestSnapshotEndsAtOffsets(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)
	snap := BuildSnapshot(ds)

	for _, sa := range snap.Auctions {
		switch sa.Status {
		case "closed":
			if sa.EndsAtOffset != 0 {
				t.Errorf("closed auction %d の ends_at_offset = %d, want 0", sa.ID, sa.EndsAtOffset)
			}
		default:
			if sa.EndsAtOffset <= 0 {
				t.Errorf("%s auction %d の ends_at_offset = %d, want 正の値", sa.Status, sa.ID, sa.EndsAtOffset)
			}
		}
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd initial-data && go test -count=1 -run TestSnapshot ./...`
Expected: コンパイルエラー（`BuildSnapshot` が未定義）

- [ ] **Step 3: 実装する**

`initial-data/snapshot.go`:

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Counts は生成データの総件数。Prepare の健全性確認に使う。
type Counts struct {
	Users         int64 `json:"users"`
	Auctions      int64 `json:"auctions"`
	LiveAuctions  int64 `json:"live_auctions"`
	Bids          int64 `json:"bids"`
	Notifications int64 `json:"notifications"`
}

// SnapshotAuction は Prepare が照合するオークションの正解値。
type SnapshotAuction struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	CategoryID    int64  `json:"category_id"`
	SellerID      int64  `json:"seller_id"`
	SellerName    string `json:"seller_name"`
	StartingPrice int64  `json:"starting_price"`
	CurrentPrice  int64  `json:"current_price"`
	BidCount      int64  `json:"bid_count"`
	Status        string `json:"status"`
	// EndsAtOffset は generatedEpoch からの秒数。live/upcoming のみ。closed は 0。
	// POST /initialize がこのオフセットを現在時刻へ付け替える。
	EndsAtOffset int    `json:"ends_at_offset"`
	WinnerID     *int64 `json:"winner_id"`
	WinningPrice *int64 `json:"winning_price"`
}

// Snapshot は初期データの正解スナップショット。bench/snapshot.go が読む側として対になる。
//
// 全オークションは載せない。Prepare が照合するのは一覧1ページ目・代表サンプル・
// 総件数の3つだけなので、全 live + 全 upcoming + サンプル対象の closed で足りる。
// 1万件を載せると JSON が数MBになり、ベンチの起動が重くなるだけで得るものがない。
type Snapshot struct {
	Seed             int64             `json:"seed"`
	Scale            string            `json:"scale"`
	Counts           Counts            `json:"counts"`
	SampleAuctionIDs []int64           `json:"sample_auction_ids"`
	Auctions         []SnapshotAuction `json:"auctions"`
}

// closedSampleCount はスナップショットに載せる closed オークションの件数。
const closedSampleCount = 30

func BuildSnapshot(ds *Dataset) *Snapshot {
	bidCount := map[int64]int64{}
	maxAmount := map[int64]int64{}
	for _, b := range ds.Bids {
		bidCount[b.AuctionID]++
		if b.Amount > maxAmount[b.AuctionID] {
			maxAmount[b.AuctionID] = b.Amount
		}
	}
	userName := make(map[int64]string, len(ds.Users))
	for _, u := range ds.Users {
		userName[u.ID] = u.Name
	}

	toSnapshot := func(a Auction) SnapshotAuction {
		price := a.StartingPrice
		if m := maxAmount[a.ID]; m > price {
			price = m
		}
		off := 0
		if a.Status != "closed" {
			off = int(a.EndsAt.Sub(generatedEpoch).Seconds())
		}
		return SnapshotAuction{
			ID: a.ID, Title: a.Title, CategoryID: a.CategoryID,
			SellerID: a.SellerID, SellerName: userName[a.SellerID],
			StartingPrice: a.StartingPrice, CurrentPrice: price,
			BidCount: bidCount[a.ID], Status: a.Status,
			EndsAtOffset: off, WinnerID: a.WinnerID, WinningPrice: a.WinningPrice,
		}
	}

	snap := &Snapshot{Seed: ds.Config.Seed, Scale: ds.Config.Name}
	var closed []Auction
	for _, a := range ds.Auctions {
		switch a.Status {
		case "live", "upcoming":
			snap.Auctions = append(snap.Auctions, toSnapshot(a))
		case "closed":
			closed = append(closed, a)
		}
		snap.Counts.Auctions++
		if a.Status == "live" {
			snap.Counts.LiveAuctions++
		}
	}
	snap.Counts.Users = int64(len(ds.Users))
	snap.Counts.Bids = int64(len(ds.Bids))
	snap.Counts.Notifications = int64(len(ds.Notifications))

	// closed は等間隔にサンプリングする(決定的で、id 範囲全体に散る)
	if len(closed) > 0 {
		n := closedSampleCount
		if n > len(closed) {
			n = len(closed)
		}
		step := len(closed) / n
		if step < 1 {
			step = 1
		}
		for i := 0; i < len(closed) && len(snap.SampleAuctionIDs) < n; i += step {
			sa := toSnapshot(closed[i])
			snap.Auctions = append(snap.Auctions, sa)
			snap.SampleAuctionIDs = append(snap.SampleAuctionIDs, sa.ID)
		}
	}
	// live からも代表を数件サンプルに加える
	for _, sa := range snap.Auctions {
		if sa.Status == "live" && len(snap.SampleAuctionIDs) < closedSampleCount+10 {
			snap.SampleAuctionIDs = append(snap.SampleAuctionIDs, sa.ID)
		}
	}
	sort.Slice(snap.Auctions, func(i, j int) bool { return snap.Auctions[i].ID < snap.Auctions[j].ID })
	sort.Slice(snap.SampleAuctionIDs, func(i, j int) bool { return snap.SampleAuctionIDs[i] < snap.SampleAuctionIDs[j] })
	return snap
}

func WriteSnapshot(dir string, snap *Snapshot) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "snapshot.json"), append(b, '\n'), 0o644)
}
```

`initial-data/main.go`:

```go
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	scale := flag.String("scale", "small", "生成規模 (small|medium|full)")
	seed := flag.Int64("seed", DefaultSeed, "乱数シード")
	out := flag.String("out", "out", "出力ディレクトリ")
	flag.Parse()

	cfg, ok := Scales[*scale]
	if !ok {
		fmt.Fprintf(os.Stderr, "不明な scale: %q (small|medium|full)\n", *scale)
		os.Exit(1)
	}
	cfg.Seed = *seed

	ds := Generate(cfg)
	if err := WriteSQL(*out, ds); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := WriteSnapshot(*out, BuildSnapshot(ds)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("scale=%s seed=%d users=%d auctions=%d bids=%d notifications=%d -> %s\n",
		cfg.Name, cfg.Seed, len(ds.Users), len(ds.Auctions), len(ds.Bids), len(ds.Notifications), *out)
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd initial-data && go test -count=1 ./... && go vet ./...`
Expected: PASS / vet クリーン

- [ ] **Step 5: small を実際に生成して中身を確認**

Run:
```bash
cd initial-data && go run . -scale small -out out && ls -la out/ && head -c 300 out/93_bids.sql && echo && head -20 out/snapshot.json
```
Expected: 5ファイルが生成され、`93_bids.sql` がマルチバリュー INSERT になっている

- [ ] **Step 6: コミット**

```bash
git add initial-data/
git commit -m "feat: ジェネレータのスナップショットJSON出力とCLIを追加"
```

---

## 4-A-2: 読み込み経路

### Task 7: `init.sh` と実行環境

**Files:**
- Create: `webapp/sql/init.sh`
- Modify: `webapp/go/Dockerfile`, `dev/compose.yaml`

**Interfaces:**
- Consumes: `initial-data/out/` の生成物（Task 5・6）
- Produces: `webapp/sql/init.sh`（環境変数 `ISUBID_DB_*` と `ISUBID_INITIAL_DATA_DIR` を読む）。Task 8 の `postInitialize` がこれを実行する

- [ ] **Step 1: `init.sh` を作る**

`webapp/sql/init.sh`:

```sh
#!/bin/sh
# ベンチ前初期化。POST /initialize から呼ばれる。
#
# 生成データ(initial-data/out)の読み込みは ISBID_INITIAL_DATA_DIR による明示的オプトイン。
# 未設定ならスキーマとシードだけを流す。生成物はリポジトリにコミットされるため
# 「ファイルが存在すれば読む」では条件として機能しない(webapp/go のテストが
# 生成データを読み込んでしまい、値ベースのアサーションが全滅する)。
set -eu

: "${ISUBID_DB_HOST:=127.0.0.1}"
: "${ISUBID_DB_PORT:=3306}"
: "${ISUBID_DB_USER:=isucon}"
: "${ISUBID_DB_PASSWORD:=isucon}"
: "${ISUBID_DB_NAME:=isubid}"

SQL_DIR="$(cd "$(dirname "$0")" && pwd)"

run() {
  mysql -h "$ISUBID_DB_HOST" -P "$ISUBID_DB_PORT" \
        -u "$ISUBID_DB_USER" -p"$ISUBID_DB_PASSWORD" \
        "$ISUBID_DB_NAME" < "$1"
}

run "$SQL_DIR/00_schema.sql"
run "$SQL_DIR/90_seed_phase1.sql"

if [ -n "${ISUBID_INITIAL_DATA_DIR:-}" ]; then
  for f in 91_users.sql 92_auctions.sql 93_bids.sql 94_notifications.sql; do
    run "$ISUBID_INITIAL_DATA_DIR/$f"
  done
fi
```

（環境変数名のタイポに注意: 正しくは `ISUBID_INITIAL_DATA_DIR`。上のコメント中の表記も揃えること。）

```bash
chmod +x webapp/sql/init.sh
```

- [ ] **Step 2: Dockerfile に mysql クライアントを追加**

`webapp/go/Dockerfile` の実行ステージを変更:

```dockerfile
FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends default-mysql-client \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /isubid /usr/local/bin/isubid
ENV ISUBID_PORT=8000
EXPOSE 8000
CMD ["isubid"]
```

- [ ] **Step 3: compose に生成データを配線**

`dev/compose.yaml` の `app` サービス:

```yaml
  app:
    build: ../webapp/go
    environment:
      ISUBID_DB_HOST: mysql
      ISUBID_SQL_DIR: /webapp/sql
      ISUBID_INITIAL_DATA_DIR: /initial-data
    volumes:
      - ../webapp/sql:/webapp/sql:ro
      - ../initial-data/out:/initial-data:ro
    depends_on:
      mysql:
        condition: service_healthy
```

- [ ] **Step 4: コンテナ内で `init.sh` が動くことを確認**

Run:
```bash
cd initial-data && go run . -scale small -out out && cd ..
docker compose -f dev/compose.yaml up -d --build
docker compose -f dev/compose.yaml exec -T app sh /webapp/sql/init.sh && echo "init.sh OK"
docker compose -f dev/compose.yaml exec -T mysql \
  mysql -uisucon -pisucon isubid -e "SELECT COUNT(*) AS auctions FROM auctions; SELECT COUNT(*) AS bids FROM bids;"
```
Expected: `init.sh OK` と、auctions が `12 + 1075`、bids が `8 + 30000` に相当する件数

- [ ] **Step 5: コミット**

```bash
git add webapp/sql/init.sh webapp/go/Dockerfile dev/compose.yaml
git commit -m "feat: init.shと生成データのマウントを追加"
```

---

### Task 8: `POST /initialize` の刷新

**Files:**
- Modify: `webapp/go/initialize.go`
- Test: `webapp/go/initialize_test.go`

**Interfaces:**
- Consumes: `webapp/sql/init.sh`（Task 7）、既存の `applyRelativeSchedule`
- Produces: `func applyGeneratedSchedule(ctx context.Context, db *sqlx.DB, base time.Time) error`、`const seedMaxAuctionID = 12`、`const generatedEpochLiteral = "2000-01-01 00:00:00"`

- [ ] **Step 1: 失敗するテストを書く**

`webapp/go/initialize_test.go` に追記:

```go
// 生成データ非搭載(ISUBID_INITIAL_DATA_DIR 未設定)では、従来どおり
// スキーマとシードだけが入る。webapp/go のテストはこの経路で走る。
func TestInitializeWithoutGeneratedData(t *testing.T) {
	if os.Getenv("ISUBID_INITIAL_DATA_DIR") != "" {
		t.Skip("生成データ搭載モードではこのテストは対象外")
	}
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
	// シードの live は10件のまま
	if len(list) != 10 {
		t.Fatalf("live = %d件, want 10 (生成データが混入している)", len(list))
	}
}
```

`import` に `"os"` を追加。

- [ ] **Step 2: テストが落ちないことを確認（現状維持の回帰テスト）**

Run: `cd webapp/go && go test -count=1 -run TestInitializeWithoutGeneratedData ./...`
Expected: PASS（このテストは Step 3 の変更で壊れないことを守るためのもの）

- [ ] **Step 3: 実装する**

`webapp/go/initialize.go` を編集。`import` に `"os/exec"` と `"fmt"` を追加し、以下を追記:

```go
// seedMaxAuctionID は webapp/sql/90_seed_phase1.sql が占める auction id の上端。
// 生成データは id 13 から採番される(initial-data/config.go の SeedMaxAuctionID と揃えること)。
const seedMaxAuctionID = 12

// generatedEpochLiteral は生成データが live/upcoming の時刻を保持する固定基準。
// initial-data/generate.go の generatedEpoch と一致させること。
const generatedEpochLiteral = "2000-01-01 00:00:00"

// applyGeneratedSchedule は生成データの live/upcoming を base 基準の時刻へ付け替える。
//
// WHERE id > seedMaxAuctionID が必須である。シード(id 1〜12)は applyRelativeSchedule で
// 既に「現在時刻＋オフセット」の絶対時刻になっており、ここで固定エポック起点の変換を
// 当てると TIMESTAMPDIFF が約8億3600万秒となり ends_at が2052年へ飛ぶ。
//
// closed は過去データなので書き換えない(走行時刻に依存しない)。
func applyGeneratedSchedule(ctx context.Context, db *sqlx.DB, base time.Time) error {
	_, err := db.ExecContext(ctx,
		"UPDATE auctions SET "+
			"starts_at = DATE_ADD(?, INTERVAL TIMESTAMPDIFF(SECOND, ?, starts_at) SECOND), "+
			"ends_at   = DATE_ADD(?, INTERVAL TIMESTAMPDIFF(SECOND, ?, ends_at)   SECOND) "+
			"WHERE id > ? AND status IN ('live','upcoming')",
		base, generatedEpochLiteral, base, generatedEpochLiteral, seedMaxAuctionID)
	return err
}

// loadViaInitScript は init.sh に投入を委譲する(mysql クライアントでのバルクロード)。
func loadViaInitScript(ctx context.Context, sqlDir string) error {
	cmd := exec.CommandContext(ctx, "sh", filepath.Join(sqlDir, "init.sh"))
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("init.sh: %w: %s", err, out)
	}
	return nil
}

// loadViaGo は Go でスキーマとシードだけを流す(生成データ非搭載時)。
// ホストに mysql クライアントが無い環境でも webapp/go のテストが動くよう、この経路を残す。
func loadViaGo(ctx context.Context, db *sqlx.DB, sqlDir string) error {
	for _, f := range initSQLFiles {
		b, err := os.ReadFile(filepath.Join(sqlDir, f))
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, string(b)); err != nil {
			return err
		}
	}
	return nil
}
```

`postInitialize` を書き換える:

```go
func (h *handler) postInitialize(w http.ResponseWriter, r *http.Request) {
	sqlDir := getEnv("ISUBID_SQL_DIR", "../sql")
	generatedDir := os.Getenv("ISUBID_INITIAL_DATA_DIR")

	db, err := sqlx.Open("mysql", dbDSN(true))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer db.Close()

	if generatedDir != "" {
		if err := loadViaInitScript(r.Context(), sqlDir); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		if err := loadViaGo(r.Context(), db, sqlDir); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	base := time.Now().UTC()
	if err := applyRelativeSchedule(r.Context(), db, base); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if generatedDir != "" {
		if err := applyGeneratedSchedule(r.Context(), db, base); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"lang": "go"})
}
```

- [ ] **Step 4: 生成データ非搭載でテストが通ることを確認**

Run: `cd webapp/go && go test -count=1 ./...`
Expected: PASS（27本＋新規1本。`mysql` クライアントに依存しない経路を通る）

- [ ] **Step 5: 生成データ搭載で initialize が通ることを確認**

Run:
```bash
docker compose -f dev/compose.yaml up -d --build
time curl -s -XPOST http://localhost:8080/initialize
docker compose -f dev/compose.yaml exec -T mysql mysql -uisucon -pisucon isubid \
  -e "SELECT status, COUNT(*) FROM auctions GROUP BY status;
      SELECT MIN(ends_at), MAX(ends_at) FROM auctions WHERE status='live';
      SELECT MIN(ends_at), MAX(ends_at) FROM auctions WHERE status='closed';"
```
Expected: `{"lang":"go"}`。live の `ends_at` が現在時刻付近、closed の `ends_at` が2025年（＝書き換えられていない）。**live に2052年が現れたら `WHERE id > 12` が効いていない**

- [ ] **Step 6: コミット**

```bash
git add webapp/go/initialize.go webapp/go/initialize_test.go
git commit -m "feat: initializeを生成データ対応(init.sh委譲と相対時刻の一括変換)に刷新"
```

---

## 4-A-3: ベンチのスナップショット照合

### Task 9: スナップショットの読み込み

**Files:**
- Create: `bench/snapshot.go`, `bench/snapshot_test.go`
- Modify: `bench/main.go`, `bench/scenario.go`

**Interfaces:**
- Consumes: `initial-data/out/snapshot.json`（Task 6 が出力）
- Produces: `type Snapshot`, `type SnapshotAuction`, `type SnapshotCounts`, `func LoadSnapshot(path string) (*Snapshot, error)`, `func (s *Snapshot) ByID(id int64) (*SnapshotAuction, bool)`、`Scenario.Snapshot *Snapshot`

- [ ] **Step 1: 失敗するテストを書く**

`bench/snapshot_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	body := `{
  "seed": 20260830,
  "scale": "small",
  "counts": {"users": 500, "auctions": 1075, "live_auctions": 50, "bids": 30000, "notifications": 1200},
  "sample_auction_ids": [13, 100],
  "auctions": [
    {"id": 13, "title": "テスト椅子", "category_id": 2, "seller_id": 21, "seller_name": "gen_user_00021",
     "starting_price": 1000, "current_price": 1500, "bid_count": 2, "status": "closed",
     "ends_at_offset": 0, "winner_id": 22, "winning_price": 1500},
    {"id": 100, "title": "テスト椅子2", "category_id": 1, "seller_id": 23, "seller_name": "gen_user_00023",
     "starting_price": 2000, "current_price": 2000, "bid_count": 0, "status": "live",
     "ends_at_offset": 42, "winner_id": null, "winning_price": null}
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	snap, err := LoadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Scale != "small" || snap.Seed != 20260830 {
		t.Errorf("scale/seed = %q/%d", snap.Scale, snap.Seed)
	}
	if snap.Counts.LiveAuctions != 50 {
		t.Errorf("counts.live_auctions = %d, want 50", snap.Counts.LiveAuctions)
	}
	if len(snap.SampleAuctionIDs) != 2 {
		t.Errorf("sample_auction_ids = %v", snap.SampleAuctionIDs)
	}

	a, ok := snap.ByID(13)
	if !ok {
		t.Fatal("id 13 が引けない")
	}
	if a.Title != "テスト椅子" || a.CurrentPrice != 1500 || a.BidCount != 2 {
		t.Errorf("auction 13 = %+v", a)
	}
	if a.WinnerID == nil || *a.WinnerID != 22 {
		t.Errorf("auction 13 winner_id = %v, want 22", a.WinnerID)
	}
	if b, _ := snap.ByID(100); b.WinnerID != nil {
		t.Errorf("auction 100 winner_id = %v, want nil", b.WinnerID)
	}
	if _, ok := snap.ByID(99999); ok {
		t.Error("存在しない id が引けてしまう")
	}
}

func TestLoadSnapshotMissingFile(t *testing.T) {
	if _, err := LoadSnapshot(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("存在しないファイルでエラーにならない")
	}
}
```

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd bench && go test -count=1 -run TestLoadSnapshot ./...`
Expected: コンパイルエラー（`LoadSnapshot` が未定義）

- [ ] **Step 3: 実装する**

`bench/snapshot.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// SnapshotCounts は生成データの総件数。
type SnapshotCounts struct {
	Users         int64 `json:"users"`
	Auctions      int64 `json:"auctions"`
	LiveAuctions  int64 `json:"live_auctions"`
	Bids          int64 `json:"bids"`
	Notifications int64 `json:"notifications"`
}

// SnapshotAuction は初期データの正解値。initial-data/snapshot.go の同名型と対になる。
type SnapshotAuction struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	CategoryID    int64  `json:"category_id"`
	SellerID      int64  `json:"seller_id"`
	SellerName    string `json:"seller_name"`
	StartingPrice int64  `json:"starting_price"`
	CurrentPrice  int64  `json:"current_price"`
	BidCount      int64  `json:"bid_count"`
	Status        string `json:"status"`
	// EndsAtOffset は初期化時刻からの秒数。live/upcoming のみ。closed は 0。
	EndsAtOffset int    `json:"ends_at_offset"`
	WinnerID     *int64 `json:"winner_id"`
	WinningPrice *int64 `json:"winning_price"`
}

// Snapshot は initial-data ジェネレータが出力した正解スナップショット。
// 全オークションは載っていない(全 live + 全 upcoming + サンプル対象の closed のみ)。
type Snapshot struct {
	Seed             int64             `json:"seed"`
	Scale            string            `json:"scale"`
	Counts           SnapshotCounts    `json:"counts"`
	SampleAuctionIDs []int64           `json:"sample_auction_ids"`
	Auctions         []SnapshotAuction `json:"auctions"`

	byID map[int64]*SnapshotAuction
}

func LoadSnapshot(path string) (*Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("スナップショットの読み込みに失敗: %w", err)
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("スナップショットのJSONが不正 (%s): %w", path, err)
	}
	s.byID = make(map[int64]*SnapshotAuction, len(s.Auctions))
	for i := range s.Auctions {
		s.byID[s.Auctions[i].ID] = &s.Auctions[i]
	}
	return &s, nil
}

func (s *Snapshot) ByID(id int64) (*SnapshotAuction, bool) {
	a, ok := s.byID[id]
	return a, ok
}
```

`bench/main.go` にフラグを追加:

```go
	snapshotPath := flag.String("snapshot", "", "初期データの正解スナップショット(空なら生成データ非搭載モード)")
```

`flag.Parse()` の後に読み込み、`Scenario` へ渡す:

```go
	var snap *Snapshot
	if *snapshotPath != "" {
		var err error
		snap, err = LoadSnapshot(*snapshotPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
```

`Scenario` の初期化に `Snapshot: snap,` を追加。`bench/scenario.go` の `Scenario` 構造体に `Snapshot *Snapshot` を追加する。

- [ ] **Step 4: テストが通ることを確認**

Run: `cd bench && go build ./... && go test -count=1 ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add bench/snapshot.go bench/snapshot_test.go bench/main.go bench/scenario.go
git commit -m "feat: ベンチにスナップショット読み込みと-snapshotフラグを追加"
```

---

### Task 10: Prepare のスナップショット照合

**Files:**
- Modify: `bench/validate.go`, `bench/scenario.go`
- Test: `bench/validate_test.go`

**Interfaces:**
- Consumes: `Snapshot`, `SnapshotAuction`（Task 9）、既存の `expectedInitialAuctions` / `endsAtTolerance`
- Produces: `func ValidateAuctionListWithSnapshot(list []AuctionSummary, snap *Snapshot, base time.Time) error`, `func ValidateSnapshotAuctionDetail(d *AuctionDetail, sa *SnapshotAuction, base time.Time) error`

- [ ] **Step 1: 失敗するテストを書く**

`bench/validate_test.go` に追記:

```go
// 生成データ搭載時の一覧検証。Phase 3 の完全一致照合(initialAuctionOrder)は
// live が約260件になると同着やミリ秒のズレで壊れるため、
// (a) ends_at が非減少 (b) id 昇順にソートされていない の2性質に置き換える。
func TestValidateAuctionListWithSnapshot(t *testing.T) {
	base := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	snap := &Snapshot{
		Counts: SnapshotCounts{LiveAuctions: 3},
		Auctions: []SnapshotAuction{
			{ID: 13, Title: "gen A", CategoryID: 1, SellerID: 21, SellerName: "gen_user_00021",
				StartingPrice: 1000, CurrentPrice: 1000, BidCount: 0, Status: "live", EndsAtOffset: 30},
			{ID: 14, Title: "gen B", CategoryID: 2, SellerID: 22, SellerName: "gen_user_00022",
				StartingPrice: 2000, CurrentPrice: 2500, BidCount: 3, Status: "live", EndsAtOffset: 10},
			{ID: 15, Title: "gen C", CategoryID: 3, SellerID: 23, SellerName: "gen_user_00023",
				StartingPrice: 3000, CurrentPrice: 3000, BidCount: 0, Status: "live", EndsAtOffset: 50},
		},
	}
	snap.byID = map[int64]*SnapshotAuction{}
	for i := range snap.Auctions {
		snap.byID[snap.Auctions[i].ID] = &snap.Auctions[i]
	}

	gen := func(id int64) AuctionSummary {
		sa := snap.byID[id]
		return AuctionSummary{
			ID: sa.ID, Title: sa.Title, CategoryID: sa.CategoryID,
			Seller:       User{ID: sa.SellerID, Name: sa.SellerName},
			CurrentPrice: sa.CurrentPrice, BidCount: sa.BidCount, Status: "live",
			EndsAt: base.Add(time.Duration(sa.EndsAtOffset) * time.Second),
		}
	}
	seed := func(id int64) AuctionSummary {
		w := expectedInitialAuctions[id]
		return AuctionSummary{
			ID: id, Title: w.Title, CategoryID: w.CategoryID,
			Seller:       User{ID: w.SellerID, Name: "seed_user_" + pad2(w.SellerID)},
			CurrentPrice: w.CurrentPrice, BidCount: w.BidCount, Status: "live",
			EndsAt: base.Add(time.Duration(w.EndsAtOffset) * time.Second),
		}
	}

	// ends_at 昇順にマージした正しい一覧を組み立てる。
	// オフセット: gen14=10, seed4=12, seed2=20, seed8=28, gen13=30, seed6=36,
	//             seed10=44, gen15=50, seed1=3600, seed3=3660, seed5=3720, seed7=3780, seed9=3840
	// 合計13件 = シード10件 + 生成3件。
	build := func() []AuctionSummary {
		out := []AuctionSummary{gen(14), seed(4), seed(2), seed(8), gen(13), seed(6), seed(10), gen(15)}
		for _, id := range initialAuctionOrder[5:] { // 3600秒台のシード5件
			out = append(out, seed(id))
		}
		return out
	}

	if err := ValidateAuctionListWithSnapshot(build(), snap, base); err != nil {
		t.Fatalf("正しい一覧が拒否された: %v", err)
	}

	// ends_at が降順に混ざると落ちる
	bad := build()
	bad[0], bad[1] = bad[1], bad[0]
	if err := ValidateAuctionListWithSnapshot(bad, snap, base); err == nil {
		t.Error("ends_at の順序違反が検出されなかった")
	}

	// id 昇順にソートすると落ちる(ORDER BY id ASC への書き換え相当)
	byID := build()
	sort.Slice(byID, func(i, j int) bool { return byID[i].ID < byID[j].ID })
	if err := ValidateAuctionListWithSnapshot(byID, snap, base); err == nil {
		t.Error("id 昇順ソート(ORDER BY id ASC 相当)が検出されなかった")
	}

	// 件数が合わないと落ちる
	short := build()[:len(build())-1]
	if err := ValidateAuctionListWithSnapshot(short, snap, base); err == nil {
		t.Error("件数不一致が検出されなかった")
	}

	// 生成オークションのフィールドが改変されると落ちる
	tampered := build()
	for i := range tampered {
		if tampered[i].ID == 14 {
			tampered[i].CurrentPrice = 9999
		}
	}
	if err := ValidateAuctionListWithSnapshot(tampered, snap, base); err == nil {
		t.Error("生成オークションの current_price 改変が検出されなかった")
	}
}
```

`import` に `"sort"` が必要（既にあれば不要）。

- [ ] **Step 2: テストが落ちることを確認**

Run: `cd bench && go test -count=1 -run TestValidateAuctionListWithSnapshot ./...`
Expected: コンパイルエラー（`ValidateAuctionListWithSnapshot` が未定義）

- [ ] **Step 3: 実装する**

`bench/validate.go` に追記:

```go
// ValidateAuctionListWithSnapshot は生成データ搭載時の一覧検証。
//
// Phase 3 の ValidateInitialAuctionList は期待 id 列(initialAuctionOrder)との
// 完全一致で照合していたが、生成データが入ると live は約260件になり、
// シードと生成分が ends_at 順で交互に並ぶ。完全一致は同着やミリ秒単位のズレで
// 壊れるため、次の2つの独立した性質に置き換える。
//
//	(a) ends_at が非減少であること
//	(b) リストが id 昇順にソートされていないこと
//
// 一覧の ORDER BY ends_at ASC を ORDER BY id ASC に書き換える改変は
// (a)(b) の両方に引っかかるため、検出力は落ちない。
func ValidateAuctionListWithSnapshot(list []AuctionSummary, snap *Snapshot, base time.Time) error {
	want := int64(len(expectedInitialAuctions)) + snap.Counts.LiveAuctions
	if int64(len(list)) != want {
		return fmt.Errorf("GET /auctions: 件数が %d (期待: %d = シード %d + 生成 %d)",
			len(list), want, len(expectedInitialAuctions), snap.Counts.LiveAuctions)
	}

	// (a) ends_at が非減少
	for i := 1; i < len(list); i++ {
		if list[i].EndsAt.Before(list[i-1].EndsAt) {
			return fmt.Errorf("GET /auctions: ends_at が昇順でない (index %d: id=%d %v の前が id=%d %v)",
				i, list[i].ID, list[i].EndsAt, list[i-1].ID, list[i-1].EndsAt)
		}
	}

	// (b) id 昇順にソートされていない
	sortedByID := true
	for i := 1; i < len(list); i++ {
		if list[i].ID < list[i-1].ID {
			sortedByID = false
			break
		}
	}
	if sortedByID {
		return fmt.Errorf("GET /auctions: 一覧が id 昇順に並んでいる (ORDER BY ends_at ASC が ORDER BY id ASC に置き換わっている疑い)")
	}

	// 各行の中身を、シードは既存の期待値表、生成分はスナップショットと照合する
	for _, a := range list {
		if a.Status != "live" {
			return fmt.Errorf("auction %d: status が %q (期待: live)", a.ID, a.Status)
		}
		if a.ID <= seedMaxAuctionID {
			w, ok := expectedInitialAuctions[a.ID]
			if !ok {
				return fmt.Errorf("GET /auctions: 想定外のシードauction %d が live 一覧にいる", a.ID)
			}
			if err := checkListRow(a, w.Title, w.CategoryID, w.SellerID,
				"seed_user_"+pad2(w.SellerID), w.CurrentPrice, w.BidCount,
				base.Add(time.Duration(w.EndsAtOffset)*time.Second)); err != nil {
				return err
			}
			continue
		}
		sa, ok := snap.ByID(a.ID)
		if !ok {
			return fmt.Errorf("GET /auctions: スナップショットに無い auction %d が live 一覧にいる", a.ID)
		}
		if err := checkListRow(a, sa.Title, sa.CategoryID, sa.SellerID, sa.SellerName,
			sa.CurrentPrice, sa.BidCount,
			base.Add(time.Duration(sa.EndsAtOffset)*time.Second)); err != nil {
			return err
		}
	}
	return nil
}

// seedMaxAuctionID は webapp/sql/90_seed_phase1.sql が占める auction id の上端。
// webapp/go/initialize.go の同名定数と揃えること。
const seedMaxAuctionID = 12

func checkListRow(a AuctionSummary, title string, categoryID, sellerID int64,
	sellerName string, currentPrice, bidCount int64, wantEndsAt time.Time) error {
	if a.Title != title {
		return fmt.Errorf("auction %d: title が %q (期待: %q)", a.ID, a.Title, title)
	}
	if a.CategoryID != categoryID {
		return fmt.Errorf("auction %d: category_id が %d (期待: %d)", a.ID, a.CategoryID, categoryID)
	}
	if a.Seller.ID != sellerID || a.Seller.Name != sellerName {
		return fmt.Errorf("auction %d: seller が %+v (期待: id=%d name=%q)", a.ID, a.Seller, sellerID, sellerName)
	}
	if a.CurrentPrice != currentPrice {
		return fmt.Errorf("auction %d: current_price が %d (期待: %d)", a.ID, a.CurrentPrice, currentPrice)
	}
	if a.BidCount != bidCount {
		return fmt.Errorf("auction %d: bid_count が %d (期待: %d)", a.ID, a.BidCount, bidCount)
	}
	if d := a.EndsAt.Sub(wantEndsAt); d > endsAtTolerance || d < -endsAtTolerance {
		return fmt.Errorf("auction %d: ends_at が %v (期待: %v ± %v)", a.ID, a.EndsAt, wantEndsAt, endsAtTolerance)
	}
	return nil
}

// ValidateSnapshotAuctionDetail は代表サンプルの詳細をスナップショットと照合する。
func ValidateSnapshotAuctionDetail(d *AuctionDetail, sa *SnapshotAuction, base time.Time) error {
	if d.ID != sa.ID {
		return fmt.Errorf("auction detail: id が %d (期待: %d)", d.ID, sa.ID)
	}
	if d.Title != sa.Title {
		return fmt.Errorf("auction %d: title が %q (期待: %q)", d.ID, d.Title, sa.Title)
	}
	if d.Status != sa.Status {
		return fmt.Errorf("auction %d: status が %q (期待: %q)", d.ID, d.Status, sa.Status)
	}
	if d.StartingPrice != sa.StartingPrice {
		return fmt.Errorf("auction %d: starting_price が %d (期待: %d)", d.ID, d.StartingPrice, sa.StartingPrice)
	}
	if d.CurrentPrice != sa.CurrentPrice {
		return fmt.Errorf("auction %d: current_price が %d (期待: %d)", d.ID, d.CurrentPrice, sa.CurrentPrice)
	}
	if d.BidCount != sa.BidCount {
		return fmt.Errorf("auction %d: bid_count が %d (期待: %d)", d.ID, d.BidCount, sa.BidCount)
	}
	if int64(len(d.Bids)) != sa.BidCount {
		return fmt.Errorf("auction %d: bids が %d件 (期待: %d件)", d.ID, len(d.Bids), sa.BidCount)
	}
	if sa.Status == "closed" {
		if (d.WinnerID == nil) != (sa.WinnerID == nil) {
			return fmt.Errorf("auction %d: winner_id が %v (期待: %v)", d.ID, d.WinnerID, sa.WinnerID)
		}
		if d.WinnerID != nil && *d.WinnerID != *sa.WinnerID {
			return fmt.Errorf("auction %d: winner_id が %d (期待: %d)", d.ID, *d.WinnerID, *sa.WinnerID)
		}
		if d.WinningPrice != nil && sa.WinningPrice != nil && *d.WinningPrice != *sa.WinningPrice {
			return fmt.Errorf("auction %d: winning_price が %d (期待: %d)", d.ID, *d.WinningPrice, *sa.WinningPrice)
		}
	}
	return ValidateBidsInvariant(d.Bids)
}
```

`bench/scenario.go` の `Prepare` を分岐させる。既存の `ValidateInitialAuctionList(list, base)` 呼び出しを次に置き換える:

```go
	if s.Snapshot != nil {
		if err := ValidateAuctionListWithSnapshot(list, s.Snapshot, base); err != nil {
			return err
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
		if err := ValidateInitialAuctionList(list, base); err != nil {
			return err
		}
	}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd bench && go test -count=1 ./...`
Expected: PASS

- [ ] **Step 5: 生成データ非搭載で従来どおり Prepare が通ることを確認**

Run:
```bash
docker compose -f dev/compose.yaml stop app && docker compose -f dev/compose.yaml rm -f app
# ISUBID_INITIAL_DATA_DIR を外した状態で app を起動する(compose.yaml の環境変数を一時的にコメントアウト)
docker compose -f dev/compose.yaml up -d --build app
cd bench && go run . -target http://localhost:8080 -prepare-only
```
Expected: `PREPARE: PASS`。確認後、compose.yaml を元に戻す

- [ ] **Step 6: コミット**

```bash
git add bench/validate.go bench/validate_test.go bench/scenario.go
git commit -m "feat: Prepareの一覧検証をスナップショット照合(2性質方式)へ移行"
```

---

## 4-A-4: 段階的サイジング

### Task 11: `small` の実測とゲート判定

**Files:**
- Create: `docs/phase4-notes.md`
- Modify: なし（`initial-data/out/` の生成物は Task 12 で確定させる）

**Interfaces:**
- Consumes: Task 1〜10 の全て
- Produces: `docs/phase4-notes.md`（Task 12 が追記する）

**このタスクは計測である。** 数値を作らず、観測したものだけを記録すること。

- [ ] **Step 1: `small` を生成してスタックを立てる**

Run:
```bash
cd initial-data && go run . -scale small -out out && ls -la out/ && cd ..
docker compose -f dev/compose.yaml up -d --build
```

生成物のサイズ（`ls -la out/`）を記録する。

- [ ] **Step 2: ゲート1 — `POST /initialize` の所要時間**

Run:
```bash
for i in 1 2 3; do
  /usr/bin/time -p curl -s -o /dev/null -XPOST http://localhost:8080/initialize 2>&1 | grep '^real'
done
```
Expected: 3回とも 15秒未満。閾値の根拠はレギュレーションの30秒制限に対して半分の余裕を残すこと

- [ ] **Step 3: ゲート2 — Prepare の所要時間**

Run:
```bash
cd bench && for i in 1 2 3; do
  /usr/bin/time -p go run . -target http://localhost:8080 -prepare-only \
    -snapshot ../initial-data/out/snapshot.json 2>&1 | grep -E 'PREPARE|^real'
done
```
Expected: 3回とも `PREPARE: PASS` かつ 6秒未満。

閾値の根拠は**シードの auction 4（`ends_at` オフセット +12秒）**である。生成 live の最短オフセットは15秒だが、シードのほうが早く閉じるため拘束条件はこちら。Prepare がこれを追い越すと「一覧にいたはずのオークションが消えている」で false-FAIL する。Phase 3 でも同じ理由から「Prepare が6秒を超えたらオフセット表を見直す」と記録している（当時の実測は 0.6〜1.2秒）

- [ ] **Step 4: ゲート3 — 60秒走行**

Run:
```bash
cd bench && go run . -target http://localhost:8080 -duration 60s \
  -snapshot ../initial-data/out/snapshot.json
```
Expected: `RESULT: PASS`、critical 0件。`SCORE` と breakdown を記録する

- [ ] **Step 5: ゲート4 — `FOR UPDATE` 除去で FAIL すること（最重要）**

単調増加検出器は「同一オークションへの同時入札」に依存する。生成 live が増えるほど入札が薄まり、検出力が落ちる。**採用規模はこの検出器が生きている最大規模で決まる。**

`webapp/go/bids.go` の `postBid` 内、オークション行の `SELECT ... FOR UPDATE` から `FOR UPDATE` を一時的に外す。

```bash
docker compose -f dev/compose.yaml up -d --build
cd bench && go run . -target http://localhost:8080 -duration 60s \
  -snapshot ../initial-data/out/snapshot.json
```
Expected: `RESULT: FAIL`。`bids の金額が単調増加違反` または `フィードの金額が単調増加でない` の critical

**1回目で検出できなければ、あと2回まで繰り返す**（検出は確率的になっている）。3回とも PASS した場合は**ゲート4は不合格**であり、そのスケールは採用できない。結果を取り繕わず、3回分すべて記録すること。

```bash
git checkout webapp/go/bids.go
docker compose -f dev/compose.yaml up -d --build
```
で復元し、通常走行が PASS に戻ることを確認する。

- [ ] **Step 6: `docs/phase4-notes.md` を作る**

`<...>` は実際に観測した値で埋める。測っていない数字は書かない。

```markdown
# Phase 4 実装ノート

## 4-A 初期データジェネレータ

### 設計判断

- **生成データは Phase 1 シードを置き換えず id 13 から continue する**: `webapp/go` の
  テスト27本がシードの具体値(auction 1 の入札列 1500/1200/1000、seed_user_05、
  auction 11 の落札者12など)に依存しており、置き換えると値ベースのアサーションを
  性質ベースへ全面的に書き直すことになる。前置きに残せばテストスイートと
  Prepare の詳細検証が無傷で済み、書き直しは一覧レベルだけに限定できる
- **生成データの読み込みは `ISUBID_INITIAL_DATA_DIR` による明示的オプトイン**:
  生成物はコミットするため「ファイルが存在すれば読む」では条件として機能せず、
  `go test` が `/initialize` 経由で生成データを読み込んでテストが全滅する。
  同じ変数が読み込み経路(init.sh / Go)も切り替えるので、ホストに mysql クライアントが
  無い環境でも webapp のテストが動く
- **一覧の順序検証を完全一致から2性質へ**: live が約260件になるとシードと生成分が
  ends_at 順で交互に並び、期待 id 列との完全一致は同着やミリ秒のズレで壊れる。
  (a) ends_at が非減少 (b) id 昇順にソートされていない、の2つに置き換えた。
  `ORDER BY id ASC` への書き換えは両方に引っかかるため検出力は落ちない
- **`applyGeneratedSchedule` の `WHERE id > 12` が必須**: シードは
  `applyRelativeSchedule` で既に現在時刻基準の絶対時刻になっており、そこへ固定エポック
  起点の変換を当てると `TIMESTAMPDIFF` が約8億3600万秒となり ends_at が2052年へ飛ぶ

### サイジング実測(2026-08-30)

| scale | 生成物サイズ | ゲート1 initialize | ゲート2 Prepare | ゲート3 60秒走行 | ゲート4 FOR UPDATE除去 |
|---|---|---|---|---|---|
| small | <...> | <...> | <...> | <...> | <...> |
```

- [ ] **Step 7: コミット**

```bash
git add docs/phase4-notes.md
git commit -m "docs: 4-A の設計判断と small スケールのサイジング実測を記録"
```

---

### Task 12: `medium` / `full` の実測と採用規模の決定

**Files:**
- Modify: `docs/phase4-notes.md`, `README.md`
- Create: `initial-data/out/`（採用スケールの生成物をコミット）

**Interfaces:**
- Consumes: Task 11 の手順と `docs/phase4-notes.md`
- Produces: 採用スケールの生成物、`README.md` の更新

- [ ] **Step 1: `medium` で4つのゲートを測る**

Task 11 の Step 1〜5 を `-scale medium` で繰り返し、`docs/phase4-notes.md` の表に行を足す。

- [ ] **Step 2: `full` で4つのゲートを測る**

同じく `-scale full` で繰り返す。

`full` の `93_bids.sql` は概算15MB前後になる。実サイズを `ls -la` で確認し記録する。

- [ ] **Step 3: 採用規模を決める**

**4つのゲートをすべて満たす最大のスケールを採用する。**

判断を `docs/phase4-notes.md` に、根拠となる実測値とともに記録する。特にゲート4で落ちたスケールがある場合は、その事実を明記すること — それは「入札の集中度が足りず、単調増加検出器が効かなくなった」という Phase 3 から持ち越したリスクが顕在化したという意味であり、4-E（負荷調整）への重要な入力になる。

**全スケールでゲート4が不合格の場合**: `small` を採用し、判断を 4-E へエスカレーションする。その旨を `docs/phase4-notes.md` の持ち越しに明記する。

- [ ] **Step 4: 生成物サイズの扱いを決める**

採用スケールの生成物合計サイズを見て、リポジトリにコミットするか GitHub Releases へ逃がすかを決める。目安として合計50MBを超えるようなら Releases を検討する。判断と実サイズを記録する。

コミットする場合:

```bash
cd initial-data && go run . -scale <採用スケール> -out out && cd ..
git add initial-data/out/
```

- [ ] **Step 5: 採用スケールで通し走行を3回行い、再現性を確認する**

Run:
```bash
cd bench && for i in 1 2 3; do
  go run . -target http://localhost:8080 -duration 60s \
    -snapshot ../initial-data/out/snapshot.json | grep -E 'SCORE|RESULT'
done
```
Expected: 3回とも `RESULT: PASS`。スコアの振れ幅を記録する（このマシンは同一セッション内でも約7〜12%振れることが Phase 3 で分かっている。**Phase 3 のスコアと比較して性能の優劣を述べないこと** — 初期データが変わっており同一条件の比較ではない）

- [ ] **Step 6: `README.md` を更新する**

「ステータス」と「クイックスタート」に、初期データの生成と `-snapshot` の指定を追記する。

```markdown
## クイックスタート

```bash
# 1. 初期データを生成(初回のみ。生成物はコミット済みなので通常は不要)
cd initial-data && go run . -scale <採用スケール> -out out && cd ..

# 2. フルスタック起動(nginx :8080 → app :8000 → mysql :3306)
docker compose -f dev/compose.yaml up -d --build

# 3. ベンチ実行(60秒の負荷走行+整合性検証)
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json
```

初期データ無しで動かす場合は `-snapshot` を省き、compose の `ISUBID_INITIAL_DATA_DIR` を外す。
```

- [ ] **Step 7: `docs/phase4-notes.md` に総括と持ち越しを書く**

採用スケール、その根拠、4-B 以降への持ち越し（生成データで一覧の N+1 がさらに重くなること、入札集中度の変化、生成物サイズの扱い）を記録する。

- [ ] **Step 8: 最終確認**

Run:
```bash
cd initial-data && go test -count=1 ./... && go vet ./...
cd ../webapp/go && go test -count=1 ./...
cd ../../bench && go test -count=1 ./... && go vet ./...
cd .. && gofmt -l initial-data/ bench/ webapp/go/
git status --short
```
Expected: すべて PASS、gofmt は何も出力しない、作業ツリーはクリーン（コミット後）

- [ ] **Step 9: コミット**

```bash
git add docs/phase4-notes.md README.md initial-data/out/
git commit -m "feat: 初期データの採用規模を実測で決定し生成物をコミット"
```
