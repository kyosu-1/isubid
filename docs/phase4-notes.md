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
- **一覧の順序検証を完全一致から性質検証へ**: live が合計60件(シード10 + 採用スケール
  `small` の生成50)になるとシードと生成分が ends_at 順で交互に並び、期待 id 列との
  完全一致は同着やミリ秒のズレで壊れる。「(a) ends_at が非減少であること」の1性質に
  置き換えた。当初検討した「(b) id 昇順にソートされていない」は実装段階で到達不能
  (シードと生成データの ends_at 順はどちらも id 順と相関しないよう作られているため、
  ends_at 昇順の一覧が同時に id 昇順にもなることは無い)と判明し削除したため、
  (a)単独で実装している。`ORDER BY id ASC` への書き換えは、id 順と ends_at 順が
  相関しない前提のもとでは必ず(a)の非減少性チェックに引っかかるため、検出力は
  落ちない(`bench/validate.go` の `ValidateAuctionListWithSnapshot` 参照)
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

計測はブランチ `phase-4a-initial-data` の HEAD(`6b8fa8e`、`small` 実測時)/
`7a15223`(`medium`・`full` 実測時。差分は前者のドキュメントコミットのみで
`webapp/go`・`bench`・`initial-data/*.go` は無変更)を `dev/compose.yaml` で
都度ビルドし直した状態で行った。3スケールとも同じ4ゲート(ゲート1
`POST /initialize`・ゲート2 `-prepare-only`・ゲート3 60秒走行・ゲート4
`FOR UPDATE` 除去)で測っている。

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
| small | 44K(users) + 192K(auctions) + 1.7M(bids) + 562K(notifications) + 38K(snapshot.json) | 3/3 成功(HTTP 200)、0.33〜0.37s。15秒基準に十分な余裕 | 3/3 `PREPARE: PASS`、1.62〜2.63s。6秒基準内 | `RESULT: PASS`、critical 0件、SCORE 6177 | 2回目の試行で検出(`フィードの金額が単調増加でない`/`bids の金額が単調増加違反`, auction 1155)。復元後は `RESULT: PASS` に回帰。**PASS** |
| medium | 179K(users) + 747K(auctions) + 6.8M(bids) + 2.3M(notifications) + 65K(snapshot.json) | 3/3 成功(HTTP 200)、0.72〜0.85s。15秒基準に十分な余裕 | 3/3 `PREPARE: PASS`、4.31〜4.41s。6秒基準内だが small(1.62〜2.63s)より明確に悪化 | `RESULT: PASS`、critical 0件、SCORE 4129 | 3回とも単調増加系criticalを検出できず(1・2回目は `RESULT: PASS`、3回目は無関係な通知criticalで `RESULT: FAIL`)。**FAIL** |
| full | 449K(users) + 1.9M(auctions) + 17,378,884 bytes(bids, `ls -la` 実測) + 5.7M(notifications) + 120K(snapshot.json) | 3/3 成功(HTTP 200)、1.48〜1.60s。15秒基準に十分な余裕 | 3/3 `PREPARE: PASS` だが 11.47〜12.31s。**6秒基準を超過**(seed auction 4 の +12秒窓に対する安全余裕が消失) | `RESULT: PASS`、critical 0件、SCORE 3323。ただし `GET /auctions` が60秒間まるごとタイムアウト(0回成功、60件のtimeoutエラー)、`POST /auctions/:id/bids` も0回 | 3回ともタイムアウトで入札が1件も成立せず(0回)、単調増加系criticalの検出機会そのものが失われた。**FAIL** |

生成コマンドの出力:
- small: `scale=small seed=20260830 users=500 auctions=1075 bids=30000 notifications=4000 -> out`
- medium: `scale=medium seed=20260830 users=2000 auctions=4150 bids=120000 notifications=16000 -> out`
- full: `scale=full seed=20260830 users=5000 auctions=10300 bids=300000 notifications=40000 -> out`

読み込み後のDB行数(`SELECT COUNT(*)`、シード込み):
- medium: users=2020, auctions=4162, bids=120008, notifications=16000
- full: users=5020, auctions=10312, bids=300008, notifications=40000

いずれもシード(users 20 / auctions 12 / bids 8)+生成分と一致しており、規模と整合している。

### 採用規模の決定

**4つのゲートをすべて満たす最大のスケールは `small` である。採用スケールは `small` とする。**

- `medium` はゲート4(`FOR UPDATE` 除去の検出)で不合格になった。3回の試行のうち
  1・2回目は単調増加系criticalが1件も出ず `RESULT: PASS`(検出漏れ)、3回目は
  `RESULT: FAIL` にはなったものの内容は `user 14: outbid通知が0件`という無関係な
  criticalで、ブリーフが名指しする `bids の金額が単調増加違反` /
  `フィードの金額が単調増加でない` はどの試行でも観測できなかった。live auction が
  50件(small)→100件(medium)に増え、入札が薄く分散したことで、同一オークションへの
  同時入札が起きにくくなり検出器の感度が落ちたと考えられる — これは仕様の
  「Global Constraints」に記した「生成 live オークションの ends_at 順が id 順と
  相関しないこと」とは別の懸念で、Phase 3 から持ち越されていた「入札の集中度が
  足りないと単調増加検出器が効かなくなる」というリスクがここで顕在化した。
- `full` はさらに悪く、ゲート2(Prepare 11.47〜12.31s、6秒基準を大幅に超過)と
  ゲート4(3回とも入札が0件でそもそも検出不能)の両方で不合格。加えてゲート3の
  60秒走行でも `GET /auctions` が全件タイムアウトしており(RESULT自体は
  `PASS`と出るがcriticalではなくtimeoutが60件)、`full` は現行の意図的な低速実装
  (N+1・インデックス無し)のままでは10,300件のauctionsを捌けないことを示している。
- 計画の判定ルール「`medium` と `full` の両方がゲート4で不合格なら `small` を採用し、
  判断を4-Eへエスカレーションする」に該当するため、**`small` を採用し、
  `medium`/`full` でのゲート4検出力低下(および `full` のゲート2・ゲート3の
  悪化)を4-E(負荷調整)へ持ち越す。**

### 生成物サイズの扱い

採用スケール `small` の生成物合計は 44K + 192K + 1.7M + 562K + 38K ≈ 2.5MB で、
計画が目安とする「50MBを超えたら GitHub Releases を検討」の閾値を大きく下回る。
そのため生成物はリポジトリに直接コミットする(`initial-data/out/`)。
(参考: `full` を仮に採用していた場合は 449K + 1.9M + 17M + 5.7M + 120K ≈ 25MB で、
それでも50MB以下ではあった。今回サイズはゲート判定に影響していない。)

### 採用スケールでの再現性確認(3回連続、通常のwebapp)

```
run 1: SCORE: 6269  RESULT: PASS
run 2: SCORE: 5801  RESULT: PASS
run 3: SCORE: 5847  RESULT: PASS
```

3回とも `RESULT: PASS`。スコアの振れ幅は 5801〜6269(約7.5%)で、このマシンが
同一セッション内でも約7〜12%振れることを踏まえると妥当な範囲。**Phase 3 のスコアとの
比較は行わない**(初期データが異なり同一条件の比較にならないため)。

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

`git diff webapp/go/bids.go` は空(`FOR UPDATE` の復元を確認)。復元後の再ビルドは全レイヤーが
CACHEDで完了した(`sha256:7ec60b03...`)。Step 1 の最初のビルド時点のハッシュは記録して
いなかったため、両者が一致することの厳密な比較はできていない。

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

### 実測ログ(medium)

**ゲート1(`POST /initialize` を3回連続)**

```
=== run 1 ===
http_code=200
real 0.85
=== run 2 ===
http_code=200
real 0.75
=== run 3 ===
http_code=200
real 0.72
```

→ 3回とも HTTP 200、15秒基準に対し十分な余裕。**PASS**。

**ゲート2(`-prepare-only` を3回連続)**

```
=== run 1 ===
PREPARE: PASS
real 4.41
=== run 2 ===
PREPARE: PASS
real 4.35
=== run 3 ===
PREPARE: PASS
real 4.31
```

→ 3連続 `PREPARE: PASS`、6秒基準内(ただし small の1.62〜2.63sより明確に悪化)。**PASS**。

**ゲート3(60秒走行、通常のwebapp)**

