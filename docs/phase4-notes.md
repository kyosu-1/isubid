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
- **持ち越し2: `full` スケールでの `GET /auctions` 全滅**。`FOR UPDATE` の有無に
  関係なく、`full`(auctions 10,300件)では60秒走行中 `GET /auctions` が
  一度も成功しなかった(60件のタイムアウト)。意図的な低速実装のままでは
  `full` は現実的な負荷走行の土俵に乗らない。4-Eで負荷調整(bidders/watchers数、
  タイムアウト値、あるいは意図的な遅さの度合い自体)を検討する材料とする。
- **持ち越し3: `full` スケールでのPrepare所要時間**。11.47〜12.31sで6秒基準
  (seed auction 4 の+12秒窓由来)を大幅に超過。`full` を将来採用する場合は
  Prepareの高速化、またはseedスケジュールの調整が必要。
- **持ち越し4: 生成データによる一覧のN+1悪化**。`medium`/`full` のゲート3測定でも
  `GET /auctions`(一覧)のスコア配分が small より明確に下がっており(small 496回
  →medium 112回→full 0回)、生成データを足すほど一覧のN+1コストが支配的になる
  傾向が確認できた。4-Bで一覧まわりのチューニング設計を行う際の実測的な裏付けとする。
- **持ち越し5: 生成物サイズ**。今回コミットするのは `small` のみ(合計約2.5MB)。
  `medium`/`full` の生成物(`medium` 約10MB、`full` 約25MB)はいずれもリポジトリに
  コミットしていない(採用スケールではないため)。将来 `full` を採用する場合は
  50MB前後に収まる見込みだが、その時点で再度サイズを確認すること。
