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
- **`applyGeneratedSchedule` の `WHERE id > 12` が必須である**: シードは
  `applyRelativeSchedule` で既に現在時刻基準の絶対時刻になっており、そこへ固定エポック
  起点の変換を当てると `TIMESTAMPDIFF` が約8億3600万秒となり ends_at が2052年へ飛ぶ
- **`generatedEpoch` は意図的に未来日付(2100-01-01)である**: 過去日付だと、ダンプ投入から
  `applyGeneratedSchedule` 完了までの窓で `runAuctionCloser` が生成 live を全件期限切れと
  みなし、`notifications` の採番が進んで明示 id 挿入と衝突する(確率的な初期化失敗)。
  「整地」して過去日付に戻してはいけない。不変条件テスト
  `TestGeneratedEpochIsInTheFuture` で守っている。
- **`applyGeneratedSchedule` は bids → auctions の順を厳守する**: auctions を先に書き換えると
  最速の生成 live が `base+15秒` で確定し、bids のフルスキャン UPDATE がその15秒以内に
  終わらない場合に closer が先に closed へ倒す。すると bids UPDATE の
  `a.status IN ('live','upcoming')` がその入札を黙って飛ばし、入札だけ2100年に取り残される。
  bids を先に流せば auctions はまだ2100年帯なので closer は構造的に何も拾えない。
- **`bids.created_at` も auctions と同じシフトが必要**: 詳細 API は
  `ORDER BY created_at DESC, id DESC` で返し、ベンチはその並びを受理順とみなして
  金額の単調性を検証する。生成入札の時刻を放置すると走行中の新規入札との順序が壊れる。
  不変条件テスト `TestGeneratedLiveAuctionBidsEndBeforeEpoch` で「生成 live の最終入札が
  エポックより前」を守っている(現在の余裕は約35分・最大42入札/オークション)。

### サイジング実測(2026-08-30)

計測はブランチ `phase-4a-initial-data` の HEAD(`6b8fa8e`)を `dev/compose.yaml` で
都度ビルドし直した状態で行った。**このタスクは `small` スケールのみを対象とする**
(`medium`・`full` は別タスクで測る)。

以前(コミット `98f5f24`)に一度計測を行ったが、その結果は全て無効であり本ノートには
転記していない。理由は次の3つの欠陥を踏んでいたためで、いずれも `98f5f24..6b8fa8e` で
修正済みである: (1) 生成データのエポックが過去日付だったため `runAuctionCloser` が
ダンプ投入直後の生成 live を期限切れとみなし `notifications` の採番が明示id挿入と衝突する
競合(60秒走行7回中3回がPrepare段階で失敗)、(2) bench の Validation が生成オークションを
追跡しておらず生成 live への入札が全て `想定外のauctionに入札が受理された` critical になる
不具合(前回は一度も `RESULT: PASS` を観測できなかった)、(3) `applyGeneratedSchedule` が
`bids.created_at` を書き換えておらずエポック移動後に単調増加違反が56件出ていた不具合。
今回の計測はこれら3件が解消済みの HEAD で行った、**唯一の有効な計測**である。

| scale | 生成物サイズ | ゲート1 initialize | ゲート2 Prepare | ゲート3 60秒走行 | ゲート4 FOR UPDATE除去 |
|---|---|---|---|---|---|
| small | 44K(users) + 192K(auctions) + 1.7M(bids) + 562K(notifications) + 38K(snapshot.json) | 3/3 成功(HTTP 200)、0.33〜0.37s。15秒基準に十分な余裕 | 3/3 `PREPARE: PASS`、1.62〜2.63s。6秒基準内 | `RESULT: PASS`、critical 0件、SCORE 6177 | 2回目の試行で検出(`フィードの金額が単調増加でない`/`bids の金額が単調増加違反`, auction 1155)。復元後は `RESULT: PASS` に回帰 |

生成コマンドの出力: `scale=small seed=20260830 users=500 auctions=1075 bids=30000 notifications=4000 -> out`

### 実測ログ

**ゲート1(`POST /initialize` を3回連続)**

```
=== run 1 ===
http_code=200
real 0.37
=== run 2 ===
http_code=200
real 0.36
=== run 3 ===
http_code=200
real 0.33
```

→ 3回とも HTTP 200、15秒基準に対し十分な余裕。**PASS**。

**ゲート2(`-prepare-only` を3回連続)**

```
=== run 1 ===
PREPARE: PASS
real 2.63
=== run 2 ===
PREPARE: PASS
real 1.86
=== run 3 ===
PREPARE: PASS
real 1.62
```

→ 3連続 `PREPARE: PASS`、6秒基準内。**PASS**。

**ゲート3(60秒走行、通常のwebapp)**

```
SCORE: 6177  (raw 6177, penalty 0)
  GET /auctions            : 496回 (496点)
  GET /auctions/:id        : 496回 (496点)
  POST /auctions/:id/bids  : 279回 (1395点)
  GET /auctions/:id/bids   : 279回 (279点)
  GET /notifications       : 513回 (1026点)
  POST /auctions           : 497回 (2485点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

→ `RESULT: PASS`、critical 0件。**PASS**。

**ゲート4(`webapp/go/bids.go` の `postBid` から `SELECT ... FOR UPDATE` の `FOR UPDATE` を
一時的に除去し、`docker compose -f dev/compose.yaml build app && docker compose -f dev/compose.yaml up -d`
で再ビルド後に60秒走行。最大3回まで許容)**

1回目(単調増加系criticalは検出せず。ただし `RESULT` 自体は別要因でFAIL):

```
ERR: validation: critical: auction 1411: closed なのに落札者が未設定 (期待: user=19 price=4714)
ERR: validation: critical: user 15: outbid通知が 2件 (期待: 3件以上、欠落の疑い)
SCORE: 6200  (raw 6202, penalty 2)
ERRORS: 2件 (critical: 2件)
RESULT: FAIL
```

2回目(単調増加系criticalを検出):

```
ERR: load: critical: auction 1155: フィードの金額が単調増加でない (id=30157(amount=4348) の次に id=30159(amount=4302))
ERR: validation: critical: auction 1155: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=30159(amount=4302) の直後に id=30157(amount=4348) が来ており単調減少でない)
ERR: validation: critical: user 20: outbid通知が 2件 (期待: 3件以上、欠落の疑い)
SCORE: 6273  (raw 6276, penalty 3)
ERRORS: 3件 (critical: 3件)
RESULT: FAIL
```

2回目で単調増加系criticalの検出に成功したため、3回目は実施していない。ブリーフの
「1回目で検出できなければあと2回まで」の範囲内で検出できており、**ゲート4は small スケールで
PASS**(検出器は生きている)と判定する。

**復元確認(`git checkout webapp/go/bids.go` → `docker compose build app && up -d`)**

`git diff webapp/go/bids.go` は空(`FOR UPDATE` の復元を確認)。再ビルド後のイメージの
manifest ハッシュは Step 1 で最初にビルドした際のものと一致した(`sha256:7ec60b03...`)。

```
SCORE: 6251  (raw 6251, penalty 0)
  GET /auctions            : 497回 (497点)
  GET /auctions/:id        : 498回 (498点)
  POST /auctions/:id/bids  : 285回 (1425点)
  GET /auctions/:id/bids   : 285回 (285点)
  GET /notifications       : 518回 (1036点)
  POST /auctions           : 502回 (2510点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

→ 通常走行が `RESULT: PASS` に回帰したことを確認した。