```
SCORE: 4129  (raw 4129, penalty 0)
  GET /auctions            : 112回 (112点)
  GET /auctions/:id        : 112回 (112点)
  POST /auctions/:id/bids  : 66回 (330点)
  GET /auctions/:id/bids   : 66回 (66点)
  GET /notifications       : 522回 (1044点)
  POST /auctions           : 493回 (2465点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

→ `RESULT: PASS`、critical 0件。**PASS**。

**ゲート4(`FOR UPDATE` 除去、最大3回まで許容)**

事前確認: 除去後のバイナリに対し `grep -a -c 'FOR UPDATE' /usr/local/bin/isubid` は `1` を
返した。これは `closer.go` 由来の別の(無関係な)`FOR UPDATE` であり、`bids.go` からの除去
自体はビルドに正しく反映されていることを確認した。
(**→ 4-B で誤りと判明。この grep は改悪の有無にかかわらず常に `1` を返すため、
何も検証していない。訂正は 4-B 節を参照**)

1回目(検出なし):

```
SCORE: 4016  (raw 4016, penalty 0)
  GET /auctions            : 108回 (108点)
  GET /auctions/:id        : 108回 (108点)
  POST /auctions/:id/bids  : 63回 (315点)
  GET /auctions/:id/bids   : 63回 (63点)
  GET /notifications       : 511回 (1022点)
  POST /auctions           : 480回 (2400点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

2回目(検出なし):

```
SCORE: 3972  (raw 3972, penalty 0)
  GET /auctions            : 112回 (112点)
  GET /auctions/:id        : 112回 (112点)
  POST /auctions/:id/bids  : 64回 (320点)
  GET /auctions/:id/bids   : 64回 (64点)
  GET /notifications       : 502回 (1004点)
  POST /auctions           : 472回 (2360点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

3回目(FAILだが単調増加系ではない):

```
ERR: validation: critical: user 14: outbid通知が 0件 (期待: 1件以上、欠落の疑い)
SCORE: 3921  (raw 3922, penalty 1)
  GET /auctions            : 112回 (112点)
  GET /auctions/:id        : 112回 (112点)
  POST /auctions/:id/bids  : 63回 (315点)
  GET /auctions/:id/bids   : 63回 (63点)
  GET /notifications       : 495回 (990点)
  POST /auctions           : 466回 (2330点)
ERRORS: 1件 (critical: 1件)
RESULT: FAIL
```

3回とも `bids の金額が単調増加違反` / `フィードの金額が単調増加でない` を検出できなかった。
**ゲート4は medium スケールで FAIL**。

**復元確認**

`git diff webapp/go/bids.go` は空。再ビルド後の通常走行:

```
SCORE: 3802  (raw 3802, penalty 0)
  GET /auctions            : 109回 (109点)
  GET /auctions/:id        : 110回 (110点)
  POST /auctions/:id/bids  : 66回 (330点)
  GET /auctions/:id/bids   : 66回 (66点)
  GET /notifications       : 476回 (952点)
  POST /auctions           : 447回 (2235点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

→ 通常走行が `RESULT: PASS` に回帰したことを確認した。

### 実測ログ(full)

**ゲート1(`POST /initialize` を3回連続)**

```
=== run 1 ===
http_code=200
real 1.60
=== run 2 ===
http_code=200
real 1.49
=== run 3 ===
http_code=200
real 1.48
```

→ 3回とも HTTP 200、15秒基準に対し十分な余裕。**PASS**。

**ゲート2(`-prepare-only` を3回連続)**

```
=== run 1 ===
PREPARE: PASS
real 11.71
=== run 2 ===
PREPARE: PASS
real 12.31
=== run 3 ===
PREPARE: PASS
real 11.47
```

→ 3回とも `PREPARE: PASS` は出力されたが、11.47〜12.31sは**6秒基準を大幅に超過**しており、
seed auction 4(`ends_at` オフセット+12秒)を Prepare が追い越して false-FAIL になる安全余裕が
事実上消失している。**FAIL**。

**ゲート3(60秒走行、通常のwebapp)**

```
(GET /auctions への timeout エラーが60件連続、詳細は省略)
SCORE: 3323  (raw 3383, penalty 60)
  GET /auctions            : 0回 (0点)
  GET /auctions/:id        : 0回 (0点)
  POST /auctions/:id/bids  : 0回 (0点)
  GET /auctions/:id/bids   : 0回 (0点)
  GET /notifications       : 524回 (1048点)
  POST /auctions           : 467回 (2335点)
ERRORS: 60件 (critical: 0件)
RESULT: PASS
```

判定基準上は `RESULT: PASS` かつ critical 0件で字義通りは**PASS**だが、`GET /auctions` が
60秒間まるごとタイムアウトし1回も成功していない点は重大な劣化として記録する
(入札シナリオの前提となる一覧取得が機能していない)。

**ゲート4(`FOR UPDATE` 除去、最大3回まで許容)**

1回目:

```
SCORE: 3269  (raw 3329, penalty 60)
  GET /auctions            : 0回 (0点)
  GET /auctions/:id        : 0回 (0点)
  POST /auctions/:id/bids  : 0回 (0点)
  GET /auctions/:id/bids   : 0回 (0点)
  GET /notifications       : 512回 (1024点)
  POST /auctions           : 461回 (2305点)
ERRORS: 60件 (critical: 0件)
RESULT: PASS
```

2回目:

```
SCORE: 3295  (raw 3355, penalty 60)
  GET /auctions            : 0回 (0点)
  GET /auctions/:id        : 0回 (0点)
  POST /auctions/:id/bids  : 0回 (0点)
  GET /auctions/:id/bids   : 0回 (0点)
  GET /notifications       : 520回 (1040点)
  POST /auctions           : 463回 (2315点)
ERRORS: 60件 (critical: 0件)
RESULT: PASS
```

3回目:

```
SCORE: 3277  (raw 3337, penalty 60)
  GET /auctions            : 0回 (0点)
  GET /auctions/:id        : 0回 (0点)
  POST /auctions/:id/bids  : 0回 (0点)
  GET /auctions/:id/bids   : 0回 (0点)
  GET /notifications       : 516回 (1032点)
  POST /auctions           : 461回 (2305点)
ERRORS: 60件 (critical: 0件)
RESULT: PASS
```

3回とも `POST /auctions/:id/bids` が0回、つまり入札が1件も成立しておらず、単調増加系
criticalを検出する機会自体が存在しない。**ゲート4は full スケールで FAIL**。

**復元確認**

`git diff webapp/go/bids.go` は空。再ビルド後の通常走行:

```
SCORE: 3311  (raw 3371, penalty 60)
  GET /auctions            : 0回 (0点)
  GET /auctions/:id        : 0回 (0点)
  POST /auctions/:id/bids  : 0回 (0点)
  GET /auctions/:id/bids   : 0回 (0点)
  GET /notifications       : 523回 (1046点)
  POST /auctions           : 465回 (2325点)
ERRORS: 60件 (critical: 0件)
RESULT: PASS
```

`FOR UPDATE` 復元後も `GET /auctions` の全件タイムアウトは変わらない。これは
`FOR UPDATE` 除去とは無関係に、`full` スケール(auctions 10,300件)そのものが
現行の意図的低速実装(N+1・インデックス無し)で捌ける限界を超えていることを示している。

### まとめと持ち越し(4-Eへ)

- **採用スケールは `small`**。`medium`・`full` はいずれもゲート4で不合格
  (「4つのゲートをすべて満たす最大のスケールを採用する」というルールに従った)。
- **持ち越し1: 単調増加検出器の感度低下**。live auction 件数が増えるほど入札が
  分散し、同一オークションへの同時入札(=検出のトリガー)が起きにくくなる。
  `small`(live 50件)では3回中2回目で検出できたが、`medium`(live 100件)では
  3回とも検出できなかった。4-Eで負荷パラメータ(bidders数、入札対象の絞り込み方
  など)を調整し、`medium`/`full` でも検出力を回復できるか検討が必要。
- **持ち越し2: ベンチマーカー本体の欠陥 — エラー上限が絶対件数であるため「遅い全滅」を
  見逃す**
  (**→ 4-E1 で対応済み**。下記の候補案 (a) を採り、合否条件に liveness floor
  (`採点対象の各エンドポイントが最低 max(1, 走行秒数/10) 回は成功していること`)を
  追加した。あわせて `errorPenalty` を 1 → 20 に引き上げている(4-B 持ち越し17)。
  候補案 (b)(エラー上限の割合化)は**未対応のまま 4-E へ持ち越す** —— 理由と
  必要な前提作業は「4-E1」節の持ち越し21を参照。コミット `1582910` / `7ec58c1`。
  以下は当時の記述をそのまま残す)。
  これは `full` スケール固有の症状ではなく、ベンチ(`bench/`)自体の欠陥であり、
  たまたま `full` の実測でその存在が露呈した、という位置づけで記録する。
  `bench/main.go` の合否判定は `pass := criticalCount == 0 && appCount <= errorLimit && total > 0`
  で、`errorLimit`(`bench/score.go`、値は100)は割合ではなく**絶対件数**。`addErr`
  (`bench/load.go`)は Load 終了間際の ctx キャンセル/タイムアウトだけをノイズとして
  握りつぶす設計で、走行中のクライアントタイムアウト(`bench/client.go` で10秒)はそのまま
  `ErrApplication` として計上される。`full` の60秒走行では `GetAuctions` が毎回10秒
  ブロックしてタイムアウトし、bidder/watcher プール(ペーシング無しでループ)がそれぞれ
  60秒で約6回しか周回できず、結果としてアプリエラーは約60件に収まり、100件のしきい値を
  下回った。そのため `GET /auctions`・`GET /auctions/:id`・`POST /auctions/:id/bids`・
  `GET /auctions/:id/bids` が全て0回のまま `RESULT: PASS` が出力された。
  - 合否ルールには「採点対象のいずれかのエンドポイントが1回でも成功すること」を
    要求する項目が無い。最も近い検査である `len(list)==0` → critical(`bench/load.go`)
    は `GetAuctions` が**成功して**空配列を返したときにしか発火せず、タイムアウトした
    場合には発火しない。
  - しきい値が絶対件数であるためインセンティブが逆転している。10秒でタイムアウトする
    「遅い全滅」は60秒間で高々数十件のエラーしか生まないが、即座に5xxを返す
    「速い失敗」は同じ60秒でしきい値を軽く超える。**遅く失敗する実装の方が、速く失敗する
    実装より安全に`PASS`を通ってしまう**、という向きの誤ったインセンティブになっている。
  - 4-Eで検討すべき対策候補: (a) Load中に採点対象の各エンドポイントが最低1回は成功する
    ことを要求する liveness floor を追加する、(b) `errorLimit` を絶対件数から
    試行回数に対する割合(エラー率)に変更する。どちらか一方、あるいは両方の設計を
    4-Eで詰める。
  - この欠陥は `full` に限らず、どのスケールでも「遅く全滅する」タイプの regression が
    起きれば同様に見逃されうる。スケール調整だけでは解決しない、ベンチ側の設計課題として
    切り分けて扱うこと。
- **持ち越し3: `full` スケールでの `GET /auctions` 全滅**。`FOR UPDATE` の有無に
  関係なく、`full`(auctions 10,300件)では60秒走行中 `GET /auctions` が
  一度も成功しなかった(60件のタイムアウト)。意図的な低速実装のままでは
  `full` は現実的な負荷走行の土俵に乗らない。4-Eで負荷調整(bidders/watchers数、
  タイムアウト値、あるいは意図的な遅さの度合い自体)を検討する材料とする。
  (この症状が上記「持ち越し2」のベンチ欠陥によって `RESULT: PASS` のまま見逃されて
  いた点も合わせて参照)
- **持ち越し4: `full` スケールでのPrepare所要時間**。11.47〜12.31sで6秒基準
  (seed auction 4 の+12秒窓由来)を大幅に超過。`full` を将来採用する場合は
  Prepareの高速化、またはseedスケジュールの調整が必要。
- **持ち越し5: 生成データによる一覧のN+1悪化**。`medium`/`full` のゲート3測定でも
  `GET /auctions`(一覧)のスコア配分が small より明確に下がっており(small 496回
  →medium 112回→full 0回)、生成データを足すほど一覧のN+1コストが支配的になる
  傾向が確認できた。4-Bで一覧まわりのチューニング設計を行う際の実測的な裏付けとする。
- **持ち越し6: 生成物サイズ**。今回コミットするのは `small` のみ(合計約2.5MB)。
  `medium`/`full` の生成物(`medium` 約10MB、`full` 約25MB)はいずれもリポジトリに
  コミットしていない(採用スケールではないため)。将来 `full` を採用する場合は
  50MB前後に収まる見込みだが、その時点で再度サイズを確認すること。
- **持ち越し7: Prepareに、gate2の12秒窓とは別の未文書化された締切がもう1つある**
  (**→ 4-B で対応済み**。Task 7 が候補案(a)を採り、`ValidateSnapshotAuctionDetail` に
  「`ends_at` が実際に到来していれば `closed` を受理する」許容を入れた。コミット
  `5fc81c4`。以下は当時の記述をそのまま残す)。
  `BuildSnapshot` は詳細検証用に先頭10件のliveオークションをidで抽出するが、
  live のオフセットはシャッフルされているためこれらは任意のオフセットに着地し
  (コミット済みsnapshotで最小21秒、ジェネレータ設計上は最小15秒)、しかもサンプル
  されたliveオークションは*最後に*fetchされる。Prepareの末尾がそのオフセットを
  跨ぐと `ValidateSnapshotAuctionDetail` がstatus不一致を報告し、正しいアプリを
  hard-failさせる。notesのgate2根拠はseed auction 4の+12秒窓しか名指ししておらず、
  この経路は別物。候補案: (a) 期限が実際に到来している場合は
  `ValidateSnapshotAuctionDetail` が `closed` を受理できるようにする
  (`ValidateAuctionClosedIfDue` が既に行っている許容と同じ発想)、
  (b) サンプル対象をオフセットが長いliveオークションだけに限定する。
- **持ち越し8: モジュール間の定数drift が、誤解を招く症状としてしか検出されない**。
  `generatedEpochLiteral`(`webapp/go`)と `generatedEpoch`(`initial-data`)は別モジュールに
  存在し、両者を機械的に突き合わせる仕組みが無い。片方だけがズレると、Prepareは
  一覧のライブ件数不一致として報告し、原因が一覧エンドポイントにあるかのように
  見えてしまう。同様に `Snapshot.Seed` と `Snapshot.Scale` は `bench/snapshot.go` で
  パースされた後どこからも読まれておらず、古いdumpに新しいsnapshotを当てても
  それ自体では検出されず、下流の症状としてしか気付けない。安価な緩和策:
  起動時にsnapshotのscale/seedをログ出力する、Prepareでアサートできる範囲を
  アサートする。
- **持ち越し9: 生成オークションが closed オークションの証人から除外されている**。
  Validationは触れた生成オークションに対して `reconcileAuction` しか呼んでおらず、
  `reconcileClosedAuction`・`ValidateAuctionClosedIfDue`、そして won通知検査を
  支える winnersマップをスキップしている。false-FAIL回避の観点では正しい判断だが、
  結果として「winner = argmax(bids)」という証人(単調増加検査に次いで強力な
  `FOR UPDATE` 検出器)がseedの10オークションとベンチの一覧取得分しかカバーせず、
  bidderトラフィックの大半が向かう生成オークションはこの検証から漏れている。
  これはmediumスケールで記録済みのgate4検出漏れ(持ち越し1)と同じ希釈メカニズムで
  あり、スケール固有の問題ではなくこちらが本質として名指しされるべきもの。
- **持ち越し10: 生成した25件の `upcoming` オークションは何にも検証されていない**。
  snapshotにもValidationの既知集合にも含まれるが、liveの一覧には出てこず、詳細
  サンプルの対象にもならない。`starts_at`・`ends_at`・title・sellerのいずれかが
  間違っていても検出できない。サンプル対象へupcoming idを2〜3件足すだけで、
  低コストにこの穴を塞げる。
- **持ち越し11: `closeAuction` の期限再チェックがアプリ側の時計を使っている**。
  最終レビューで見つかった stale-id レース(`closeDueAuctions` が id を集めてから
  1件ずつ処理する間に `/initialize` がテーブルを入れ替えると、明示idのダンプゆえに
  古いidが「まだ期限前の新しいオークション」を指す)は、`closeAuction` の
  `FOR UPDATE` 再読み取りに `ends_at` チェックを足して閉じた。ただしそのチェックは
  Go 側の `time.Now().UTC()` で行っており、収集側の `SELECT` は MySQL の `NOW(6)` を
  使っている。**時計の出所が2つある。**
  - 安全側ではある: このガードは DB 時計基準の `SELECT` が既に「期限切れ」と判断した
    close を**却下できるだけ**で、期限前の close を発生させることはない。したがって
    時計のズレは正当な close を遅らせるだけであり、1秒間隔のtickerが再選択するので
    ズレの幅を上限として自己修復する。
  - それでも SQL 側で見るほうが厳密に良い(単一の情報源に戻り、
    アプリとDBの時刻同期への依存が消える)。ISUCON の実運用ではアプリとDBを
    別サーバーに分けるのが定石なので、ズレは机上の話ではない。
  - 4-E で closer 周りに手を入れる際に、同一クエリ内で `NOW(6)` と比較する形へ寄せること。
    なお `SELECT ... FOR UPDATE` の `WHERE` に条件を足すと期限前の行が `ErrNoRows` になり
    `closeDueAuctions` がエラーとしてログに出すため、単純な述語追加ではなく
    「due を計算列として同時に取る」形が要る。
- **持ち越し12: `closeAuction` のトランザクションと `/initialize` の `DROP TABLE` が
  メタデータロックで待ち合う**(このブランチが作った問題ではなく既存の性質)。
  `closeAuction` は `auctions` に `FOR UPDATE` を取ったまま bids の非インデックス走査を
  行うため、その間に走る `00_schema.sql` の `DROP TABLE auctions` はメタデータロック待ちに入る。
  正しさは壊れず、`initScriptTimeout`(10分)で上限は付いているが、持ち越し11のレースと
  隣接する挙動なので、closer に手を入れる回に併せて見ておくこと。

## 4-B 一覧のページネーションと検索

### 設計判断

- **レスポンス形を `{auctions, total_count, has_next}` にした理由**: 4-A まで
  `GET /auctions` は live 全件(60件)を1レスポンスで返しており、1リクエストあたりの
  N+1 コストが際限なく初期データ規模に比例していた。`full` では実際にこれが原因で
  一覧が60秒間まるごとタイムアウトしている(4-A 持ち越し3)。ページネーションを入れる
  にあたり、`total_count` と `has_next` を両方返すのは冗長に見えるが、両方あるからこそ
  **単一レスポンスの中で内部矛盾を検査できる**(`has_next == (page*20 < total_count)`、
  `total_count >= len(auctions)`、`len(auctions) <= 20`)。この自己整合性が改悪4
  (`total_count` を `len(summaries)` にすり替える)を1リクエストで捕まえる仕掛けに
  なっている。カーソルページネーションではこの検査が作れない。
- **`total_count` がトランザクションを必要とする関係**: `COUNT(*)` と本体の `SELECT` を
  別々に読むと、その間に入札や `runAuctionCloser` の close が commit された場合、
  「`total_count` は 137 なのに全ページ合計は 138 件」が**正しい実装でも**起きる。
  ベンチはこれを不整合として報告するので false-FAIL になる。よって `getAuctions` は
  COUNT と SELECT を単一トランザクション(MySQL デフォルトの REPEATABLE READ)の
  同一スナップショットから読む。**意図的なN+1構成(`summarize` が1件あたり3クエリ)は
  そのまま維持し、読み取り一貫性だけを確保している。** 同じ理由で `getAuction` も
  トランザクション化した(`bid_count` と `bids` 件数の食い違い防止)。
- **Load の絞り込み検査は一方向である**: 走行中の一覧・検索レスポンスに対しては
  「述語に合致しない行が返ったら異常」しか見ない。「期待集合にあるのに返ってこない」は
  走行中なら正常でありうる(closed になった、別ページへ移った)ため検査しない。
  「返さなすぎ」を捕まえるのは静穏期の Prepare の仕事、という分業にしている
  (`bench/load.go` の `watcherIteration` 内コメント参照)。
- **Load のプローブが title 専用に限られる理由**: 一覧レスポンスの `auctionSummary` には
  `description` が無い。したがって走行中は「返ってきた行の title がプローブ語を含むか」
  しか検査できず、description 側で一致した行を正しく返す実装を誤って critical FAIL に
  してしまう。そのため Load の `q` 分岐は `probeTitleOnly`(`ワークス`)だけを使い、
  かつ **ベンチ自身が出品するオークションの title / description にプローブ語が
  絶対に混入しない**ことを安全条件としている。この結合はコード上どこにも書かれない
  暗黙の依存なので、`bench/validate_test.go` の `TestSearchProbesAreClassified` で
  固定している(`sellerDescription` にプローブ語を1つ足すだけでこのテストが落ちる)。
- **プローブ語は「語中に出る部分文字列」でなければならない**: 当初スペックが選んだ
  `エルゴフロー` / `職人` はそれぞれ title / description の**先頭**に必ず現れるため、
  `LIKE '%q%'` を `LIKE 'q%'` に変える前方一致改悪が素通りしていた(contains 5件 =
  prefix 5件、contains 13件 = prefix 13件が実DBで確認された)。Task 6 で
  `ワークス`(`メッシュワークス` の index 3)と `手作業`(`職人による手作業の仕上げ` の
  index 5)へ差し替え、「プローブが先頭に来ないこと」をテストで固定した。
- **AND結合プローブに `category=2` を選んだ理由**: `probeTitleOnly` に一致する live の
  カテゴリ内訳は `{1:1, 2:2, 3:2}`。`category=1` は1件しかなく、しかもその1件は
  `ends_at` +27秒で、許容幅を含めると走行開始から20秒強で期待件数が 0 になる
  ——「常に空を返すアプリ」が素通りする。`category=2` の2件は +159秒 / +3859秒 で
  安全側に厚い。
- **Prepare の照合ルールを「件数の厳密一致」から「id集合 + 期限切れ許容」へ緩めた**:
  全ページ走査でリクエスト数が増え、走査中に期待集合の要素が closed へ落ちる窓が
  広がったため。正確なルールは、E = Prepare 開始時の期待集合、D = 走査中に期限が
  到来した部分集合、R = 返ってきた行として:
  - `R ⊆ E`(期待集合外の id が返ったら常に異常)
  - `E \ D ⊆ R`(期限未到来のものが欠けていたら異常)
  - `|E| - |D| ≤ total_count ≤ |E|`
  - **`len(R) == total_count` の厳密一致は捨てる**(走査途中で期限到来すると
    `len(R)` が `total_count` を上回るため)

  検出力が落ちないことは改悪4・改悪6の実測で確認している(下記ゲート4)。

### ゲート1(`POST /initialize` を3回連続)

`docker compose -f dev/compose.yaml down -v` でボリュームごと作り直し、HEAD で
`build app` した状態から測定。

```
=== run 1 ===
http_code=200
curl -s -o /dev/null -w 'http_code=%{http_code}\n' -X POST  -d '{}'  0.01s user 0.01s system 3% cpu 0.334 total
=== run 2 ===
http_code=200
curl -s -o /dev/null -w 'http_code=%{http_code}\n' -X POST  -d '{}'  0.01s user 0.01s system 4% cpu 0.333 total
=== run 3 ===
http_code=200
curl -s -o /dev/null -w 'http_code=%{http_code}\n' -X POST  -d '{}'  0.01s user 0.01s system 3% cpu 0.340 total
```

(シェルが zsh のため `time` の出力形式が 4-A の `real 0.37` 形式と異なる。`total` の
値が 4-A の `real` に相当する。)

→ 3回とも HTTP 200、0.333〜0.340s。15秒基準に対し十分な余裕。**PASS**。

### ゲート2(`-prepare-only` を3回連続、6秒基準)

`go run` のコンパイル時間が計測に混ざらないよう、事前に `go build -o /dev/null .` で
ビルドキャッシュを温めてから測定した(4-A も同一セッション内の連続実行なので同条件)。

```
=== run 1 ===
PREPARE: PASS
go run . -target http://localhost:8080 -snapshot  -prepare-only  0.13s user 0.31s system 24% cpu 1.791 total
=== run 2 ===
PREPARE: PASS
go run . -target http://localhost:8080 -snapshot  -prepare-only  0.13s user 0.34s system 27% cpu 1.747 total
=== run 3 ===
PREPARE: PASS
go run . -target http://localhost:8080 -snapshot  -prepare-only  0.12s user 0.29s system 23% cpu 1.749 total
```

→ 3連続 `PREPARE: PASS`、1.747〜1.791s。**6秒基準内。PASS**。

事前の懸念(ページ走査 + 検索プローブでリクエストが +14 程度増えるため 6秒を超えうる)は
実測では顕在化しなかった。4-A の small が 1.62〜2.63s だったので、むしろ振れ幅が縮んで
いる。ページネーションで1リクエストあたりの N+1 コストが約1/8になった効果が、
リクエスト本数の増加を相殺している。**基準は 6秒のまま維持する。**

### ゲート3(60秒走行、通常のwebapp)

```
SCORE: 19086  (raw 19086, penalty 0)
  GET /auctions            : 2078回 (2078点)
  GET /auctions (検索)       : 1566回 (3132点)
  GET /auctions/:id        : 3627回 (3627点)
  POST /auctions/:id/bids  : 1136回 (5680点)
  GET /auctions/:id/bids   : 1136回 (1136点)
  GET /notifications       : 504回 (1008点)
  POST /auctions           : 485回 (2425点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

内訳の検算(4-A 持ち越し2 を踏まえ `RESULT: PASS` を信用せず目視):
2078×1 + 1566×2 + 3627×1 + 1136×5 + 1136×1 + 504×2 + 485×5
= 2078 + 3132 + 3627 + 5680 + 1136 + 1008 + 2425 = **19086 = raw**。一致。

**採点対象7本すべてが0回でない**ことを確認。→ **PASS**。

> **→ 4-E1 で手順を差し替え。** 以降のフェーズは「採点対象すべてが0回でないことを
> 目視で明示的に確認する」のではなく、**ベンチが出力する `LIVENESS: PASS` 行を確認する**。
> 新しい手順の定義は「4-E1」節の「ゲート3の手順(4-E1 で差し替え)」を参照。
> 上の走行は liveness 判定の実装前なので `LIVENESS:` 行が出ておらず、当時の目視記録を
> そのまま残してある(内訳の検算そのものは引き続き有効な手順である)。

### ゲート4(改悪7種)

各改悪は1つずつ投入し、`docker compose -f dev/compose.yaml build app && up -d` で
再ビルドしてから60秒走行を実行、その後 `git checkout` で復元して再ビルドした。
改悪1〜6は `webapp/go/auctions.go`、改悪7は `webapp/go/bids.go`。

| # | 改悪 | 結果 | 検出したエラー |
|---|---|---|---|
| 1 | `like := "%" + escapeLike(q.Q) + "%"` → `escapeLike(q.Q) + "%"`(前方一致化) | **FAIL(検出)** | `prepare: GET /auctions?q=ワークス (title専用プローブ): 期限前(...)の auction 1018 が結果に含まれていない` |
| 2 | `cond += " AND (title LIKE ? OR description LIKE ?)"` / `args = append(args, like, like)` → `cond += " AND (title LIKE ?)"` / `args = append(args, like)` | **FAIL(検出)** | `prepare: GET /auctions?q=手作業 (description専用プローブ): 期限前(...)の auction 1054 が結果に含まれていない` |
| 3 | `cond += " AND category_id = ?"` → `cond += " OR category_id = ?"` | **FAIL(検出)** | `prepare: GET /auctions?page=1: live以外が混入 (id=728 status="closed")` |
| 4 | `TotalCount: total` → `TotalCount: int64(len(summaries))` | **FAIL(検出)** | `prepare: GET /auctions?page=1: has_next が true (期待: false, total_count=20)` |
| 5 | `ORDER BY ends_at ASC, id ASC` → `ORDER BY id ASC` | **FAIL(検出)** | `prepare: GET /auctions?page=1: ends_at が昇順でない (index 1: id=2 ... の前が id=1 ...)` |
| 6 | `LIMIT ? OFFSET ?` とその引数を削除 | **FAIL(検出)** | `prepare: GET /auctions?page=1: 60件 (期待: 20件以下)` |
| 7 | `postBid` の `SELECT ... FOR UPDATE` から `FOR UPDATE` を削除 | **FAIL(検出、1回目)** | `load: critical: auction 1088: bids の金額が単調増加違反 ...` ほか63件 |

**7種すべてが検出された。ゲート4は PASS。**

改悪1〜6はいずれも Prepare 段階で落ちるため、採点は全項目0回・`SCORE: 0` になる。
改悪7だけは走行を完走したうえで critical を積む。以下、実出力の抜粋。

**改悪1(前方一致化)**

```diff
-		like := "%" + escapeLike(q.Q) + "%"
+		like := escapeLike(q.Q) + "%"
```

投入直後に直接確認したところ、`GET /auctions?q=ワークス` の `total_count` が
5 → 0 に変わっていた(復元後は 5 に戻ることも確認済み)。

```
ERR: prepare: GET /auctions?q=ワークス (title専用プローブ): 期限前(2026-08-30 13:22:32.337588 +0000 UTC)の auction 1018 が結果に含まれていない
SCORE: 0  (raw 0, penalty 1)
RESULT: FAIL
```

**Task 6 でプローブ語を語中一致へ差し替えた効果がここで確認できた。**
旧プローブ(`エルゴフロー` / `職人`)のままだったらこの改悪は素通りしていた。

**改悪2(`OR description LIKE ?` を削る)**

```diff
-		cond += " AND (title LIKE ? OR description LIKE ?)"
-		args = append(args, like, like)
+		cond += " AND (title LIKE ?)"
+		args = append(args, like)
```

```
ERR: prepare: GET /auctions?q=手作業 (description専用プローブ): 期限前(2026-08-30 13:24:58.44684 +0000 UTC)の auction 1054 が結果に含まれていない
SCORE: 0  (raw 0, penalty 1)
RESULT: FAIL
```

**改悪3(`AND category_id` → `OR category_id`)**

```diff
-		cond += " AND category_id = ?"
+		cond += " OR category_id = ?"
```

```
ERR: prepare: GET /auctions?page=1: live以外が混入 (id=728 status="closed")
SCORE: 0  (raw 0, penalty 1)
RESULT: FAIL
```

**検出したのは AND結合プローブではなく、その手前の `GET /auctions?category=1` である。**
`status = 'live' OR category_id = 1` は closed のカテゴリ1を全部拾うので、
期待集合の照合に到達する前に `ValidatePagedListShape` の「live以外が混入」で落ちた。
DB で直接確認したところ `id=728` は `category_id=1, status=closed` であり、この解釈と
一致する。**検出はされるが、検出経路はブリーフの想定(AND結合プローブでの集合不一致)
とは異なる**ため、AND結合プローブそのものの検出力は今回の測定では独立に確認できて
いない(下記の軽微な持ち越しへ)。

**改悪4(`TotalCount` を返却件数にすり替える)**

```diff
-		TotalCount: total,
+		TotalCount: int64(len(summaries)),
```

```
ERR: prepare: GET /auctions?page=1: has_next が true (期待: false, total_count=20)
SCORE: 0  (raw 0, penalty 1)
RESULT: FAIL
```

`HasNext` の式は `total`(本物のCOUNT)を見たままなので、単一レスポンス内の
自己整合性検査が即座に矛盾を検出した。**Task 6 で件数の厳密一致を捨てても
この改悪が捕まることの、実機での裏取りになっている。**

**改悪5(`ORDER BY ends_at ASC, id ASC` → `ORDER BY id ASC`)**

```diff
-			" ORDER BY ends_at ASC, id ASC LIMIT ? OFFSET ?", pageArgs...); err != nil {
+			" ORDER BY id ASC LIMIT ? OFFSET ?", pageArgs...); err != nil {
```

```
ERR: prepare: GET /auctions?page=1: ends_at が昇順でない (index 1: id=2 2026-08-30 13:24:53.298517 +0000 UTC の前が id=1 2026-08-30 14:24:33.298517 +0000 UTC)
SCORE: 0  (raw 0, penalty 1)
RESULT: FAIL
```

**改悪6(`LIMIT ? OFFSET ?` を削る)**

```diff
-	pageArgs := append(append([]any{}, args...), auctionsPerPage, (q.Page-1)*auctionsPerPage)
+	pageArgs := append([]any{}, args...)
 	var rows []auctionRow
 	if err := tx.SelectContext(r.Context(), &rows,
 		"SELECT "+auctionColumns+" FROM auctions WHERE "+cond+
-			" ORDER BY ends_at ASC, id ASC LIMIT ? OFFSET ?", pageArgs...); err != nil {
+			" ORDER BY ends_at ASC, id ASC", pageArgs...); err != nil {
```

```
ERR: prepare: GET /auctions?page=1: 60件 (期待: 20件以下)
SCORE: 0  (raw 0, penalty 1)
RESULT: FAIL
```

**改悪7(`postBid` の `FOR UPDATE` 除去)**

```diff
-		"SELECT "+auctionColumns+" FROM auctions WHERE id = ? FOR UPDATE", auctionID)
+		"SELECT "+auctionColumns+" FROM auctions WHERE id = ?", auctionID)
```

**1回目の走行で検出した(3回まで許容のところ1回で発火)。** 4-A では2回目でようやく
発火し、単調増加系は2件(`load: フィードの金額が単調増加でない` と
`validation: bids の金額が単調増加違反`、どちらも auction 1155)だったのに対し、
今回は critical 63件と大差で発火している。Task 8 で
watcher の `q` 分岐を `page=1` 固定にし、bidder のトラフィックが少数の live に
集中するようになった効果と考えられる。

```
ERR: load: critical: auction 1088: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=30012(amount=4122) の直後に id=30011(amount=4252) が来ており単調減少でない)
ERR: load: critical: auction 1129: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=30648(amount=4534) の直後に id=30647(amount=4553) が来ており単調減少でない)
ERR: load: critical: auction 1129: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=30648(amount=4534) の直後に id=30647(amount=4553) が来ており単調減少でない)
ERR: load: critical: auction 1319: フィードの金額が単調増加でない (id=31124(amount=1339) の次に id=31125(amount=1262))
ERR: validation: critical: auction 1088: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=30012(amount=4122) の直後に id=30011(amount=4252) が来ており単調減少でない)
ERR: validation: critical: auction 1106: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=30124(amount=1472) の直後に id=30123(amount=1538) が来ており単調減少でない)
ERR: validation: critical: auction 1117: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=30182(amount=5125) の直後に id=30181(amount=5177) が来ており単調減少でない)
ERR: validation: critical: auction 1129: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=30648(amount=4534) の直後に id=30647(amount=4553) が来ており単調減少でない)
ERR: validation: critical: auction 1146: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=30515(amount=3933) の直後に id=30514(amount=4033) が来ており単調減少でない)
ERR: validation: critical: auction 1224: winner_id が 1 (期待: 5 = 最高額 4083 の入札者)
ERR: validation: critical: auction 1319: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=31125(amount=1262) の直後に id=31124(amount=1339) が来ており単調減少でない)
ERR: validation: critical: auction 1018: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=30539(amount=15120) の直後に id=30538(amount=15241) が来ており単調減少でない)
ERR: validation: critical: user 6: outbid通知が 149件 (期待: 151件以上、欠落の疑い)
ERR: validation: critical: user 17: outbid通知が 164件 (期待: 165件以上、欠落の疑い)
ERR: validation: critical: user 10: outbid通知が 195件 (期待: 196件以上、欠落の疑い)
ERR: validation: critical: user 11: outbid通知が 166件 (期待: 169件以上、欠落の疑い)
ERR: validation: critical: user 13: outbid通知が 189件 (期待: 191件以上、欠落の疑い)
ERR: validation: critical: user 20: outbid通知が 185件 (期待: 186件以上、欠落の疑い)
ERR: validation: critical: user 19: outbid通知が 117件 (期待: 118件以上、欠落の疑い)
SCORE: 19223  (raw 19286, penalty 63)
  GET /auctions            : 2075回 (2075点)
  GET /auctions (検索)       : 1658回 (3316点)
  GET /auctions/:id        : 3709回 (3709点)
  POST /auctions/:id/bids  : 1126回 (5630点)
  GET /auctions/:id/bids   : 1117回 (1117点)
  GET /notifications       : 502回 (1004点)
  POST /auctions           : 487回 (2435点)
