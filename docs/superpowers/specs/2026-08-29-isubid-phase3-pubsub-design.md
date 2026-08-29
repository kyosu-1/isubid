# ISUBID Phase 3 — pub/sub要素 設計ドキュメント

作成日: 2026-08-29
ステータス: 承認済み(論点1〜4の方針合意済み)
親ドキュメント: `docs/superpowers/specs/2026-07-08-isubid-design.md`

## 前提

Phase 2b-1 完了時点（`main` = 5063df6）。参照実装は6エンドポイント（`/initialize`・`/register`・`/login`・`GET /auctions`・`GET /auctions/:id`・`POST /auctions/:id/bids`）、ベンチは Prepare / Load(60秒) / Validation(台帳突合) の3フェーズが通しで動き、初期実装に対して `SCORE: 29745 / RESULT: PASS` を実測済み。

スキーマ（`webapp/sql/00_schema.sql`）には `notifications` テーブル、`auctions.winner_id` / `winning_price`、`users.icon` が既に定義されているが、いずれもハンドラ側が未実装で使われていない。Phase 3 はこのうち通知と落札を稼働させる（`users.icon` は Phase 4）。

## スコープ

**含む**

- 時間軸の作り直し（走行中に live→closed 遷移が起きるようにする）
- 終了処理バッチ（落札者確定）と落札結果の Validation
- 入札フィード API と「反映2秒以内」のリアルタイム性検証
- 通知ファンアウトと通知欠落の検証
- 出品 API・売上ダッシュボードと出品者シナリオ（`pubsub` によるシナリオ間連携）

**含まない（Phase 4 に送る）**

- SPA フロントエンド、アセット追従検証、`GET /users/:id/icon`（BLOB配信）
- 検索 `GET /auctions?category=&q=`
- 初期データジェネレータとフルサイズ初期データ
- 負荷の段階増加、レギュレーション/マニュアル/writeup 文書

初期データジェネレータは親ドキュメントでは Phase 2 に置かれているが、Phase 3 でスキーマとAPIが増える（通知・落札・出品）ため、**先に作ると確実に作り直しになる**。スキーマが固まる Phase 3 完了後、Phase 4 で一度に作る。

## 論点と決定

### 論点1: 走行中に live→closed を起こす方法

**決定: `POST /initialize` が seed 投入後に `ends_at` を初期化時刻からの相対値で書き換える。**

現在の seed は `ends_at` が `2030-01-01` 固定で、60秒走行中に1件も閉じない。落札 Validation には遷移が必須。

ISUCON 本家と同じ「初期化時刻を基準時刻にする」発想を採る。ベンチは `/initialize` の応答時刻を基準に「いつ何が閉じるか」を予測でき、Validation の期待値を組み立てられる。UPDATE は十数件なので初期化の30秒制限には影響しない。

不採用:

- **seed に固定時刻を書き、ベンチ側で時計を合わせる** — 再現性が実行時刻に依存して脆い
- **アプリに仮想時刻を持たせる** — 親ドキュメントの「模擬・ダミー処理なし」方針に反する

### 論点2: 終了処理の実装方式

**決定: アプリ内 goroutine による毎秒ポーリングバッチ（親ドキュメント指定どおり）。**

`WHERE status='live' AND ends_at <= NOW()` を毎秒フルスキャンし、該当オークションを1件ずつ逐次処理する。1件あたり `SELECT MAX(amount)` で最高額を求め、落札者を確定し、通知を同期 INSERT する。逐次・同期で重い初期実装にしておくと、非同期化・インデックス追加・一括処理という改善の道筋がそのまま効く。

不採用:

- **参照時 lazy close** — バッチが要らず軽い代わりに「終了処理が重い」という出題ポイントが消える

**持ち越し**: 複数台構成にするとバッチが二重実行される。Phase 5 のクラウドIaC でレギュレーション（バッチを動かすホストの指定、または冪等性の要求）として扱う。Phase 3 は単一アプリプロセスを前提とする。

### 論点3: 入札フィード API の契約

**決定: `GET /auctions/:id/bids?since=<最後に見た bid id>` で、`id > since` を `id ASC` で返す。**

