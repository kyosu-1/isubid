# Phase 4-B: 一覧の一貫性・ページネーション・検索

作成日: 2026-08-30
ブランチ: `phase-4b-list-and-search`

## 背景

Phase 4-A で本番規模の初期データを載せられるようになった。その過程で `GET /auctions`
について3つの問題が確定した。

1. **読み取り一貫性が無い**。`getAuctions` は `h.db` を直接 `summarize` に渡している
   (トランザクション無し)。`summarize` は1オークションあたり3クエリを発行するため、
   同一オークションの `current_price` と `bid_count` が別の瞬間の値になりうる。
   詳細(`getAuction`)は Phase 2b-1 で単一トランザクションに包んで解決済みだが、
   一覧は未対応のまま残っていた(`docs/phase2-notes.md` のバックログ項目)。

2. **一覧に上限が無い**。

   ```sql
   SELECT ... FROM auctions WHERE status = 'live' ORDER BY ends_at ASC, id ASC
   ```

   `LIMIT` が無いため、live 件数がそのまま N+1 の N になる。4-A のサイジング実測が
   これを裏付けた。

   | スケール | live 件数 | 発行クエリ数 | 60秒でさばけた `GET /auctions` |
   |---|---|---|---|
   | `small` | 60 | 180 | 496回 |
   | `medium` | 100 | 300 | 112回 |
   | `full` | 200 | 600(各々が30万行の bids を非インデックス走査) | **0回(全件タイムアウト)** |

   つまり `full` が崩壊したのは「N+1 だから」ではなく「**N が無制限だから**」。
   実サイトは必ずページングする。ここを誤診したまま 4-E に入ると、本来維持すべき
   仕込み(N+1)を弱める方向の対策を打ちかねない。

3. **検索が未実装**。当初設計文書
   (`docs/superpowers/specs/2026-07-08-isubid-design.md:77`)は
   `GET /auctions?category=&q=` を「`LIKE '%q%'` フルスキャン、index欠如」という
   仕込みとして既に宣言しているが、実装されていない。

この3つは独立ではない。**トランザクション化が `total_count` を検証可能にし、
ページネーションが検索を現実的な負荷にする。** まとめて 4-B で扱う。

## スコープ

含む:

- `getAuctions` のトランザクション化(読み取り一貫性)
- `GET /auctions` のページネーション(`?page=`、レスポンス形の変更)
- 検索(`?q=`)とカテゴリ絞り込み(`?category=`)
- スナップショットへの `description` 追加とベンチ側の検証拡張
- 負荷シナリオの変更(ページ・検索の回遊、bidder のページ1集中)
- 4-A の持ち越し7(Prepare の未文書の締切)の前倒し修正
- `small` での非回帰確認と、`medium` / `full` の1回ずつの探り

含まない:

- 採用スケールの再決定(4-E)
- ベンチの合否判定の欠陥(4-A 持ち越し2、4-E)
- `/api` プレフィックスへの移行(4-D)
- アイコン BLOB(4-C)

## 1. API 契約

エンドポイントは既存パスのまま。新エンドポイントは作らない。

```
GET /auctions?q=<文字列>&category=<数値>&page=<数値>
```

| パラメータ | 既定 | 不正時 |
|---|---|---|
| `page` | 1(1-origin) | 非数値 / パース不能 / < 1 → **400** |
| `q` | 空(絞り込み無し) | 255 rune 超 → **400**(`title` の上限に揃える) |
| `category` | 空(絞り込み無し) | 非数値 → **400**、存在しない id → **200 で空結果** |

`per_page` は **20 固定のサーバ定数**。レスポンスには含めず、参加者向けマニュアル
(4-F)に明記する。

存在しない `category` を 400 にしないのは、`categories` への追加クエリと追加の
ベンチ検証が要るわりに、出題として得るものが無いため。非数値だけを 400 にする。

### レスポンス

```json
{
  "auctions": [ /* 従来の auctionSummary と同一 */ ],
  "total_count": 137,
  "has_next": true
}
```