ERRORS: 63件 (critical: 63件)
RESULT: FAIL
```

(採取は `| tail -30` で行ったため、上に転記した ERR 行は**63件のうち末尾19件**である。
先頭側の44件は取りこぼしており復元できない。合計は出力どおり `ERRORS: 63件`。
内訳の検算: 2075 + 3316 + 3709 + 5630 + 1117 + 1004 + 2435 = 19286 = raw。一致。)

**復元確認**

`git checkout webapp/go/bids.go` → `git diff` は空、`git status --short` も空。
再ビルド後の通常走行:

```
SCORE: 19429  (raw 19429, penalty 0)
  GET /auctions            : 2096回 (2096点)
  GET /auctions (検索)       : 1610回 (3220点)
  GET /auctions/:id        : 3681回 (3681点)
  POST /auctions/:id/bids  : 1167回 (5835点)
  GET /auctions/:id/bids   : 1167回 (1167点)
  GET /notifications       : 505回 (1010点)
  POST /auctions           : 484回 (2420点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

→ 通常走行が `RESULT: PASS` に回帰したことを確認した。

**4-A の記述の訂正**: 4-A の medium ゲート4 の節にある「除去後のバイナリに対し
`grep -a -c 'FOR UPDATE' /usr/local/bin/isubid` は 1 を返した。これは `closer.go` 由来の
別の(無関係な)`FOR UPDATE` であり、`bids.go` からの除去自体はビルドに正しく
反映されている」という確認は、**検証として成立していない**。`bids.go:52` と
`closer.go:51` は
`"SELECT "+auctionColumns+" FROM auctions WHERE id = ? FOR UPDATE"` という
**完全に同一の文字列リテラル**を組み立てており、Go のリンカは同一の文字列定数を
1つに畳む。今回、`FOR UPDATE` を**残したまま**のバイナリに対して同じ grep を実行しても
やはり `1` だった(`grep -a -o 'FOR UPDATE' | wc -l` も `1`)。つまりこの grep は
改悪の有無にかかわらず常に 1 を返し、何も判別していない。改悪7の投入が実際に効いて
いることの根拠は、投入前の `git diff` と、走行結果(critical 0件 → 63件)である。

**この訂正の帰結(重要)**: この grep は 4-A において
「**改悪が実際にバイナリに載っている**」ことの**唯一の証拠**だった。それが無効である
以上、**4-A の `medium` / `full` のゲート4 の FAIL 判定には、改悪が実際にデプロイされて
いた証拠が残っていない。** したがって **4-A 持ち越し1(「スケールを上げると単調増加
検出器の感度が落ちる」)は、単にビルドが反映されていなかったことが原因だった可能性を
排除できない。** 4-B ではこの持ち越しを解決していないので、未解決のまま残す
(下記のまとめを参照)。4-E で再測定を設計する際は手順を変えること: 改悪投入時に
`git diff` の出力を記録として残す、バイナリ内で**改悪後の文字列(たとえば
`FROM auctions WHERE id = ?` に続く語)の存在/不在**を確認する、あるいは
アプリの挙動そのものを直接叩いて差を見る(4-B の改悪1では
`GET /auctions?q=ワークス` の `total_count` が 5 → 0 になることを実際に確認した)。

### ゲート5(非回帰)

**4-A の SCORE 6177 との絶対値比較は行わない。** 4-B はページネーションで1リクエスト
あたりのコストが約1/8になり、同じ DB 負荷がはるかに多い HTTP リクエストへ分散する
別ワークロードである。見るのは次の3つの構造的指標(ゲート3の実測を使う)。

| 指標 | 4-A | 4-B(ゲート3実測) | 判定 |
|---|---|---|---|
| 1. `GET /notifications` の回数 | 513 | **504**(0.98x) | ほぼ 1.0x → OK |
| 1. `POST /auctions` の回数 | 497 | **485**(0.98x) | ほぼ 1.0x → OK |
| 2. 採点対象7本が0回でない(→ 4-E1 以降は `LIVENESS: PASS`) | — | 2078 / 1566 / 3627 / 1136 / 1136 / 504 / 485 | すべて非0 → OK |
| 3. `GET /auctions` の回数 | 496 | **2078**(検索1566 を足すと一覧系 3644) | 増加 → OK |

通知ワーカーと出品ワーカーは自エンドポイントのレイテンシで律速されており、一覧の
変更に影響されないという想定どおり、どちらも 4-A から 2% 以内の差に収まっている。
意図しない副作用は観測されなかった。**ゲート5 PASS。**

**4-B 以降のスコア基準は 18000〜19000 台で引き直す。** 今回このマシンで観測した
通常走行の実測は 19086 / 19429 の2本(Task 8 のレビュー実測 18672 / 19134 / 18439 を
含めると 18439〜19429)。4-E で採用スケールを再決定する際は、この帯を出発点にすること。

### `medium` / `full` の探り

`initial-data` を `-scale medium` / `-scale full` で生成し、`ISUBID_INITIAL_DATA_DIR`
(compose の `../initial-data/out:/initial-data:ro` マウント)を生成先へ差し替えて
各1回だけ60秒走行させた。生成物はコミットしていない。**採用スケールは `small` のまま
変更しない。**

生成コマンドの出力:

```
scale=medium seed=20260830 users=2000 auctions=4150 bids=120000 notifications=16000 -> .../isubid-medium
scale=full   seed=20260830 users=5000 auctions=10300 bids=300000 notifications=40000 -> .../isubid-full
```

**記録すべき1点 —— ページネーション導入後、`GET /auctions` は `medium` / `full` で
完走するか(4-A では `full` で 0回だった)**

| scale | `GET /auctions` | `GET /auctions (検索)` | 4-A の `GET /auctions` |
|---|---|---|---|
| medium | **880回** | 418回 | 112回 |
| full | **377回** | 154回 | **0回(60件タイムアウト)** |

**答え: 完走する。`full` でも `GET /auctions` は 377回成功し、採点対象7本すべてが
非0になった。** 4-A 持ち越し3(`full` での一覧全滅)はページネーションによって
解消している。

medium(60秒走行):

```
SCORE: 9174  (raw 9174, penalty 0)
  GET /auctions            : 880回 (880点)
  GET /auctions (検索)       : 418回 (836点)
  GET /auctions/:id        : 1307回 (1307点)
  POST /auctions/:id/bids  : 605回 (3025点)
  GET /auctions/:id/bids   : 605回 (605点)
  GET /notifications       : 398回 (796点)
  POST /auctions           : 345回 (1725点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

full(60秒走行):

```
SCORE: 4316  (raw 4316, penalty 0)
  GET /auctions            : 377回 (377点)
  GET /auctions (検索)       : 154回 (308点)
  GET /auctions/:id        : 539回 (539点)
  POST /auctions/:id/bids  : 270回 (1350点)
  GET /auctions/:id/bids   : 270回 (270点)
  GET /notifications       : 276回 (552点)
  POST /auctions           : 184回 (920点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

参考として Prepare も1回ずつ測った(ゲート判定には使わない):

```
medium: PREPARE: PASS   ... 5.201 total
full:   PREPARE: PASS   ... 14.319 total
```

`medium` は 4-A の 4.31〜4.41s から 5.2s へ、`full` は 11.47〜12.31s から 14.3s へ
悪化している。全ページ走査(`full` の live は **210件 = 11ページ**。
`initial-data/config.go:36` の `LiveAuctions: 200` + シードの live 10件。
`UpcomingAuctions: 100` は `generate.go:185` が `starts_at` を `epoch + 3600+i 秒` に
置くため走行中ずっと upcoming のままで、`webapp/go/auctions.go` の
`cond := "status = 'live'"` により一覧には現れない)と検索プローブ5本
ぶんの増加が効いており、`full` の Prepare は 4-A よりさらに 6秒基準から遠ざかった。
`medium` は依然 6秒基準内。**この探りではゲート4(`FOR UPDATE` 検出)は測っていない**
ので、4-A 持ち越し1(スケールを上げると単調増加検出器の感度が落ちる)が解消したか
どうかは**今回のデータからは判断できない**。

### まとめと持ち越し(4-Eへ)

**ゲート1〜5 はすべて PASS。改悪7種すべてが検出された。**

4-A からの持ち越しのうち、**持ち越し7(Prepare の第2の締切)は 4-B の Task 7 で
対応済み**(上記 4-A 節の該当箇所に追記済み)。持ち越し3(`full` での一覧全滅)は
上記の探りのとおり実質的に解消しているが、`full` のゲート4は未測定のため
「採用スケールの再検討材料が増えた」という位置づけに留める。

**持ち越し1(スケールを上げると単調増加検出器の感度が落ちる)は未解決のまま残す。**
4-B では `medium` / `full` のゲート4 を測っていないので、`page=1` 固定によって
解消したかどうかのデータが無い。加えて上記「4-A の記述の訂正」のとおり、
**4-A の `medium` / `full` のゲート4 FAIL 判定には改悪が実際にデプロイされていた
証拠が残っていないため、持ち越し1 の根拠そのものが揺らいでいる**(ビルドが
反映されていなかっただけ、という可能性を排除できない)。4-E で再測定する際は、
改悪の反映を確実に検証する手順(`git diff` を記録に残す、改悪後の文字列の
存在/不在を確認する、アプリの挙動を直接叩いて差を見る)を先に決めてから測ること。

- **持ち越し13: オフセットページネーションの読み飛ばし**。Prepare の全ページ走査中に
  先頭側の live が closed になると、後続ページの行が手前へずれ、まだ読んでいない行が
  読み飛ばされる。Task 6 で入れた期限切れ許容は「期限が到来して**消えた**行」しか
  救わないため、ずれて隠れた行は「期限前なのに欠けている」として **false-FAIL に
  なりうる**。採用スケール `small` では最短の期限が +12秒(シード auction 4)で
  Prepare 実測が 1.7〜1.8秒なので窓に十分な余裕があるが、`full`(Prepare 14.3秒)では
  危険。候補の対策: **最終ページから逆順に辿る**(行が先頭から消えると逆順走査では
  重複が出るだけで、欠落にはならない = 安全な向きに倒れる)。他に、走査を
  1トランザクション相当のスナップショットで固定する API を足す案もあるが、
  参照実装に検証専用の口を増やすことになるので優先度は低い。
- **持ち越し14: `endsAtTolerance`(5秒)と `POST /initialize` の許容時間のギャップ**。
  `bench/validate.go:41` の `endsAtTolerance = 5 * time.Second` は、Prepare が
  `base`(initialize の応答**後**に採る時刻)と実際の `ends_at`(アプリが基準時刻を
  採った時点)のズレを吸収するためのもの。しかし **initialize の応答が5秒を超えると、
  シード auction 4(+12秒オフセット)はこの許容境界より手前で closed になりえて、
  `ValidateSearchResult` がまだ存在を要求してしまう。** ゲート1は initialize に
  15秒まで、クライアントは60秒まで許しているのに、許容幅だけが5秒固定という設計上の
  不整合がある。実測(本節上記のゲート1)は 0.333〜0.340秒なので現状は実害なし。
  **4-A 由来の設計であり 4-B の回帰ではない**(`endsAtTolerance` の値・
  `auctionEndOffsets`・initialize のタイムアウト設定は、いずれもこのブランチで
  変更していない)。ただし 4-B はこの許容幅への依存箇所を3つ増やした
  (`ValidateSearchResult` / `ValidateSnapshotAuctionDetail` / `expectedLiveMatches`
  の計算経路)。4-E で initialize のレイテンシが悪化する変更(生成データ規模の
  引き上げなど)を扱う際は、この許容幅を initialize の実測レイテンシに連動させる
  (あるいはゲート1の基準に合わせて拡げる)ことを検討する。上記の持ち越し13
  (オフセットページネーションの読み飛ばし)と隣接する懸念なので併せて見ること。
- **持ち越し15: `total_count` をトランザクション外に出す改変は検出できない**。
  `getAuctions` が COUNT と SELECT を単一トランザクションで読んでいることは、
  ベンチでは検出できない。両者を別々に読んだ場合の不整合は、その間に入札や close が
  commit されるという理論上のレースとしてしか現れず、静穏期の Prepare では起きず、
  Load では「返さなすぎ」を検査しない(一方向検査)ため素通りする。**一貫性の担保は
  コード構造(単一トランザクション)とコメントに委ねており、ベンチによる検出は
  最初から期待していない。** 4-E で「参加者がトランザクションを外す改善」を明示的に
  不合格にしたいなら、別の仕掛け(たとえば `total_count` と全ページ合計の突き合わせを
  高頻度で回す専用フェーズ)が必要になる。
- **持ち越し16: 深い OFFSET が Load で一度も踏まれない**。watcher は page 1〜3 しか
  引かず、`q` 分岐は Task 8 の修正で page=1 固定になった。一方、走行終盤には live が
  増えて8ページ程度まで伸びる。つまり **`LIMIT ? OFFSET ?` の「OFFSET が深いほど遅い」
  という意図的な遅さが、Load ではほとんど負荷になっていない。** 参加者から見ると
  深いページの最適化にインセンティブが無い。4-E で watcher のページ選択を
  「実際の `total_count` から算出した範囲」に広げるか検討する。
- **持ち越し17: スコアが約3倍になったことで `errorPenalty = 1` が相対的に弱くなった**
  (**→ 4-E1 で対応済み**。`errorPenalty` を 1 → 20 に引き上げ、上限100件の減点が
  raw 19086 に対して約10.5%になるようにした。コミット `1582910`。ただし
  **絶対値のままである**という本質は変わっておらず、採用スケールを変えれば
  再調整が要る —— 4-E1 節の持ち越し22 を参照。以下は当時の記述をそのまま残す)。
  4-A では critical 100件で 1.6% の減点だったが、4-B のスコア帯(約19000)では
  0.5% にしかならない。4-A 持ち越し2(エラー上限が絶対件数)と同じ軸の問題で、
  **減点もしきい値も、スコアのスケールに追随しない絶対値になっている。**
  4-E で errorLimit の割合化を検討する際に、`errorPenalty` も併せて見直すこと。
- **持ち越し18: `auctionsPerPage = 20` がモジュールを跨いで二重定義されている**。
  `webapp/go/auctions.go` と `bench/validate.go` の両方にあり、別 go.mod なので
  コンパイル時に照合する手段が無い(4-A 持ち越し8 と同じ drift リスク)。
  現状は双方にコメントで相互参照を書いてある。片方だけ変えると Prepare が
  「has_next が true なのに N件」として落ちるので、エラー文が原因を名指しする
  ぶん診断は速い。恒久対策を採るなら、`GET /auctions` のレスポンスに
  `per_page` を含めてベンチがそれを読む形が素直。
- **持ち越し19: MySQL の照合順序(collation)と Go の文字列比較の食い違いが、テストで
  固定されていない**。`webapp/sql/00_schema.sql` は `DEFAULT CHARSET=utf8mb4` のみで
  `COLLATE` を指定していないため、MySQL 8 の既定である `utf8mb4_0900_ai_ci` になる。
  この照合順序は primary weight のみを比較するため、**ひらがな ≡ カタカナ、
  半角 ≡ 全角、小書き ≡ 並字を同一視する**。一方ベンチの期待集合
  (`expectedLiveMatches`)は Go の `strings.Contains`(バイト厳密一致)で計算しており、
  **仮名の異表記が語彙に混入すると `R ⊆ E`(4-B 設計 §3-2 のプローブ不変条件)が破れ、
  正しい実装が false-FAIL しうる。** 設計 §3-2 はこの罠の ASCII 面だけを認識しているが
  (「ASCII を含む語は使わない」)、`bench/validate_test.go` の
  `TestSearchProbesAreClassified` が実際に固定しているのは (1) 3語の分類、
  (2) `%_\` を含まないこと、(3) title/description の先頭に来ないこと の3点のみで、
  **ASCII 不使用も仮名の等価性も固定していない。** 現時点では実害なし(生成タイトルに
  ひらがなはゼロ、半角カナもゼロ、description の5種のひらがな語のいずれにも
  `わーくす` は現れず、`手作業` は漢字表記のみで異表記衝突が無いことを確認済み)。
  候補対策: 同テストに「プローブが ASCII を含まないこと」と「全 title/description を
  仮名種別・幅・大小で正規化しても分類が保たれること」を足す。
- **持ち越し20: 非スナップショット経路の一覧検証だけ期限切れ許容がゼロのまま**。
  `bench/scenario.go` で `-snapshot` を指定せずに走らせた場合の経路
  (`ValidateInitialAuctionList`)だけは、件数10件の厳密一致 + id 列の完全一致で、
  期限切れ許容が無い(シード auction 4 は +12秒でこの窓に収まらないことがある)。
  Phase 3 由来で 4-B は触れていないが、**4-B でスナップショット経路が全面的に
  許容ルール(持ち越し13の隣にある持ち越し14、および本節冒頭の E/D/R ルール)へ
  移行した結果、同じ検証の2経路で厳しさが揃っていない状態になった。** 実運用は
  常にスナップショット経路(`initial-data/out/snapshot.json` をコミット済み)を
  使うため優先度は低いが、4-E で手を入れる際は非スナップショット経路にも同じ
  許容を入れるか、経路自体を削除するか検討すること。
- **軽微な持ち越し(4-E で手が入る際についでに直すもの)**:
  - `ValidatePagedListShape` のエラー文が `GET /auctions?page=%d` しか名乗らないため、
    `q` / `category` 付きのプローブで落ちたときにどのリクエストか判別できない
    (改悪3 の測定で実際に困り、DB を直接引いて特定した)。ラベルを引数で受け取る
    形にすれば解決する。あわせて、**AND結合プローブ(`q=ワークス&category=2`)の
    期待集合照合そのものの検出力は、まだ実機で独立に確認できていない** ——
    改悪3 はその手前の `category=1` プローブで落ちたため。
  - `GetAuctionsRaw` に空文字列を渡すと `"/auctions?"` という末尾 `?` だけの URL に
    なる(現時点で空文字列を渡す呼び出しは無い)。
  - `ValidateSnapshotAuctionDetail` は `now` を引数で受け取らず内部で `time.Now()` を
    呼ぶが、`ValidateSearchResult` は引数で受け取る。機能上の問題は無いが流儀が不統一。
  - 「closed のはずが live」「upcoming が絡む status 不一致」を固定する committed
    テストが無い(動作はレビュー時に手で確認済み)。
  - `bench/validate.go` の期限切れ許容まわりのコメントにある「最短で数十秒」という
    表現が、実測(コミット済み snapshot で21秒、ジェネレータ設計上は15秒)より
    語感が緩い。読み手が余裕を過大に見積もる。
  - `createLiveAuctions` の戻り値 `[]int64` を誰も使っていない。
  - `TestGetAuctionsResponseShape` と `TestGetAuctions` のアサーションが重複している。
  - `?page=`(空文字)を「未指定」と同一視する契約がテストで固定されていない。
  - `TestSnapshotCarriesDescription` だけ `cfg.Seed = DefaultSeed` を明示していない
    (同ファイルの他3テストは明示)。
  - スコア表示の `%-25s` が CJK 文字幅を考慮しておらず、`GET /auctions (検索)` の行だけ
    列がずれる(表示のみの問題)。
- **プロセス上の教訓(4-B で実際に事故になりかけたもの)**:
  - **スペックに書く実データの数字は、コピー元を変えたら必ず再計算すること。**
    「`ワークス` のカテゴリ内訳 {1:3, 2:1, 3:1}」は `エルゴフロー` の数字をそのまま
    流用した誤りで、実値は {1:1, 2:2, 3:2} だった。実装者が実機クエリで発見した。
  - **プローブ語は「語の先頭に来ない部分文字列」を選ぶこと。** 先頭に来る語を選ぶと
    前方一致改悪(`LIKE '%q%'` → `LIKE 'q%'`)が原理的に検出できない。
  - **改悪リストは、実施したものと指定されたものを1つずつ突き合わせること。**
    Task 6 の実装者が実機投入した6種は、指定リストと4種しか重複しておらず、
    前方一致の穴が一度見逃されている。
  - **バイナリへの grep で「改悪が反映されたか」を確かめるときは、同一の文字列
    リテラルが他所にも無いかを確認すること**(上記 4-A の訂正を参照)。
  - **作業の完了報告を受けたら、まず作業主体が本当に停止しているかを確認してから
    リポジトリの状態を検査すること。** Task 5 で、実装者が完了を報告した時点では
    まだ作業が継続しており、報告されたコミットハッシュ `06fdd05` は git オブジェクト
    としても reflog にも存在しなかった(実際の着地は `4a0fbef`)。
    **このプロジェクトで名指しされている2件の事故のうちの1件がこれである**
    (もう1件は報告書のスコア内訳が算術的に成立しなかった件)。
    再発防止は2段構え: (1) 完了報告と実際の停止を混同しない、(2) **コミット
    ハッシュを報告する前に必ず `git log --oneline -1` / `git cat-file -t` で
    実在を確認する。**

## 4-E1 合否判定の liveness floor と減点の実効化

4-A 持ち越し2(エラー上限が絶対件数であるため「遅い全滅」を見逃す)への対処。
候補案 (a) を採り、**採点対象の各エンドポイントが最低 `max(1, 走行秒数/10)` 回は
成功していること**を合否条件に加えた。あわせて 4-B 持ち越し17(スコアが約3倍に
なって `errorPenalty = 1` が相対的に弱くなった)に対し `errorPenalty` を 20 へ
引き上げた。実装は `bench/liveness.go`(新規)・`bench/score.go`・`bench/main.go`。
コミット `1582910`(純関数の切り出しと `errorPenalty`)、`7ec58c1`(合否判定への
組み込みと内訳出力の単一定義化)。

### 設計判断

- **floor を走行時間に比例させた(`max(1, 秒数/10)`)理由。** 固定値にすると
  `-duration` を短くしたデバッグ走行で正しいアプリを落としてしまう。60秒走行なら
  floor は6回。採点対象で最小になるのは常に `POST /auctions` で、`small` の
  60秒走行の実測は本ドキュメントに記録があるものだけで
  **484 / 485 / 487回(4-B)、496回(本節ゲート1)** ——
  floor 6 はその 1/80 前後であり、正しい実装を誤って落とす余地はほぼ無い。
  一方で「0回」だけでなく「ほぼ死んでいる」状態も捕まえられる。
  10秒走行では floor 1 に落ちる(下限は1)。
  (設計文書 §floor の根拠表は最小値を 493回としているが、この数字と表の他の行は
  本ドキュメントのどの走行ログとも一致しない —— 下記の持ち越し24 を参照。
  floor 6 という結論は上記の実測レンジからも同じく導かれるので、値の妥当性には
  影響しない。)
- **ワーカー数で条件付けした(`livenessRequired`)理由。** `-sellers 0` のように
  ワーカーを止めたデバッグ走行では、そのワーカーしか叩かないエンドポイントが
  構造的に0回になる。これを FAIL にすると floor がデバッグ走行を壊す。対応表は
  `bench/load.go` の各 `*Iteration` が実際に呼ぶエンドポイントから導いており、
  新しい採点タグを足し忘れると黙って判定をすり抜けるので
  `TestLivenessRequiredCoversAllTags` が固定している。
  **なお、この根拠が実際に成立するようになったのは 4-E1 でワーカー数0のガードを
  入れてからである。** それ以前は `-sellers 0` がワーカーを止めず無制限並列で
  走らせていたため(持ち越し23)、「ワーカーを止めたデバッグ走行」という状況を
  どのフラグ値でも作れず、条件付けは committed テスト以外に到達手段の無い防御だった。
  ガード(`bench/scenario.go` の `Load`)と条件付けは対で意味を持つ。
- **`errorPenalty` を定数20にした理由。** 「成功1リクエストあたりの平均得点」の
  約11倍に置き、エラー1件が成功約11リクエストぶんの損失になるようにした。
  上限の100件で2000点、4-B のスコア帯 raw 19086 に対して約10.5%。
  平均得点は 4-B ゲート3 の実測ログから
  `raw 19086 ÷ 成功10532回(= 内訳7本の合計)` = **約1.81点/回** と再計算できる
  (4-E1 以前は分母を 10331回・約1.85点としていたが、これは2本の走行を混ぜた
  ハイブリッドだった —— 下記の持ち越し24。`bench/score.go` のコメントは
  本フェーズで上記の再計算値に訂正済み。20 という値の妥当性には影響しない差である)。
  **いずれにせよこれは採用スケールに依存する絶対値であり、割合ベースではない**
  (持ち越し22)。

### ゲート測定(2026-08-30、本ブランチ HEAD `7ec58c1`)

以下のブロックはいずれもツールの実出力の転記である。

**実行順(nginx のアクセスログのタイムスタンプで確認した実際の順序):**

| 時刻 | 走行 |
|---|---|
| 18:20:05 | ゲート1 |
| 18:21:42 | ゲート2 初回(無条件 `Sleep`。Prepare が死んで空振り) |
| 18:22:58〜18:24:40 | ゲート5 ×7(ゲート2 の改変を設計し直すための計数を兼ねる) |
| 18:25:40 | ゲート2 本測定 |
| 18:27:08 | **ゲート3 素の手順(`-sellers 0`)→ 持ち越し23 で全滅。2分45秒** |
| — | `docker compose restart app nginx` |
| 18:31:50 | ゲート4 |
| 18:32:26 | ゲート3(一時パッチ版) |
| 翌日 | **ゲート3 再測定(持ち越し23 を修正後、素の手順)** |

ゲート2 は一時的な改変を投入したため、直前後に `git checkout` + 再ビルドで復元し
`git status --short` が空であることを確認している。

> **走行環境の汚染について(スコアを比較に使う人向け)。**
> **ゲート4 とパッチ版ゲート3 は、上表のとおり「ゲート3 の全滅走行」の後に実行している。**
> 全滅走行はマシンの接続と CPU を枯渇させたので、間に `docker compose restart app nginx`
> を挟んだ。復帰後の走行はエラー0件でスコアもゲート1 と同水準に戻っており、
> 影響は残っていないと判断している。ただし
> **ゲート1(19636)/ ゲート3パッチ版(20492)/ ゲート3再測定(19249)の差を
> 性能比較に使ってはならない。** 全滅走行と再起動を挟んでいることに加え、
> `-sellers 0` は出品が起きないぶん一覧の負荷特性そのものが変わるため、
> もともとゲート1 との絶対値比較には向かない。
> **4-E でスコア帯の出発点にしてよいのは、通常のワーカー構成で汚染を挟まずに
> 測ったゲート1 の 19636 だけである**(4-B の 18439〜19429 と同水準)。

**ゲート1(正常な60秒走行)**

```
SCORE: 19636  (raw 19636, penalty 0)
  GET /auctions            : 2163回 (2163点)
  GET /auctions (検索)       : 1583回 (3166点)
  GET /auctions/:id        : 3726回 (3726点)
  POST /auctions/:id/bids  : 1175回 (5875点)
  GET /auctions/:id/bids   : 1174回 (1174点)
  GET /notifications       : 526回 (1052点)
  POST /auctions           : 496回 (2480点)
ERRORS: 0件 (critical: 0件)
LIVENESS: PASS (floor 6回、採点7本すべて到達)
RESULT: PASS
```

stderr は0行(`ERR:` 行なし)。内訳の検算:
2163×1 + 1583×2 + 3726×1 + 1175×5 + 1174×1 + 526×2 + 496×5
= 2163 + 3166 + 3726 + 5875 + 1174 + 1052 + 2480 = **19636 = raw**。
penalty 0 なので SCORE 19636 = raw。一致。→ **PASS**。

**ゲート2(遅い全滅を検出できること)← このフェーズの存在理由**

4-A で `full` が `RESULT: PASS` を通した欠陥そのものを再現する。**クライアントの
タイムアウト(10秒)を超えてブロックする**改変であることが本質で、即座に 500 を
返す「速い失敗」ではエラー件数が上限を超えて既存の判定でも落ちてしまい、この欠陥の
再現にならない。

*計画からの逸脱(1件)。* 当初の指定は `getAuctions` の先頭に無条件の
`time.Sleep(15 * time.Second)` を1行入れる、というものだった。これを実際に投入すると
**Prepare 自身が `GET /auctions?page=1` で10秒タイムアウトし、Load へ到達しない**:

```
SCORE: 0  (raw 0, penalty 20)
  (内訳7行は採点7本すべて 0回 0点。ここでは省略)
ERRORS: 1件 (critical: 0件)
LIVENESS: FAIL (floor 6回)
  (dead 7行は採点7本すべて 0回。ここでは省略)
RESULT: FAIL
ERR: prepare: timeout: Get "http://localhost:8080/auctions?page=1": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
```

この走行は `total > 0` が偽なので**旧ルールでも FAIL しており、4-A の欠陥の再現に
なっていない**(4-A では Prepare は通っていた。単一リクエストの Prepare は速く、
遅くなるのは並列負荷がかかる Load だけ、というのが当時の実態である)。そこで
**Load 区間だけを遅くする**よう改変を調整した。`-prepare-only` を3回走らせて
nginx のアクセスログを数え、Prepare が発行する一覧リクエストが毎回きっかり
**15回**(`GET /auctions` 系。`POST /initialize` は Prepare の先頭と末尾で2回)で
あることを確認したうえで、先頭15回だけ素通しするゲートを付けた:

```go
var degradeCalls int64

const degradePassthrough = 15

func (h *handler) getAuctions(w http.ResponseWriter, r *http.Request) {
	if atomic.AddInt64(&degradeCalls, 1) > degradePassthrough {
		time.Sleep(15 * time.Second) // 改悪: クライアントタイムアウト(10秒)を超えてブロックする
	}
	q, err := parseAuctionListQuery(r.URL.Query())
```

閾値がずれたときの壊れ方は安全側である。Prepare が16回出せば16回目がタイムアウトして
Prepare が落ち、即座に気づく。14回しか出さなければ Load の1回だけが速く通るが、
floor 6 には届かないので判定は変わらない。

**このゲートを再実行する人へ。** 15 は Prepare の実装と
`initial-data/out/snapshot.json` のページ数に依存するので、**必ず数え直すこと**。

```bash
CF=<リポジトリ>/dev/compose.yaml   # 絶対パスで渡すこと(下記)
M=$(docker compose -f $CF logs --no-log-prefix nginx | wc -l | tr -d ' ')
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json -prepare-only
docker compose -f $CF logs --no-log-prefix nginx | tail -n +$((M+1)) \
  | grep -cE '"GET /auctions(\?[^ ]*)? HTTP'
```

嵌まりどころが3つある。

- **`GET /auctions/:id`(詳細)を数に入れないこと。** 上の正規表現は `"GET /auctions`
  の直後がクエリ文字列か空白のものだけに一致させ、詳細を除外している。今回の Prepare は
  詳細を45件引いているので、雑に `grep -c 'GET /auctions'` とすると60になってしまう。
- **`docker compose` の `-f` は絶対パスで渡すこと。** `cd bench` した後に相対パスの
  `-f dev/compose.yaml` を使うとファイルが見つからず、エラーが握り潰されて計数が
  黙って0になる(実際に一度これで嵌まった)。
- **改変には `sync/atomic` の import 追加が要る。** `webapp/go/auctions.go` は `time` を
  既に import しているが `sync/atomic` はしていない。

この改変での60秒走行の実出力:

```
SCORE: 3178  (raw 4378, penalty 1200)
  GET /auctions            : 0回 (0点)
  GET /auctions (検索)       : 0回 (0点)
  GET /auctions/:id        : 0回 (0点)
  POST /auctions/:id/bids  : 0回 (0点)
  GET /auctions/:id/bids   : 0回 (0点)
  GET /notifications       : 634回 (1268点)
  POST /auctions           : 622回 (3110点)
ERRORS: 60件 (critical: 0件)
LIVENESS: FAIL (floor 6回)
  GET /auctions            : 0回
  GET /auctions (検索)       : 0回
  GET /auctions/:id        : 0回
  POST /auctions/:id/bids  : 0回
  GET /auctions/:id/bids   : 0回
RESULT: FAIL
```

stderr の `ERR:` 行は60行、すべて `load: timeout: application: timeout: Get
"http://localhost:8080/auctions?..." : context deadline exceeded (Client.Timeout
exceeded while awaiting headers)`(内訳: `?page=N` 49件、`?category=N&page=N` 6件、
`?page=N&q=ワークス` 5件)。critical は0件。

名指しされたのは想定どおり一覧経路の**5本**で、`GET /notifications` と
`POST /auctions` は生き残っている(`notifierIteration` / `sellerIteration` は
`GetAuctions` を呼ばないため。bidder は一覧を引けないと詳細以降へ進めない)。

内訳の検算: 634×2 + 622×5 = 1268 + 3110 = **4378 = raw**。
penalty = 60件 × `errorPenalty` 20 = **1200**。4378 − 1200 = **3178 = SCORE**。一致。

**この走行が liveness 判定なしなら PASS していたことの証拠:**

| 旧ルールの項 | 実測値 | 成否 |
|---|---|---|
| `criticalCount == 0` | `critical: 0件` | 真 |
| `appCount <= errorLimit`(上限100) | アプリエラー 60件(= 60 − critical 0) | 真(**上限に達していない**) |
| `total > 0` | `SCORE: 3178` | 真 |

3項すべてが真なので、旧ルール
`pass := criticalCount == 0 && appCount <= errorLimit && total > 0` は真になっていた。
**これが 4-A で `full` が `RESULT: PASS` を出力した機構そのものである。**
liveness floor によって `len(dead) == 0` が偽になり、いま `RESULT: FAIL` になる。

**この結論は `errorPenalty` の改定に依存しない。** 旧来の `errorPenalty = 1` で
計算しても penalty は 60 × 1 = 60 にしかならず、total = 4378 − 60 = **4318 > 0** で
やはり3項すべてが真になる。つまり「liveness floor が無ければ PASS していた」のは
floor そのものの効果であって、減点の引き上げによるものではない。

→ **PASS(欠陥を検出できた)**。

確認後 `git checkout webapp/go/auctions.go` + 再ビルドで復元し、`git status --short`
が空、`意図的に遅い実装` コメント7箇所が無傷であることを確認した。

**ゲート3(ワーカーを止めても誤検知しないこと)**

```
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json -sellers 0
```

実出力(**持ち越し23 のガードを入れたあと、素の手順で再測定したもの**):

```
SCORE: 19249  (raw 19249, penalty 0)
  GET /auctions            : 2336回 (2336点)
  GET /auctions (検索)       : 2319回 (4638点)
  GET /auctions/:id        : 3991回 (3991点)
  POST /auctions/:id/bids  : 1215回 (6075点)
  GET /auctions/:id/bids   : 1215回 (1215点)
  GET /notifications       : 497回 (994点)
  POST /auctions           : 0回 (0点)
ERRORS: 0件 (critical: 0件)
LIVENESS: PASS (floor 6回、採点7本すべて到達)
RESULT: PASS
```

exit code 0、stderr は0行、所要 1:06.93。
**`POST /auctions` が 0回でも `LIVENESS: PASS` / `RESULT: PASS`、critical 0件。**
`livenessRequired` の条件付けは意図どおり働いている。
内訳の検算: 2336×1 + 2319×2 + 3991×1 + 1215×5 + 1215×1 + 497×2 + 0×5
= 2336 + 4638 + 3991 + 6075 + 1215 + 994 + 0 = **19249 = raw**。penalty 0。一致。

ワーカーが実際に止まっていることは nginx のアクセスログでも裏を取った。
この走行中の総リクエスト13524件のうち、**出品(`"POST /auctions HTTP"`)は0件**:

```
$ docker compose -f dev/compose.yaml logs --no-log-prefix nginx | tail -n +<走行前の行数+1> \
    | grep -cE '"POST /auctions HTTP'
0
```

→ **PASS**。

**このゲートを最初に実行したときは、ベンチ本体の別の欠陥で全滅していた**(持ち越し23)。
記録として残す。ガードを入れる前の `-sellers 0` は出品ワーカーを止めるどころか
**無制限並列で走らせて**おり、走行は 2:45 かかって以下で終わっていた:

```
SCORE: 0  (raw 0, penalty 5387360)
  (内訳7行は採点7本すべて 0回 0点。ここでは省略)
ERRORS: 269368件 (critical: 0件)
LIVENESS: FAIL (floor 6回)
  (dead 6行 = POST /auctions を除く6本、すべて 0回。ここでは省略)
RESULT: FAIL
```

`ERR:` で始まる行は 269368行(`ERRORS:` の件数と一致。stderr ファイル全体は
289522行で、差分は nginx の 504 HTML 本文が複数行にまたがるぶん)。内訳の上位は
`Post ".../login"` の `context deadline exceeded` 191195件 /
`dial tcp: connect: resource temporarily unavailable` 27815件 /
`connect: operation timed out` 22102件 / `EOF` 11585件 で、
**マシン側の接続・CPU が枯渇したことによる全滅**であり liveness 判定の問題ではない
(`-sellers 0` にしたのに `POST "http://localhost:8080/auctions"` を含むエラーが
234件出ていることが、出品ワーカーが動いていた直接の証拠)。
penalty も 269368 × `errorPenalty` 20 = 5387360 で報告値と一致する。
なお `POST /auctions` が dead に挙がっていないことから、この壊れた走行でも
`livenessRequired` の条件付け自体は設計どおり効いている。

この全滅走行が持ち越し23 の発見につながり、**4-E1 でガード
(`bench/scenario.go` の `Load` で「ワーカー数が0以下なら `Process` を呼ばない」)を
入れて対応した。** 上の PASS はガード適用後の素の手順による実測である。
条件付けロジック自体は `TestLivenessRequiredRespectsWorkerCounts`
(`bench/liveness_test.go`)が committed テストとして固定している。

なお、この全滅走行と上の再測定の間には
`-sellers 0` の一時パッチ版の走行(SCORE 20492、`POST /auctions` 0回で
`LIVENESS: PASS` / `RESULT: PASS`)が1本ある。ガード適用後の再測定と同じ結論
だったので、記録としては再測定のほうを正とする。

**ゲート4(短い走行でも壊れないこと、`-duration 10s`)**

```
SCORE: 3547  (raw 3547, penalty 0)
  GET /auctions            : 379回 (379点)
  GET /auctions (検索)       : 302回 (604点)
  GET /auctions/:id        : 650回 (650点)
  POST /auctions/:id/bids  : 219回 (1095点)
  GET /auctions/:id/bids   : 219回 (219点)
  GET /notifications       : 90回 (180点)
  POST /auctions           : 84回 (420点)
ERRORS: 0件 (critical: 0件)
LIVENESS: PASS (floor 1回、採点7本すべて到達)
RESULT: PASS
```

stderr は0行。`LIVENESS: PASS (floor 1回、…)` と表示され `RESULT: PASS`。
**floor 1 すら満たせないエンドポイントは無かった**(最小は `POST /auctions` の84回で
floor の84倍)。内訳の検算: 379×1 + 302×2 + 650×1 + 219×5 + 219×1 + 90×2 + 84×5
= 379 + 604 + 650 + 1095 + 219 + 180 + 420 = **3547 = raw**。一致。→ **PASS**。

**ゲート5(Prepare の非回帰、`-prepare-only`)**

ゲート2 の改変を戻したあと、正常なアプリに対して計7回実行した(ゲート2 の
Prepare リクエスト数を数えるために繰り返したもので、そのまま非回帰の測定になっている)。
**7回とも標準出力は `PREPARE: PASS` の1行のみ、stderr は0行、そして
`LIVENESS:` 行は出ていない**(`*prepareOnly` の分岐が liveness 判定より手前で
`return` するため)。所要時間(`go run` の起動込み、ビルドキャッシュは温かい状態)は
**1.722s / 1.730s / 1.734s / 1.735s / 1.759s / 1.768s / 1.817s** で、
レンジは **1.722〜1.817秒**。

6秒基準内。→ **PASS**。(4-B のゲート2実測 1.7〜1.8秒と同水準で、非回帰。)

### ゲート3の手順(4-E1 で差し替え)

**ここでいう「ゲート3」は 4-A・4-B の意味でのゲート3、すなわち
「60秒の通常走行」のことである。** 本節で G1〜G5 と並べて測った
「ゲート3(ワーカーを止めても誤検知しないこと)」とは別物なので注意すること
(4-E1 のゲート番号は本フェーズ限りのもので、フェーズ横断のゲート手順とは無関係)。

**以降のフェーズは次の手順を使う。**

> **ゲート3(60秒走行、通常のwebapp)**
> 1. `cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json`
> 2. **`LIVENESS: PASS` 行が出ていることを確認する。** 出ていなければ、続く行が
>    floor を下回った採点対象を名指ししている。
> 3. `RESULT: PASS` と `critical: 0件` を確認する。
> 4. スコア内訳を検算する(各行 `count × weight == points`、全行の合計 `== raw`、
>    `SCORE == raw − penalty`)。

4-A・4-B のゲート手順にあった「採点対象すべてが0回でないことを**目視で明示的に
確認する**」は、手順2 に置き換わる。目視の脱落が 4-A の見逃しを生んだので、
**判定はベンチ側に持たせ、人間は出力行を読むだけにする**のが差し替えの趣旨である。
手順4(内訳の検算)は引き続き人間が行う —— こちらは自動化されていない。

### まとめと持ち越し(4-Eへ)

**ゲート1〜5 はすべて PASS**(ゲート3 は、測定中に見つかったベンチ本体の欠陥
(持ち越し23)を本フェーズで直したうえで、素の手順で再測定して PASS)。

本フェーズで対応済みにした持ち越しは4件。いずれも該当箇所にインラインで追記した。

| 持ち越し | 由来 | 対応 |
|---|---|---|
| 2(エラー上限が絶対件数で「遅い全滅」を見逃す) | 4-A | liveness floor を追加(候補案 (a))。候補案 (b) の割合化は持ち越し21 へ |
| 17(`errorPenalty = 1` が相対的に弱い) | 4-B | `errorPenalty` を 20 へ。絶対値であることは変わらず → 持ち越し22 |
| 23(ワーカー数0が無制限並列になる) | 4-E1 で発見 | `Load` にガードを追加 |
| 24(コメントの数字が複数走行のハイブリッド) | 4-E1 で発見 | 実測1本から再計算して訂正 |

- **持ち越し21: エラーのエンドポイント紐付けと、割合ベースのエラー上限。**
  4-A 持ち越し2 の候補案 (b) は未対応で残る。現在エラーは
  `result.Errors.All()` のフラットな配列で、**どのエンドポイントで起きたか分からない**。
  そのため「一部のエンドポイントだけが遅く壊れている」(たとえば6回成功・60回失敗)は、
  全体のエラー率では捕まえられない —— 試行が1万回あれば 0.6% にしかならず、
  どんな割合しきい値を置いても発火しない。**割合化を入れる前に、まず
  `addErr`(`bench/load.go`)の全呼び出し箇所へエンドポイントのタグを配り、
  エラーをエンドポイント別に集計できるようにする変更が要る。** その上でなら
  「エンドポイント単位のエラー率」という、liveness floor より細かい判定が書ける。
- **持ち越し22: `errorPenalty = 20` は採用スケールに依存する。** 20 という値は
  4-B `small` の「成功1リクエストあたり平均約1.8点」から逆算したもので、
  スコアのスケールが変われば相対的な重みも変わる。**4-E で採用スケールを
  再決定する際に再調整が要る。** 根本的には持ち越し21 と同じ軸(絶対値 vs 割合)の
  問題である。
- **持ち越し23: `-bidders 0` / `-watchers 0` / `-notifiers 0` / `-sellers 0` は
  ワーカーを止めず、無制限並列で走らせる**
  (**→ 4-E1 で対応済み**。`bench/scenario.go` の `Load` に「ワーカー数が0以下なら
  `Process` を呼ばない」ガードを入れ、機序をコメントに明記した。対応後に
  ゲート3 を素の手順(`-sellers 0`、パッチ無し)で再測定して
  `LIVENESS: PASS` / `RESULT: PASS`、nginx ログでも出品リクエスト0件を確認済み。
  `livenessRequired` の条件付けは、このガードがあって初めて到達可能な防御になる。
  以下は発見当時の記述をそのまま残す)。
  `bench/scenario.go` の `Load` は
  `worker.WithMaxParallelism(int32(s.Sellers))` を無条件に渡し、4本の worker すべてを
  `Process` する。isucandar 側の `parallel.isLimitKept` は
  `limit < 1 || count < (limit*2)` と書かれており、**`limit == 0` は「上限なし」と
  解釈される**(`NewParallel` も `limit > 0` のときしか `doner` チャネルを作らない)。
  結果、`-sellers 0` は出品ワーカーを無限ループ・無制限並列で走らせ、goroutine と
  接続を際限なく増やして走行全体を破壊する(上記ゲート3の実測: エラー269368件、
  2分45秒)。**デバッグ用途でワーカーを止める操作が、事実上マシンを落とす操作に
  なっている。** 対策は `Load` で「ワーカー数が0以下なら `Process` を呼ばない」と
  すること。負値も `limit < 1` に該当するので同じ扱いで弾く。
  (以上が発見当時の記述。本フェーズでこのとおり対応した。)
- **持ち越し24: 設計文書の実測表が、2本の走行を混ぜたハイブリッドになっていた**
  (**→ 4-E1 で対応済み**。`bench/score.go` の `errorPenalty` コメントを
  4-B ゲート3 の実測ログ1本から再計算した値に訂正し、`bench/liveness.go` の
  コメントにも出所を明記した。以下は機序の記録)。
  **数字は捏造ではなく、実在する2本の走行の数字を混ぜたものだった。**
  4-E1 設計文書 §floor の根拠表は
  `3250 / 2078 / 1723 / 1137 / 1137 / 513 / 493`(合計10331回)を「4-B の `small` 実測」
  としている。このうち `3250 / 1723 / 1137 / 513 / 493` は
  `docs/superpowers/plans/2026-08-30-isubid-phase4b-list-search.md` の Task 8
  レビュー実測表に実在する。**ところが転記の際、一覧の行だけ同表の 2125 ではなく
  別走行(4-B ゲート3)の 2078 に差し替わった。** その結果、合計10331回は
  どの単一走行とも一致しない数になった(Task 8 表どおりなら10378、
  4-B ゲート3 どおりなら10532)。
  - 混成された 10331 は、設計文書 → 実装計画 → `bench/score.go` のコメントへ
    そのまま転記されていた。分子の raw 19086 は 4-B ゲート3 の値なので、
    **分子と分母が別の走行から来ている**状態だった。
  - 訂正値: 4-B ゲート3 の実測ログ1本で閉じて
    `raw 19086 ÷ 成功10532回(内訳7本の合計)` = **約1.81点/回**(従来の記載は1.85)。
  - `bench/liveness.go` の 493 は Task 8 実測表由来の**実在する数字**なので値としては
    正しい。出所が書かれていなかっただけなので、参照先と他走行の実測
    (4-B ゲート3 で485、4-E1 ゲート1 で496)を併記した。
  - **floor 6 も `errorPenalty` 20 も、訂正後の数字から再計算しても同じ結論に落ちる。**
    値の妥当性には影響しなかった。
  - 教訓として残す: **複数の文書にまたがる実測表を転記するときは、1本の走行ログで
    閉じること。** 行ごとに出所の違う表は、合計を取った瞬間にどこにも存在しない
    数字になる。しかも各行は実在するので、行単位の照合では発見できない
    (今回も Task 1 のレビューは「コメント中の数値を設計文書と照合して捏造でないことを
    確認」しており、照合先のほうが混成である可能性は見ていなかった)。
- **持ち越し25: floor は「ほぼ死んでいる」の閾値として保守的すぎる可能性がある。**
  60秒走行の floor は6回、対する実測の最小値は `POST /auctions` の 484〜496回。
  **桁違いに安全側**であり、正しい実装を誤って落とす心配はまず無い。裏を返すと、
  **「60秒で10回しか成功しない」程度の degradation は floor を通り抜ける。**
  liveness floor が捕まえるのは「全滅・ほぼ全滅」であって「大幅な劣化」ではない、
  という守備範囲の線引きを意識しておくこと。劣化の検出まで踏み込むなら、
  floor を上げるのではなく持ち越し21(エンドポイント単位のエラー率)や
  レイテンシ基準など別の軸を足すほうが素直である —— floor を実測の最小値へ
  近づけると、スケールやワーカー数を変えたときに正しい実装を落としやすくなる。
- **軽微な持ち越し(4-E で手が入る際についでに直すもの)**:
  - `LIVENESS: PASS` のメッセージが `採点7本すべて到達` と `len(scoredTags)` を
    そのまま出すため、`livenessRequired == false` で判定対象外になったタグがあっても
    「7本すべて」と表示される。上記ゲート3の実測がまさにその状態
    (`POST /auctions` は0回なのに「採点7本すべて到達」)。表示のみの問題だが
    読み手を誤解させる。判定対象の本数を数えて出すのが素直。
  - `LIVENESS: FAIL` 時に表示されるのは floor を下回ったタグのみで、
    `livenessRequired == false` で判定対象外にしたタグは出力に現れない。
    デバッグ時に「なぜ対象外なのか」が分からない。実害なし。
  - `TestLivenessRequiredRespectsWorkerCounts` の `noWatchers` ケースに
    `ScoreGETDetail` の対称テストが無い(`ScoreGETList` と `ScoreGETDetail` が同じ
    `switch` の case なので実害は小さいが、将来この2つを分離したときに検出力が落ちる)。
  - `TestLivenessFloor` に f=2 相当(20秒)のケースが無く、`f > 1` の比較演算子の
    境界が60秒/120秒のケースで間接的にしか検証されていない。
- **プロセス上の教訓(4-E1)**:
  - **「改悪を1行入れる」型のゲートは、Prepare を通過することを設計時に確かめること。**
    ゲート2 の当初指定(無条件 `time.Sleep`)は Prepare 自身を殺してしまい、
    `total > 0` が偽になる別の経路で FAIL していた。**FAIL したこと自体は同じでも、
    再現しようとしていた欠陥は再現できていなかった** —— 「FAIL した」で満足すると
    ゲートが空振りしていることに気づけない。旧ルールの3項を1つずつ突き合わせる
    手順(上記の表)を踏んだことで発覚した。