`bids.id` は AUTO_INCREMENT であり、入札APIはオークション行を `FOR UPDATE` で保持したまま INSERT するため、**同一オークション内では id 順 = コミット順**が保証される（ロックがコミットまで解放されないため、後から採番された入札が先にコミットされることがない）。したがって `id > since` のカーソルは境界での重複・欠落を原理的に起こさず、「自分の入札が2秒以内にフィードへ現れる」検証が決定的になる。

不採用:

- **`since=<timestamp>`** — 同一マイクロ秒に複数入札が入ると、境界の扱い次第で重複か欠落が起き、検証が確率的になる

なおこの保証は「オークション行ロックで入札を直列化する」という参照実装の性質に依存する。競技者が `FOR UPDATE` を外すと id 順=コミット順が崩れうるが、それは既存の単調増加検証（`ValidateBidAmountsMonotonic`）が先に critical で捕まえる。

### 論点4: 通知ファンアウトの範囲

**決定: 高値更新のたびに、そのオークションに入札済みの全ユーザー（今回の入札者を除く）へ1行ずつ INSERT する。**

親ドキュメントの「高値更新のたび全入札者にINSERTするファンアウトが入札APIを重くしている」をそのまま実装する。落札確定時は winner へ `won` 通知を1件送る。

ファンアウト範囲を「直前の最高額入札者だけ」に狭めると、検証の成立性は変わらないまま重さだけが消える。出題としてはファンアウトの重さが肝なので広い方を採る。

## API 仕様（追加分）

すべて JSON。エラーボディは既存と同じ `{"error": "..."}` 形式。

### `POST /auctions` — 出品（要ログイン）

```
req:  {"title": string, "description": string, "category_id": int,
       "starting_price": int, "duration_seconds": int}
res:  201 {"id": int, "title": ..., "starting_price": ..., "ends_at": "RFC3339", "status": "live"}
```

- `starts_at = NOW()`、`ends_at = NOW() + duration_seconds`、`status = 'live'`（出品即開始）
- バリデーション: `title` 非空かつ255文字以下、`starting_price >= 1`、`category_id` が存在、`duration_seconds` が 10〜300 の範囲。違反は 400
- 未ログインは 401

出品を即 live にするのは、`pubsub` で入札者へ流してすぐ入札を集めさせるため。`upcoming` を経由させると走行60秒のうち待ち時間が無駄になる。

### `GET /auctions/:id/bids?since=<bid_id>` — 入札フィード

```
res:  200 {"bids": [{"id": int, "user_id": int, "user_name": string,
                     "amount": int, "created_at": "RFC3339"}]}
```

- `id > since` を `id ASC` で返す。`since` 省略時は全件（`since=0` と同義）
- `since` が非数値、`id` が非数値 → 400。存在しない auction → 404
- 初期実装の遅さ: `auction_id` にインデックスが無く `WHERE auction_id=? AND id>?` がフルスキャン。加えて入札1件ごとに `SELECT name FROM users WHERE id=?` の N+1

### `GET /notifications` — 通知一覧（要ログイン）

```
res:  200 {"notifications": [{"id": int, "type": "outbid"|"won",
                              "auction_id": int, "message": string,
                              "is_read": bool, "created_at": "RFC3339"}]}
```

- 自分宛のみ。`id DESC`（= `created_at` の降順と一致。同時刻の順序が決まるので id を使う）
- 既読化APIは作らない（YAGNI）。`is_read` は常に `false` を返す。カラムは将来用に残置
- 初期実装の遅さ: `user_id` にインデックスが無くフルスキャン

### `GET /stats/me` — 売上ダッシュボード（要ログイン）

```
res:  200 {"listed_count": int, "sold_count": int,
           "total_sales": int, "live_count": int}
```

- `listed_count`: 自分が出品した総数、`sold_count`: うち落札者が付いて closed になった数、`total_sales`: その `winning_price` 合計、`live_count`: 現在 live の数
- 初期実装の遅さ: `seller_id` にインデックスが無く `auctions` を全走査。集計を1本のクエリにまとめず、4回に分けて走査する

### `GET /auctions/:id` — 詳細（既存を拡張）

レスポンスに `winner_id`（`int|null`）と `winning_price`（`int|null`）を追加する。