- `auctions` は該当が無くても `null` ではなく `[]`
- `total_count` は絞り込み条件を適用した後の **live 総件数**(ページ内件数ではない)
- `has_next` は `page * 20 < total_count`

これは破壊的変更である。従来は裸の JSON 配列を返していた。影響範囲は §6 に列挙する。

### 形の選定理由

`{items, total_count, has_next}` を選んだ理由は3つ。

1. `COUNT(*)` が検索と同じ WHERE 句をもう一度走るため、**LIKE フルスキャンが2回**に
   なる。これ自体が良質な仕込みであり、かつキャッシュ・近似・インデックスという
   複数の攻略線を持つ。ISUCON10(不動産検索)の `{count, estates}` と同じ形。
2. `total_count` の正しさは COUNT と SELECT が同一スナップショットを見ることを要求する。
   すなわち **一貫性の修正(トランザクション化)が無いと、正しい実装でも
   「`total_count` が 137 なのに全ページ合計が 138 件」が正常系で起きる**。
   3つの変更が噛み合う。
3. ページ番号 UI(4-D の SPA)が作れる。裸の配列 + `?page=` だと、SPA は
   「件数が 20 ちょうどなら次がある」と推測するしかなく、最終ページが
   ちょうど 20 件のときに誤判定する。

## 2. 参照実装(仕込みの置き場所)

```
tx := h.db.BeginTxx(ctx, nil)                        ← 修正1: 読み取り一貫性
defer tx.Rollback()

    SELECT COUNT(*) FROM auctions WHERE <cond>
    SELECT <auctionColumns> FROM auctions WHERE <cond>
           ORDER BY ends_at ASC, id ASC
           LIMIT 20 OFFSET (page-1)*20
    for each row: h.summarize(ctx, tx, &row)         ← N+1 は温存
```

```
<cond> = status = 'live'
         [ AND (title LIKE ? OR description LIKE ?) ]   -- どちらも '%' + q + '%'
         [ AND category_id = ? ]
```

`getAuction`(詳細)が既に採っている構造をそのまま一覧へ広げる形になる。
`summarize` は `queryer` インターフェース越しに `*sqlx.Tx` を受け取れるよう
Phase 2b-1 で用意済みなので、新しい抽象は要らない。

### 仕込みの内訳

現行スキーマ(`webapp/sql/00_schema.sql`)には **PRIMARY KEY と `users.name` の
UNIQUE 以外インデックスが1つも無い**。したがって:

| # | 仕込み | 参加者の想定改善 |
|---|---|---|
| 1 | `LIKE '%q%'` の先頭ワイルドカードは B-tree が原理的に効かず、常に全行スキャン | FULLTEXT / N-gram パーサ、外部検索エンジン、転置索引 |
| 2 | `COUNT(*)` が同じ WHERE をもう一度走る(**フルスキャン2回**) | 件数のキャッシュ、`SQL_CALC_FOUND_ROWS` 相当の一本化、近似 |
| 3 | `status` / `ends_at` / `category_id` にインデックス無し。検索無しの素の一覧すら全スキャン + filesort | `(status, ends_at, id)` の複合インデックス、`(category_id, status)` |
| 4 | `summarize` の N+1(3クエリ/件) | JOIN 化、`current_price` / `bid_count` の非正規化 |
| 5 | OFFSET が深いほど捨てる行が増える | カバリングインデックス、seek 法 |

**N は live 全件から 20 に制限されるが、N+1 の仕込み自体は温存される。** これは
意図的な再バランスである。従来は N が無制限であるがゆえに大規模スケールで
`GET /auctions` が完走すらせず、「遅い」ではなく「動かない」になっていた。
上限を入れることで、遅いまま土俵に乗る。

## 3. ベンチ検証

### 3-1. スナップショットの拡張

`SnapshotAuction` に `Description` を追加する(`initial-data/snapshot.go` と
`bench/snapshot.go` の両方)。あわせてベンチ側の `expectedAuction`
(`bench/validate.go`)にもシード10件分の `Description` を足す。
生成データの再生成と再コミットが必要になる。