これは Phase 2b のバックログ「winner_id/winning_price がAPIに露出しておらず落札結果をE2Eで検証できない」の回収であり、Validation フェーズの落札照合はこのフィールドを使う。`closed` 以外では常に `null`。

## 時間軸の設計

`POST /initialize` は seed SQL 投入後、live オークション（id 1〜10）の `starts_at` / `ends_at` を初期化時刻基準で書き換える。オフセット（秒）は以下で固定する。

| auction | ends_at オフセット | 走行中の挙動 |
|---|---|---|
| 4 | +12 | 走行中に closed |
| 2 | +20 | 走行中に closed |
| 8 | +28 | 走行中に closed |
| 6 | +36 | 走行中に closed |
| 10 | +44 | 走行中に closed |
| 1 | +3600 | 走行中は live 維持 |
| 3 | +3660 | 走行中は live 維持 |
| 5 | +3720 | 走行中は live 維持 |
| 7 | +3780 | 走行中は live 維持 |
| 9 | +3840 | 走行中は live 維持 |

設計上の要点が3つある。

**1. 半分だけ閉じる。** 10件すべてを走行中に閉じると後半の入札先が枯渇する。半数を長時間 live のまま残し、そこに常時入札が集まるようにする。閉じる5件が落札 Validation の対象になり、出品者シナリオが作る新規出品がさらに対象を増やす。

**2. id 順と ends_at 順を非相関にする。** 上表の ends_at 昇順は `4, 2, 8, 6, 10, 1, 3, 5, 7, 9` で、id 昇順と一致しない。これは Phase 2b バックログ「現シードは id順=ends_at順 が相関しているため `ORDER BY id ASC` と区別不能」の回収である。一覧の `ORDER BY ends_at ASC` を `ORDER BY id ASC` に書き換えると Prepare が即座に落ちるようになる。

**3. auction 1 は live 側に置く。** Prepare の既存の詳細検証（auction 1 の説明文・開始価格・シード入札列 1500/1200/1000）が走行を通して安定するようにする。

**Prepare が最初のクローズを追い越さないこと。** Prepare は10件すべてが live である前提で一覧を照合するため、Prepare 完了前に auction 4（+12秒）が閉じると false-FAIL する。Phase 2b-1 時点の Prepare 実測は **0.6〜1.2秒**（`-prepare-only` を3回、初期化込み）で、+12秒まで約10倍の余裕がある。Phase 3 で Prepare に検証を足しても十分だが、**Prepare が6秒を超えたら**このオフセット表を見直す（またはクローズ対象を Prepare 後にずらす）。

`starts_at` は live 群すべて「初期化時刻 −1時間」。closed の auction 11 は seed の固定値のまま（過去の落札実績として一覧・集計に残す）。upcoming の auction 12 は `starts_at = +300`、`ends_at = +600` に書き換え、走行中 upcoming のまま維持する。

**Prepare 側の変更**: `expectedInitialAuctions` の `ends_at` 厳密照合は成立しなくなる。`/initialize` の応答受信時刻を基準に「オフセット ± 許容幅」で照合する方式に変える。許容幅は初期化処理の所要時間ぶんを見込んで **±5秒** とする。順序（ends_at ASC）の検証は許容幅と無関係に成立するので、そのまま維持する。

## 終了処理バッチ

アプリ起動時に goroutine を1本立て、1秒間隔で以下を実行する。

1. `SELECT id FROM auctions WHERE status='live' AND ends_at <= NOW()`（インデックス無しのフルスキャン）
2. 該当を1件ずつ、以下をトランザクションで逐次処理
   - `SELECT ... FROM auctions WHERE id=? FOR UPDATE`（既に closed なら skip = 冪等）
   - `SELECT user_id, amount FROM bids WHERE auction_id=? ORDER BY amount DESC, id ASC LIMIT 1`（インデックス無し）
   - 入札あり: `winner_id` / `winning_price` を設定して `status='closed'`、winner へ `won` 通知を INSERT
   - 入札なし: `winner_id` / `winning_price` は NULL のまま `status='closed'`
3. エラーはログに出して次の1件へ進む（バッチ全体は止めない）

**closed への入札は 400。** 走行中に閉じるため、`POST /auctions/:id/bids` が「さっきまで live だったのに 400」を返すのは正常動作である。ベンチの入札者シナリオはこれを critical にせず、対象オークションを選び直す。ここを取り違えると Load が false-FAIL するので、実装時の注意点として明記する。

**最高額の決め方**は `amount DESC, id ASC` で一意に定める。入札APIが厳密単調増加を保証しているため同額は存在しないはずだが、`id ASC` を付けて決定性を担保する（同額が現れる = `FOR UPDATE` 不在の兆候で、既存の単調増加検証が critical で捕まえる）。

## 通知

**発火点は2つ。**

1. **`outbid`** — `POST /auctions/:id/bids` が入札を受理したトランザクション内で、そのオークションに入札済みの全ユーザー（今回の入札者を除く、重複排除）へ1行ずつ INSERT
2. **`won`** — 終了処理バッチが落札者を確定したトランザクション内で、winner へ1行 INSERT

`message` は人間可読の文字列（例: `"「ヘリテージ・ウィングチェア」で他のユーザーに競り負けました"`）。ベンチは `type` / `auction_id` / `user_id` で照合し、`message` の中身は検証しない（文言変更で壊れないようにする）。

## ベンチマーカーの設計

### シナリオ

既存の入札者・ウォッチャーに加えて2つ追加する。

- **出品者シナリオ** — `POST /auctions`（`duration_seconds` は 20〜40秒のランダム）→ `pubsub` で auction id を入札者へ配信 → `GET /stats/me` を確認。走行中に閉じる出品を作り続けることで落札 Validation の対象を増やす
- **通知確認シナリオ** — 入札者として参加しているユーザーが `GET /notifications` を定期的に見に来る。Load 中のスコア源であり、欠落検証そのものは Validation フェーズで行う

入札者シナリオは `pubsub` を購読し、新規出品が流れてきたらそれを入札対象の候補に加える。

### 台帳（`bench/ledger.go`）の拡張

既存の pending-intent 台帳に、Phase 3 の検証に必要な事実を追加で記録する。

- **outbid イベント** — 入札 `B` が auction `A` で受理されたとき、それ以前に `A` の最高額だったユーザー `U` は「抜かれた」。`(U, A)` の組を記録する
- **フィード反映の観測** — 入札 `B` の 201 受領時刻と、フィードに `B.id` が現れた観測時刻

### 検証項目

| 検証 | フェーズ | 違反時 |
|---|---|---|
| 自分の入札が2秒以内にフィードに現れる | Load | critical |
| フィードが `id ASC` で返る / `since` より大きい id のみ返る | Load | critical |
| closed の `winner_id` が台帳の最高額入札者と一致 | Validation | critical |
| closed の `winning_price` が台帳の最高額と一致 | Validation | critical |
| 入札ゼロで閉じた auction の `winner_id` が null | Validation | critical |
| outbid 通知の欠落（期待下限 ≦ 受信した outbid 通知数） | Validation | critical |
| 落札者に `won` 通知が届いている | Validation | critical |
| 通知が自分宛のみ（他人宛が混ざらない） | Load | critical |
| 通知一覧が `id DESC` で返る | Load | critical |

**pending 入札の扱い。** 結果不明（pending）の入札がある auction では、落札者が「pending を含めた最高額入札者」と「含めない最高額入札者」のどちらでもありうる。既存の `reconcile.go` と同じ考え方で、**両方を許容集合として持ち、そのいずれかに一致すればPASS**とする。通知の欠落検証も同様に「台帳から確実に導ける下限」との比較に留める（下限を下回ったら critical、上回るぶんは許容）。

**outbid 通知の期待下限の定義。** ファンアウトは「そのオークションに入札済みの全ユーザー」宛なので、ユーザー `U` が受け取るべき outbid 通知数は次で定まる。

> `U` が入札した各オークション `A` について、`A` 上で `U` の最初の入札より後に受理された他ユーザーの入札の件数。その総和。

この計算に**確定受理された入札だけを使った値が期待下限**であり、pending 入札は下限に数えない（届いていても届いていなくても許容）。`U` の受信数がこの下限を下回ったら critical とする。「抜かれた回数」ではない点に注意する — ファンアウトは最高額を保持していたユーザーだけでなく入札済み全員に飛ぶため、両者は一致しない。