理由は2つ。

1. 検索の期待集合を、「たまたま description に出ない語をプローブに選んだ」という
   暗黙の前提抜きに計算できる。
2. `auctionDetail.Description` は生成オークションについて現状どこからも検証されて
   いない。4-A 持ち越し10(upcoming が何にも検証されていない)と同種の穴が塞がる。

### 3-2. プローブ語

検索の検証に使う語は、生成語彙(`initial-data/generate.go` の `chairNames` /
`chairDescs`)とシードの title / description からそのままコピーする。

| プローブ | 種別 | 出現箇所 | 検出できる改悪 |
|---|---|---|---|
| `エルゴフロー` | **title 専用** | 生成 title(`chairNames`)のみ。どの description にも現れない | 検索が機能していない。`title LIKE` を落とした改善 |
| `職人` | **description 専用** | 生成 description(`職人による手作業の仕上げ`)のみ。どの title にも現れない | **`description LIKE` を落とした改善** |
| `ズンドコベロンチョ` | 該当なし | どこにも現れない | 0件・`total_count: 0`・`has_next: false` を返すこと |

3語がそれぞれ「title のみ」「description のみ」「どこにもなし」に厳密に属することを
`bench` のテストで固定する(シードの title/description と生成語彙の全組み合わせを走査して
確認する)。この分離があるので、`title LIKE OR description LIKE` の片側を落とした改善は
必ずどちらかのプローブで集合不一致になる。