**時刻の扱い。** 「2秒以内」はベンチ側の単調時計（`time.Now()` の差分）で測る。サーバの `created_at` は使わない（クロックずれに依存させない）。判定はベンチが 201 を受領した時刻を起点とする。

### スコア配点

既存に3タグを追加する（親ドキュメントの配点例に合わせる）。

| タグ | 配点 |
|---|---|
| `GET /auctions`（既存） | 1 |
| `GET /auctions/:id`（既存） | 1 |
| `POST /auctions/:id/bids`（既存） | 5 |
| `POST /auctions`（出品） | 5 |
| `GET /auctions/:id/bids`（フィード） | 1 |
| `GET /notifications` | 2 |

`GET /stats/me` は出品者シナリオの一部として叩くが、スコア源にはしない（出品者シナリオの主役は出品であり、集計の重さは「出品スコアを稼ぐと道連れで重い」形で効かせる）。

## 仕込み（意図的な遅さ）— Phase 3 追加分

`docs/phase2-notes.md` のインベントリに以下を追記する。writeup に記載し、誤って"修正"しない。

- 入札フィード: `auction_id` インデックス無しのフルスキャン + 入札ごとの user 名 N+1
- 通知一覧: `user_id` インデックス無しのフルスキャン
- 通知ファンアウト: 入札トランザクション内で入札者ごとに1行ずつ INSERT（バルク INSERT でない）
- 終了処理バッチ: 毎秒フルスキャン + 1件ずつ逐次処理 + `bids` の非インデックス走査
- `GET /stats/me`: `seller_id` インデックス無しの全走査を4クエリに分けて実行

## テスト戦略

親ドキュメントの方針を踏襲する。

- **ベンチの検証ロジックはユニットテストを厚く**。特に落札照合と通知欠落照合は、`reconcile.go` と同様に**純粋関数として切り出し**、テーブル駆動テストを書く（pending を含む許容集合の扱いが最も間違えやすい）
- **アプリ側はハンドラのHTTPテスト最小限**。終了処理バッチは「1回分の処理」を関数として切り出し、goroutine のループとは独立にテストする
- **ベンチのベンチ**（各サブフェーズ完了時に必ず実施し、結果を `docs/phase3-notes.md` に実測値付きで記録する）
  - 終了処理バッチを止める → 落札 Validation が FAIL する
  - フィードの応答を3秒遅延させる → 反映2秒検証が FAIL する
  - 通知ファンアウトを削る → 通知欠落検証が FAIL する
  - 一覧を `ORDER BY id ASC` に書き換える → Prepare が FAIL する（時間軸の非相関化の効果確認）

## 実装順序

前が後ろの前提になる順に積む。各段階の終わりに「参照実装にベンチをかけて PASS + スコアが出る」ことを確認する。

1. **3-1 時間軸 + 終了処理 + 落札 Validation** — `/initialize` の相対時刻書き換え、Prepare の ends_at 照合を許容幅方式へ変更、終了処理バッチ、詳細への `winner_id`/`winning_price` 露出、closed への入札を入札者シナリオが正常系として扱うようにする、Validation の落札照合
2. **3-2 入札フィード** — `GET /auctions/:id/bids?since=`、入札者シナリオのフィードポーリング、反映2秒検証、フィード順序検証
3. **3-3 通知** — 入札時の outbid ファンアウト、バッチの won 通知、`GET /notifications`、通知確認シナリオ、Validation の欠落照合
4. **3-4 出品** — `POST /auctions`、`GET /stats/me`、出品者シナリオ、`pubsub` によるシナリオ間連携

## 未解決・後続への持ち越し

- **一覧 `GET /auctions` の非トランザクショナルなレース**（Phase 2b からの持ち越し）。Phase 3 でも一覧レベルのフィールド間不変条件は検証しないため誤検知は起きないが、Phase 4 で検索を足して一覧の検証を強化する際には、先に参照実装側の一貫化が必要
- **終了処理バッチの複数台での二重実行**。Phase 5 の IaC でレギュレーションとして扱う
- **`go.mod` の go ディレクティブ不揃い**（webapp 1.26.1 / bench 1.26.4）と **compose の nginx readiness 未設定**。Phase 3 の作業に触れないため、Phase 4 の仕上げでまとめて処理する