期待集合は Go の `strings.Contains` で計算する。プローブ語が `%` `_` `\` を含まない
ことをテストで固定し、MySQL の `LIKE` と Go の `Contains` が食い違う余地を消す。
また ASCII を含む語は使わない(MySQL の `utf8mb4_0900_ai_ci` は ASCII の大文字小文字を
区別しないが Go の `Contains` は区別するため)。

### 3-3. 期限切れ許容(false-FAIL 回避)

**期待集合の要素は Prepare の途中で closed になりうる。** コミット済みスナップショット
(`small`、live 50件)の `ends_at` オフセットは**最短 15 秒**であり、ページ走査で
リクエスト数が増えた Prepare はこの窓に近づく。シード側はさらに短く、
auction 4 が +12 秒、2 が +20 秒、8 が +28 秒、6 が +36 秒、10 が +44 秒である
(auction 4 の +12 秒は 4-A のゲート2「Prepare 6秒基準」の根拠そのもの)。

全件一覧の照合が `live` 件数の一致を要求している以上この問題は元から存在していたが、
検索の集合照合を足すと露出面が増える。

したがって期待集合の照合は次の非対称なルールにする。

- 期待集合の要素は、**その `ends_at` が既に到来していれば欠けていてよい**
- 述語に合致しない行が返ってきたら、常に異常

これは `ValidateAuctionClosedIfDue` が既に採っている許容と同じ発想である。

**あわせて 4-A の持ち越し7 を前倒しで修正する。** ページ走査で Prepare の
リクエスト数が増えるため、あの未文書の締切(サンプル live オークションの
`ends_at` を Prepare 末尾が跨ぐと `ValidateSnapshotAuctionDetail` が status
不一致で正しいアプリを hard-fail させる)に近づく。持ち越し7 の対策(a)
「期限が実際に到来している場合は `closed` を受理する」をここで入れる。

### 3-4. Prepare(静穏期・厳密照合)

`has_next` が false になるまで `page=1,2,...` を辿り、連結した列に対して既存の
3検査(件数 == シード live + snapshot live、`ends_at` 非減少、各行の内容照合)を
そのまま適用する。ページ境界を跨いだ `ends_at` 非減少は連結によって自動的に見る。

追加する検査:

- `total_count` が全ページで同一、かつ連結件数と一致する
- `has_next == (page * 20 < total_count)`
- 最終ページ以外はちょうど 20 件
- 連結列に id の重複が無い
- **範囲外ページ**(総ページ数 + 1)→ `200` / `auctions: []`(`null` でない)/
  同じ `total_count` / `has_next: false`
- **検索**: §3-2 の3プローブについて、全ページ走査した集合が期待集合と一致
  (§3-3 の期限切れ許容つき)、`total_count` も一致
- **カテゴリ**: `category=1` の集合一致
- **併用**: `q` と `category` の同時指定が **AND** であること(OR 実装を落とす)
- **不正値**: `page=0` / `page=abc` / `page=-1` / `category=abc` /
  255 rune 超の `q` → いずれも **400**

### 3-5. Load(走行中)

走行中は closer が live を減らし、seller が増やす。**オフセット・ページネーションの
ページ境界は足元で動く。** したがって次は決してやらない。

> ページ間の重複が無いこと / 全ページの和が `total_count` と一致すること /
> 2回の取得結果が一致すること

Load で見るのは**単一レスポンス内で完結する不変条件だけ**にする。

- `len(auctions) <= 20`
- `total_count >= len(auctions)`
- `has_next == (page * 20 < total_count)`
- 全行が `status == "live"`(既存の検査を継承)
- `ends_at` が非減少
- `q` 指定時: 全行の **title** が `q` を含む
- `category` 指定時: 全行の `category_id` が一致

**Load のプローブは title 専用のものに限る。** 一覧レスポンスの `auctionSummary` には
`description` が含まれないため、description で一致した行を Load 側は検証しようがない。
description 専用プローブ(`職人`)を Load で使うと、正しい実装が返した行を「述語に
合致しない」と誤判定する。したがって:

- **Prepare** は3プローブ全てを使う(スナップショットから description を知っているため
  期待集合を計算できる)
- **Load** は title 専用プローブ(`エルゴフロー`)と `category` のみを使う

`q` の検査が**一方向**であることが要点。走行中に「期待集合に含まれるのに返ってこない」
のは正常(closed になった、あるいは別ページへ移った)だが、「述語に合致しない行が返る」
のは常に異常。**「返しすぎ」を Load が、「返さなすぎ」を Prepare が見る**という分担にする。

なお Load 中はベンチ自身が出品したオークションが検索結果に混ざりうるが、
一方向の検査なので期待集合の計算は不要であり、影響を受けない。

## 4. 負荷シナリオと採点

### 4-1. シナリオ

現状、bidder と watcher はどちらも `GetAuctions(ctx)` で live 全件を取得し、
その中からランダムに1件を選んでいる。

| ワーカー | 変更 |
|---|---|
| bidder | `page=1`(= 終了が最も近い20件)を取得し、その中から選ぶ。`s.Board.random()`(新規出品)へ 1/2 の確率で逸れる分は維持 |
| watcher | `page` を 1〜3 からランダムに選ぶ。1/3 の確率で `q=エルゴフロー`(title 専用プローブ)または `category=<1..3>` を付ける |
| seller | 変更なし |

bidder のページ1集中は**実サイトの挙動そのもの**(終了間際のオークションに人が
集まる)であると同時に、**4-A の持ち越し1/9(検出器の希釈)への直接の対策**になる。
live 件数が増えても入札対象が「終了が近い20件」に絞られるため、同一オークションへの
同時入札が起きやすくなり、`FOR UPDATE` 検出器(単調増加検査・落札者一致検査)が
スケールを上げても効き続ける可能性が出る。

### 4-2. 採点

`ScoreGETSearch = 2` を新設する(`ScoreGETList` は 1 のまま)。

検索は本問題で最も重い読み取り経路であり、そこを潰すのが主要な攻略線であるため、
配点で誘導するのが出題として素直。ただし 4-A の持ち越し2(合否判定の欠陥)を
踏まえ、配点を増やすことが「遅い全滅」を隠す方向に働かないかは 4-E で確認する。

## 5. 完了ゲート

| ゲート | 内容 |
|---|---|
| G1 | 正しいアプリで **3回連続 PASS**(false-FAIL 無し) |
| G2 | Prepare が 6秒基準内。ページ走査でリクエスト数が増えるため要実測 |
| G3 | 全採点エンドポイントが 0回でないことを**目視で明示的に確認**(持ち越し2 のため `RESULT: PASS` を信用しない) |
| G4 | §5-1 の改悪が全て FAIL する |
| G5 | `small` のスコアが 4-A 水準(3回の実測レンジ)から極端に落ちていない |

### 5-1. G4 の改悪一覧

| # | 改悪 | 検出する検査 |
|---|---|---|
| 1 | `LIKE '%q%'` → `LIKE 'q%'`(前方一致化) | Prepare の検索集合照合 |
| 2 | `description` 側の条件を落とす | プローブ `職人`(description 専用)の集合照合 |
| 3 | `q` と `category` を AND ではなく OR で繋ぐ | Prepare の併用検査 |
| 4 | `total_count` を `len(auctions)` で返す | Prepare の `total_count` 一致検査 |
| 5 | `ORDER BY ends_at` を落とす | `ends_at` 非減少(Prepare / Load 両方) |
| 6 | `LIMIT` を無視して全件返す | `len(auctions) <= 20` |
| 7 | 入札の `FOR UPDATE` を削除(既存の検出器) | 単調増加検査。**新シナリオでも依然発火すること**を確認する |

### 5-2. 検出できないもの(明記)

`COUNT` をトランザクションの外に出す改変は、`total_count` と `auctions` の
食い違いが**理論上のレースとしてしか現れない**ため、確定的には検出できない。
一貫性の担保はレビューとコード上の構造(単一トランザクション)に委ね、
ベンチによる検出は期待しない。この判断を明記しておく。

## 6. 影響範囲

| ファイル | 変更 |
|---|---|
| `webapp/go/auctions.go` | `getAuctions` の全面書き換え(トランザクション・ページング・検索) |
| `webapp/go/auctions_test.go` | 一覧テストを新レスポンス形へ。ページング・検索・不正値のテストを追加 |
| `bench/client.go` | `GetAuctions` のシグネチャ変更(オプション引数 `page` / `q` / `category`、戻り値をレスポンス構造体へ) |
| `bench/validate.go` | `ValidateAuctionListWithSnapshot` の書き換え、検索・カテゴリ・不正値の検証追加、`expectedAuction` への `Description` 追加、持ち越し7 の修正 |
| `bench/scenario.go` | Prepare のページ走査・検索検証の呼び出し |
| `bench/load.go` | bidder / watcher のシナリオ変更、単一レスポンス不変条件の検査 |
| `bench/score.go` | `ScoreGETSearch` の追加 |
| `bench/snapshot.go` | `Description` フィールド追加 |
| `initial-data/snapshot.go` | `Description` フィールド追加 |
| `initial-data/out/snapshot.json` | `description` 追加のため `small` を再生成・再コミット(SQLダンプ `91`〜`94` は内容が変わらないが、決定論性の確認のため同時に生成し直す) |
| `docs/phase4-notes.md` | 4-B の実測と持ち越しの追記 |

## 7. 4-D への影響

4-D(SPA フロントエンド)の設計は一覧画面を既に「一覧(検索・カテゴリ絞り込み)」と
記述しており、エンドポイントのパスも変わらないため、4-D の設計文書に矛盾は生じない。
ただし 4-D の実装時には次を反映すること。

- `GET /api/auctions` のレスポンスがオブジェクトになった(`src/api.ts` の型)
- 一覧画面にページ送り UI と検索フォームが必要
- `/` のクエリ文字列(`?q=`, `?category=`, `?page=`)を SPA のルーティングに載せるか、
  コンポーネント内の状態に留めるかは 4-D で決める

## 8. medium / full の探り

`small` で全ゲートを通した後、`medium` と `full` を各1回ずつ走らせ、記録するのは
1点だけ。

> ページネーション導入後、`GET /auctions` は `medium` / `full` で完走するか
> (4-A では `full` で 0 回だった)

**採用スケールはここでは変えない。** 結果を `docs/phase4-notes.md` に追記し、
4-E の採用スケール再決定への入力とする。
