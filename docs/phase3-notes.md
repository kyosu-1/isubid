# Phase 3 実装ノート

## 3-1 時間軸 + 終了処理 + 落札Validation

### 設計判断

- **落札照合はスナップショット内部の不変条件として検査する**: 「winner = argmax(bids)」を
  詳細レスポンス1件の中で閉じて検証する。入札APIと終了処理が同じオークション行の
  FOR UPDATE で直列化されるため中間状態が存在せず、pending の許容が要らない。
  台帳との突合(bids 自体が本物か)は既存の reconcileAuction が担う
- **「ends_at を過ぎたら closed」を独立した検証項目にした**: これが無いと、終了処理バッチを
  止めても closed が1件も無いだけで落札照合は素通りしてしまい、バッチ停止を検出できない

### ベンチのベンチ実測(2026-08-29)

| 壊し方 | 結果 |
|---|---|
| `runAuctionCloser` の起動をコメントアウト | `RESULT: FAIL` / critical 5件 / `ends_at (...) を過ぎているのに status が "live" (期待: closed)` |
| 落札者選択を `ORDER BY amount ASC, id ASC` に変更 | `RESULT: FAIL` / critical 5件 / `winner_id が ... (期待: ... = 最高額 ... の入札者)` |
| 復元後 | `RESULT: PASS` / critical 0件 / `SCORE: 29892 (raw 29892, penalty 0)` |

### 実測ログ(抜粋)

**Step 1: `webapp/go/main.go` の `go h.runAuctionCloser(context.Background())` をコメントアウト**

```
ERR: validation: critical: auction 2: ends_at (2026-08-29T09:39:22Z) を過ぎているのに status が "live" (期待: closed)
ERR: validation: critical: auction 4: ends_at (2026-08-29T09:39:14Z) を過ぎているのに status が "live" (期待: closed)
ERR: validation: critical: auction 8: ends_at (2026-08-29T09:39:30Z) を過ぎているのに status が "live" (期待: closed)
ERR: validation: critical: auction 10: ends_at (2026-08-29T09:39:46Z) を過ぎているのに status が "live" (期待: closed)
ERR: validation: critical: auction 6: ends_at (2026-08-29T09:39:38Z) を過ぎているのに status が "live" (期待: closed)
SCORE: 32416  (raw 32421, penalty 5)
  GET /auctions            : 11602回 (11602点)
  GET /auctions/:id        : 11644回 (11644点)
  POST /auctions/:id/bids  : 1835回 (9175点)
ERRORS: 5件 (critical: 5件)
RESULT: FAIL
```

**Step 2: `webapp/go/closer.go` の落札クエリを `ORDER BY amount DESC, id ASC` → `ORDER BY amount ASC, id ASC` に変更**

```
ERR: validation: critical: auction 4: winner_id が 7 (期待: 6 = 最高額 14445 の入札者)
ERR: validation: critical: auction 6: winner_id が 20 (期待: 19 = 最高額 42269 の入札者)
ERR: validation: critical: auction 8: winner_id が 7 (期待: 18 = 最高額 27722 の入札者)
ERR: validation: critical: auction 10: winner_id が 11 (期待: 19 = 最高額 55908 の入札者)
ERR: validation: critical: auction 2: winner_id が 5 (期待: 19 = 最高額 26019 の入札者)
SCORE: 30266  (raw 30271, penalty 5)
  GET /auctions            : 10619回 (10619点)
  GET /auctions/:id        : 10702回 (10702点)
  POST /auctions/:id/bids  : 1790回 (8950点)
ERRORS: 5件 (critical: 5件)
RESULT: FAIL
```

**Step 3: `git checkout webapp/go/main.go webapp/go/closer.go` で復元して再走行**

```
SCORE: 29892  (raw 29892, penalty 0)
  GET /auctions            : 10499回 (10499点)
  GET /auctions/:id        : 10593回 (10593点)
  POST /auctions/:id/bids  : 1760回 (8800点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

いずれも `docker compose -f dev/compose.yaml up -d --build` で再ビルドしたうえで
`cd bench && go run . -target http://localhost:8080 -duration 60s` を実行(各回とも1回目の実行でブリーフ記載の期待クリティカルが検出された)。破壊は毎回 `git checkout` で復元し、
最終的に `git status --short` / `git diff HEAD -- webapp/ bench/` が空であることを確認済み。

## 3-2 入札フィード

### 設計判断

- **カーソルは `since=<bid_id>`**: 入札APIがオークション行を FOR UPDATE で保持したまま
  INSERT するため、同一オークション内では id 順 = コミット順が保証される。timestamp
  カーソルだと同一マイクロ秒の複数入札で重複か欠落が起き、反映検証が確率的になる
- **反映待ちの打ち切りは違反にしない**: Load 終了に伴う ctx キャンセルで
  ポーリングを打ち切った場合、`ctx.Err() != nil` を見て critical を上げない

### ベンチのベンチ実測(2026-08-29)

| 壊し方 | 結果 |
|---|---|
| フィードが3秒より新しい入札を隠す(`created_at <= DATE_SUB(NOW(6), INTERVAL 3 SECOND)` を追加、staleness再現) | `RESULT: FAIL` / critical 104件 / `2s 以内にフィードへ反映されない` |
| フィードを `ORDER BY id DESC` に変更 | `RESULT: FAIL` / critical 105件 / `フィードが id 昇順でない` |
| 復元後 | `RESULT: PASS` / critical 0件 / `SCORE: 32428 (raw 32428, penalty 0)` |

### 検証の境界

`awaitFeedReflection` の2秒デッドラインが検出するのは**フィードの鮮度(staleness)**であり、
**応答そのものの遅さ(スループット)ではない**。これは意図的な設計であり、以下の実測が
その根拠になる。

- **検出する対象**: フィードが古いスナップショットしか返さず、コミット済みの入札がしばらく
  経っても現れないケース。上表1行目(`created_at` フィルタで staleness を再現)がこれに当たり、
  期待どおり `入札 id=... が 2s 以内にフィードへ反映されない` で `RESULT: FAIL` になる
- **検出しない対象**: フィードの応答自体が遅いだけで、返ってきた内容は最新であるケース
  (`getAuctionBids` 冒頭に `time.Sleep(3 * time.Second)` を挿入する実験で確認した。実測は
  下記実測ログの「参考: レイテンシ実験」を参照)。`POST /auctions/:id/bids` が201を返した
  時点で入札は既にコミット済みなので、遅れて届いた最初のフィード応答にも対象の bid は
  既に含まれており、`awaitFeedReflection` は1回目のポーリングで成功と判定する。これは
  critical にはならない代わりに、応答が遅い分だけ1回のリクエストに時間を取られ、走行中に
  こなせる `POST /auctions/:id/bids` の回数が激減してスコアで直接 punish される: 復元後の
  1771回(このタスクのStep3実測)に対し、3秒遅延を入れた場合は80回(`-duration 30s` で
  2回実行し、いずれも80回で再現)と、約95%の減少だった
- **なぜこれでよいか**: reference実装は意図的に遅い(bidsに `auction_id` のインデックスが
  無くフルスキャンになり、さらに入札ごとのN+1がある)。もし「bid が見つかった場合でも
  経過時間が2秒を超えていたら critical」という判定に変えると、負荷が高い状況では
  reference実装自身がこの2秒を超えて critical を出しうる。ベンチマークが reference実装
  自身を落とすのは、このプロジェクトが一貫して避けてきた false-FAIL そのものであり、
  それを避けるために「遅いだけ」は critical にせずスコア収益の減少だけで punish する
  設計にしてある

### 実測ログ(抜粋)

**Step 1: `webapp/go/feed.go` のフィードクエリに `created_at <= DATE_SUB(NOW(6), INTERVAL 3 SECOND)` を追加(3秒より新しい入札をフィードから隠す=staleness再現、`-duration 30s`)**

```
ERR: load: critical: auction 4: 入札 id=9 が 2s 以内にフィードへ反映されない
ERR: load: critical: auction 2: 入札 id=12 が 2s 以内にフィードへ反映されない
ERR: load: critical: auction 4: 入札 id=11 が 2s 以内にフィードへ反映されない
ERR: load: critical: auction 9: 入札 id=10 が 2s 以内にフィードへ反映されない
ERR: load: critical: auction 7: 入札 id=13 が 2s 以内にフィードへ反映されない
(以下 critical 99件省略、計104件、いずれも同じ「入札 id=... が 2s 以内にフィードへ反映されない」パターン)
SCORE: 33041  (raw 33145, penalty 104)
  GET /auctions            : 15189回 (15189点)
  GET /auctions/:id        : 15194回 (15194点)
  POST /auctions/:id/bids  : 112回 (560点)
  GET /auctions/:id/bids   : 2202回 (2202点)
ERRORS: 104件 (critical: 104件)
RESULT: FAIL
```

**Step 2: `webapp/go/feed.go` のフィードクエリを `ORDER BY id ASC` → `ORDER BY id DESC` に変更**

```
ERR: load: critical: auction 3: フィードが id 昇順でない (index 1: id=18 の前が id=21)
ERR: load: critical: auction 6: フィードが id 昇順でない (index 1: id=71 の前が id=72)
ERR: load: critical: auction 8: フィードが id 昇順でない (index 1: id=108 の前が id=109)
ERR: load: critical: auction 7: フィードが id 昇順でない (index 1: id=214 の前が id=215)
ERR: load: critical: auction 8: フィードが id 昇順でない (index 1: id=282 の前が id=283)
(以下 critical 100件省略、計105件)
SCORE: 31294  (raw 31399, penalty 105)
  GET /auctions            : 10362回 (10362点)
  GET /auctions/:id        : 10453回 (10453点)
  POST /auctions/:id/bids  : 1764回 (8820点)
  GET /auctions/:id/bids   : 1764回 (1764点)
ERRORS: 105件 (critical: 105件)
RESULT: FAIL
```

**Step 3: `git checkout webapp/go/feed.go` で復元して再走行**

```
SCORE: 32428  (raw 32428, penalty 0)
  GET /auctions            : 10858回 (10858点)
  GET /auctions/:id        : 10944回 (10944点)
  POST /auctions/:id/bids  : 1771回 (8855点)
  GET /auctions/:id/bids   : 1771回 (1771点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

**参考: レイテンシ実験(`getAuctionBids` 冒頭に `time.Sleep(3 * time.Second)` を挿入、`-duration 30s` で2回実行)**

`検証の境界` で述べた「検出しない対象」の根拠として実測したログ。当初の壊し方案はこちら
だったが、これは staleness ではなくスループット低下であり、`awaitFeedReflection` の
2秒デッドラインが本来検出すべき対象ではないと判明したため、Step 1 は上記の staleness版に
差し替えた。参考データとしてここに残す。

1回目:
```
SCORE: 32856  (raw 32856, penalty 0)
  GET /auctions            : 16191回 (16191点)
  GET /auctions/:id        : 16193回 (16193点)
  POST /auctions/:id/bids  : 80回 (400点)
  GET /auctions/:id/bids   : 72回 (72点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

2回目(再現性確認):
```
SCORE: 29364  (raw 29364, penalty 0)
  GET /auctions            : 14443回 (14443点)
  GET /auctions/:id        : 14449回 (14449点)
  POST /auctions/:id/bids  : 80回 (400点)
  GET /auctions/:id/bids   : 72回 (72点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

Step 1(staleness版)・2 とも `docker compose -f dev/compose.yaml up -d --build` で再ビルドした
うえで走行(各回とも1回目の実行で期待どおりのクリティカルが検出された)。破壊は毎回
`git checkout webapp/go/feed.go` で復元し、最終的に `git status --short` /
`git diff HEAD -- webapp/ bench/` が空であることを確認済み。

## 3-3 通知

### 設計判断

- **outbid の期待値は「抜かれた回数」ではない**: ファンアウトは入札済み全員宛なので、
  期待件数は「自分が入札した各オークションで、自分の最初の入札より後に受理された
  他ユーザーの入札の総数」。両者は一致しないため、混同すると検証がずれる
- **下限比較にとどめる**: pending 入札を数えず、かつベンチはシードユーザーとして
  ログインするためシード入札ぶんの通知が上乗せされうる。「受信数 < 下限なら違反」とする
- **「自分宛のみ」は新規ユーザーの0件で検出する**: レスポンスに user_id を含めずに
  他人宛の混入を検出するため、一度も入札していない新規ユーザーの通知が0件であることを
  Validation で確認する

### ベンチのベンチ実測(2026-08-29)

| 壊し方 | 結果 |
|---|---|
| outbid ファンアウトを削除 | `RESULT: FAIL` / critical 20件 / `outbid通知が 0件 (期待: 1631件以上、欠落の疑い)` |
| won 通知を削除 | `RESULT: FAIL` / critical 5件 / `auction 4 を落札したのに won通知が無い` |
| 通知一覧から `WHERE user_id = ?` を削除 | `RESULT: FAIL` / critical 1件 / `入札していない新規ユーザーに通知が 30868件 (期待: 0件、他人宛の混入)` |
| 復元後 | `RESULT: PASS` / critical 0件 / `SCORE: 40247` |

### 実測ログ(抜粋)

**Step 1: `webapp/go/bids.go` のファンアウトのループ本体(`INSERT INTO notifications`)をコメントアウト**

```
ERR: validation: critical: user 12: outbid通知が 0件 (期待: 1631件以上、欠落の疑い)
ERR: validation: critical: user 13: outbid通知が 0件 (期待: 1554件以上、欠落の疑い)
ERR: validation: critical: user 9: outbid通知が 0件 (期待: 1567件以上、欠落の疑い)
ERR: validation: critical: user 8: outbid通知が 0件 (期待: 1503件以上、欠落の疑い)
ERR: validation: critical: user 7: outbid通知が 0件 (期待: 1630件以上、欠落の疑い)
(以下 critical 15件省略、計20件、シードユーザー1〜20全員が同じパターン)
SCORE: 40312  (raw 40332, penalty 20)
  GET /auctions            : 14084回 (14084点)
  GET /auctions/:id        : 14142回 (14142点)
  POST /auctions/:id/bids  : 1844回 (9220点)
  GET /auctions/:id/bids   : 1844回 (1844点)
  GET /notifications       : 521回 (1042点)
ERRORS: 20件 (critical: 20件)
RESULT: FAIL
```

**Step 2: `bids.go` を復元し、`webapp/go/closer.go` の won 通知 INSERT をコメントアウト**

```
ERR: validation: critical: user 3: auction 4 を落札したのに won通知が無い
ERR: validation: critical: user 11: auction 2 を落札したのに won通知が無い
ERR: validation: critical: user 16: auction 6 を落札したのに won通知が無い
ERR: validation: critical: user 8: auction 8 を落札したのに won通知が無い
ERR: validation: critical: user 12: auction 10 を落札したのに won通知が無い
SCORE: 39600  (raw 39605, penalty 5)
  GET /auctions            : 13872回 (13872点)
  GET /auctions/:id        : 13947回 (13947点)
  POST /auctions/:id/bids  : 1796回 (8980点)
  GET /auctions/:id/bids   : 1796回 (1796点)
  GET /notifications       : 505回 (1010点)
ERRORS: 5件 (critical: 5件)
RESULT: FAIL
```

**Step 3: `closer.go` を復元し、`webapp/go/notifications.go` のクエリから `WHERE user_id = ?` を除去(引数も除去、`userID` は握りつぶし)**

```
ERR: validation: critical: 入札していない新規ユーザーに通知が 30868件 (期待: 0件、他人宛の混入)
SCORE: 40654  (raw 40655, penalty 1)
  GET /auctions            : 14394回 (14394点)
  GET /auctions/:id        : 14549回 (14549点)
  POST /auctions/:id/bids  : 1805回 (9025点)
  GET /auctions/:id/bids   : 1805回 (1805点)
  GET /notifications       : 441回 (882点)
ERRORS: 1件 (critical: 1件)
RESULT: FAIL
```

**Step 4: `git checkout webapp/go/bids.go webapp/go/closer.go webapp/go/notifications.go` で3ファイルすべて復元して再走行**

```
SCORE: 40247  (raw 40247, penalty 0)
  GET /auctions            : 14193回 (14193点)
  GET /auctions/:id        : 14256回 (14256点)
  POST /auctions/:id/bids  : 1798回 (8990点)
  GET /auctions/:id/bids   : 1798回 (1798点)
  GET /notifications       : 505回 (1010点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

Step 1〜4 とも `docker compose -f dev/compose.yaml up -d --build` で再ビルドしたうえで
`-duration 60s` で走行(各回とも1回目の実行で期待どおりの結果が得られた)。破壊は毎回
Step 4 の `git checkout` で3ファイルまとめて復元し、最終的に `git status --short` /
`git diff HEAD -- webapp/ bench/` が空であることを確認済み。なお本セッションの
スコアは前節までのスコアと単純比較できない(実行間で最大12%程度のばらつきが実測されて
おり、環境要因と切り分けられていないため)。ここでは復元後の `SCORE: 40247` を「validation
が critical を出さない状態でベンチが完走した」ことの証跡として記録するにとどめ、他の
セクションのスコアとの優劣は論じない。

## 3-4 出品

### 設計判断

- **出品は即 live**: upcoming を経由させると60秒走行のうち待ち時間が無駄になる。
  `duration_seconds` は 20〜40秒で、走行中に closed へ遷移して落札 Validation の対象になる
- **pubsub の購読ハンドラは即座に返す**: isucandar の `pubsub.Publish` は購読チャネルが
  満杯だとブロックするため、ハンドラはスライスへの追記のみとし、Capacity にも余裕(1000)を持たせた

### 単調増加検出器の再検証(2026-08-29、Phase 3 の母集団変化を受けて)

Phase 2 では live オークション ~10件・入札者16に対し `FOR UPDATE` を外すと初回検出だったが、
Phase 3 の出品者シナリオで live オークションが同時 ~260件に増え、入札がオークション間で
薄く分散するようになった(1オークションあたり ~2件)ため、同一オークションへの同時入札が
起きにくくなり検出器が無力化されていないか懸念があった。`webapp/go/bids.go` の `postBid` の
オークション行 `SELECT` から `FOR UPDATE` を一時的に外し、`docker compose -f dev/compose.yaml
up -d --build` で再ビルドしたうえで `cd bench && go run . -target http://localhost:8080
-duration 60s` を実行して確認した。

**結果: 1回目の実行で検出**。`bids の金額が単調増加違反`(load中のwatcherおよびValidationの
reconcile双方)と `フィードの金額が単調増加でない` の両方の critical が出た。連鎖的に
`winner_id` 不一致や `outbid通知` の件数不足も検出された。

```
ERR: load: critical: auction 14: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=13(amount=1161) の直後に id=12(amount=1185) が来ており単調減少でない)
ERR: load: critical: auction 18: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=31(amount=2185) の直後に id=30(amount=2298) が来ており単調減少でない)
ERR: load: critical: auction 144: フィードの金額が単調増加でない (id=1122(amount=3838) の次に id=1123(amount=3663))
ERR: validation: critical: auction 14: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=13(amount=1161) の直後に id=12(amount=1185) が来ており単調減少でない)
ERR: validation: critical: auction 18: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=31(amount=2185) の直後に id=30(amount=2298) が来ており単調減少でない)
ERR: validation: critical: auction 144: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=1123(amount=3663) の直後に id=1122(amount=3838) が来ており単調減少でない)
ERR: validation: critical: auction 246: winner_id が 12 (期待: 13 = 最高額 4843 の入札者)
ERR: validation: critical: user 12: outbid通知が 104件 (期待: 105件以上、欠落の疑い)
ERR: validation: critical: user 10: outbid通知が 100件 (期待: 101件以上、欠落の疑い)
ERR: validation: critical: user 19: outbid通知が 149件 (期待: 151件以上、欠落の疑い)
ERR: validation: critical: user 15: outbid通知が 92件 (期待: 93件以上、欠落の疑い)
SCORE: 19290  (raw 19412, penalty 122)
  GET /auctions            : 4262回 (4262点)
  GET /auctions/:id        : 4262回 (4262点)
  POST /auctions/:id/bids  : 1198回 (5990点)
  GET /auctions/:id/bids   : 1198回 (1198点)
  GET /notifications       : 535回 (1070点)
  POST /auctions           : 526回 (2630点)
ERRORS: 122件 (critical: 122件)
RESULT: FAIL
```

1回目で FAIL したため、ブリーフの指示どおり2回目・3回目は実施していない
(「PASSした場合のみ最大2回追試する」規定であり、1回目でFAILならそこで確定)。
`git checkout webapp/go/bids.go` で復元・再ビルドし、通常走行が `RESULT: PASS`(critical
0件)に戻ることを確認した。**観測事実: Phase 3 の出品者シナリオ導入後(live ~260件・
1オークションあたり入札 ~2件という薄い分散)のもとで、`FOR UPDATE` 除去は今回の1試行の
初回で検出された。**ただしこれは試行1回の結果であり、Phase 2 が ~10件の live オークションで
示したのと同じ検出信頼性(取りこぼし率)をここで証明したわけではない。母集団の希薄化は
検出確率を理論上下げる方向に働くため、1回の検出成功だけでは両者が同等の信頼性を持つとは
断定できない。ここで言えるのは「今回の検証では劣化は観測されなかった」ことまでであり、
希薄化そのものがもたらすリスクは Phase 4 への持ち越しとして後述する。

### ベンチのベンチ実測(2026-08-29)

| 壊し方 | 結果 |
|---|---|
| 出品を `status='upcoming'` で INSERT | `RESULT: FAIL` / critical 544件 |
| 復元後 | `RESULT: PASS` / critical 0件 |

### 実測ログ(抜粋)

**Step 1: `webapp/go/auctions.go` の `postAuction` の INSERT 文の `status` を `'live'` → `'upcoming'` に変更**

実際に検出されたのは、ブリーフが想定していた `POST /auctions: 応答が不一致` ではなく、
`GET /stats/me` の出品直後チェック(`listed_count>=1` なのに `live_count=0`)と、
終了処理バッチが `live` を対象にスキャンするため `upcoming` のまま `ends_at` を過ぎた
オークションが `closed` に遷移しない Validation 側の検出だった(いずれも「出品したオークションが
live 一覧に出ないことによる critical」に該当し、ブリーフの想定範囲内)。

```
ERR: load: critical: GET /stats/me: 出品直後なのに listed_count=2 live_count=0
ERR: load: critical: GET /stats/me: 出品直後なのに listed_count=1 live_count=0
(以下 load フェーズで同型の critical が計326件省略)
ERR: validation: critical: auction 192: ends_at (2026-08-29T11:53:46Z) を過ぎているのに status が "upcoming" (期待: closed)
ERR: validation: critical: auction 193: ends_at (2026-08-29T11:53:47Z) を過ぎているのに status が "upcoming" (期待: closed)
(以下 validation フェーズで同型の critical が計218件省略)
SCORE: 44680  (raw 45224, penalty 544)
  GET /auctions            : 17907回 (17907点)
  GET /auctions/:id        : 17918回 (17918点)
  POST /auctions/:id/bids  : 974回 (4870点)
  GET /auctions/:id/bids   : 974回 (974点)
  GET /notifications       : 510回 (1020点)
  POST /auctions           : 507回 (2535点)
ERRORS: 544件 (critical: 544件)
RESULT: FAIL
```

**Step 2: `git checkout webapp/go/auctions.go` で復元して再走行**

```
SCORE: 18278  (raw 18278, penalty 0)
  GET /auctions            : 3990回 (3990点)
  GET /auctions/:id        : 3989回 (3989点)
  POST /auctions/:id/bids  : 1128回 (5640点)
  GET /auctions/:id/bids   : 1127回 (1127点)
  GET /notifications       : 511回 (1022点)
  POST /auctions           : 502回 (2510点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

Step 1〜2 とも `docker compose -f dev/compose.yaml up -d --build` で再ビルドしたうえで
`-duration 60s` で走行(各回とも1回目の実行で期待どおりの結果が得られた)。破壊は
Step 2 の `git checkout` で復元し、最終的に `git status --short` / `git diff HEAD --
webapp/go` が空であることを確認済み。

## Phase 3 総括

### 最終スコア(2026-08-29、初期実装、60秒走行×3回)

| 回 | スコア | 結果 |
|---|---|---|
| 1 | 18622 | PASS |
| 2 | 18190 | PASS |
| 3 | 17378 | PASS |

3回とも critical 0件で `RESULT: PASS`。最大18622・最小17378の差は1244で、平均(約18063)に
対して約6.9%のばらつきに収まっており、ブリーフが求める±20%以内の再現性を満たす。

内訳(1回目):
```
SCORE: 18622  (raw 18622, penalty 0)
  GET /auctions            : 4183回 (4183点)
  GET /auctions/:id        : 4184回 (4184点)
  POST /auctions/:id/bids  : 1150回 (5750点)
  GET /auctions/:id/bids   : 1150回 (1150点)
  GET /notifications       : 485回 (970点)
  POST /auctions           : 477回 (2385点)
ERRORS: 0件 (critical: 0件)
RESULT: PASS
```

このスコア(~18000台)は3-1〜3-3節に記録されている60秒走行のスコア(29892点〜40654点)
とは水準が異なるが、これは開発機のブレ(実行間で最大12%程度)に加え、前タスクで出品者
シナリオが加わり同時 live オークション数が ~10件から ~260件規模へ変わったことで一覧
エンドポイントのオークションごとN+1が負荷特性を作り変えたためであり、両者は測っている
ものが異なる。本ドキュメントでは前節までと同様、異なるフェーズ・異なる負荷特性のスコアを
優劣として比較しない。

> **付記(最終レビューのFIX1適用後、2026-08-29)**: 上記「最終スコア」3回および本節までの
> 全てのスコア実測は、`awaitFeedReflection`(`bench/load.go`)がフィード反映を確認する
> ポーリング1回ごとに `ScoreGETFeed` を加点していた旧採点ルールの下で取得したものである。
> この旧ルールは反映が遅い(=デッドラインぎりぎりまでポーリングを重ねる)実装ほど
> `GET /auctions/:id/bids` の点が積み上がる逆インセンティブを持っており、全体の採点式で
> 比較すると1回の入札あたりの所要時間が0.8秒を超えるあたりから、フィード反映を遅延させる
> 実装の方が高スコアになりうることが判明した(最終レビュー指摘、FIX1で修正)。修正後は
> 「反映を確認できた」ことに対して1回だけ加点するようにした。したがって本節以前に記録された
> スコアはすべて旧ルールの下での値であり、この修正後の値と単純比較できない。
>
> 修正後(既定 worker数、`-duration 60s`、`docker compose -f dev/compose.yaml up -d --build`
> で再ビルド済みのスタックに対して1回走行)の実測:
> ```
> SCORE: 16105  (raw 16105, penalty 0)
>   GET /auctions            : 4216回 (4216点)
>   GET /auctions/:id        : 4224回 (4224点)
>   POST /auctions/:id/bids  : 891回 (4455点)
>   GET /auctions/:id/bids   : 891回 (891点)
>   GET /notifications       : 337回 (674点)
>   POST /auctions           : 329回 (1645点)
> ERRORS: 0件 (critical: 0件)
> RESULT: PASS
> ```
> `GET /auctions/:id/bids`(891回)は `POST /auctions/:id/bids`(891回)と完全に一致しているが、
> これは修正が挙動を変えたことの実測的な証拠にはならない。旧ルールが生んでいた露出窓は
> 実在していて、フィードが最大で2秒弱古いままでも入札1件につきフィード加点を何度も稼げる
> (3-2節の staleness実験で `POST /auctions/:id/bids` 112回に対し `GET /auctions/:id/bids`
> 2202回、約19.7倍という実測がその大きさを示している)。しかしこの露出窓は、コミット済みの
> 入札が次の読み取りで即座に見えるreference実装のような素直な実装に対しては開かない。
> `awaitFeedReflection` のポーリングはほぼ常に1回目で成功するため、旧ルールで採点しても
> 新ルールで採点しても通常のPASS走行では件数は1:1になる。したがって今回の891==891は
> 「修正が挙動を変えた」ことの実測的な裏付けではなく、このreference実装における通常の
> 結果にすぎない。修正の正当化根拠は採点式の算術と3-2節のstaleness実験であり、件数の
> 一致ではない。この値(16105)は上記の18000台とも水準が異なるが、採点ルール自体が
> 変わっているため尚更、優劣としては比較しない。

### Phase 4 への持ち越し

- 一覧 `GET /auctions` の非トランザクショナルなレース(検索実装時に一貫化が必要)
- 終了処理バッチの複数台での二重実行(Phase 5 の IaC でレギュレーション化)
- `go.mod` の go ディレクティブ不揃い(webapp 1.26.1 / bench 1.26.4)
- compose の nginx readiness 未設定(起動直後の502ウィンドウ)
- **単調増加検出器の母集団希薄化リスク**: 出品者シナリオにより同時 live オークションが
  ~10件から ~260件規模に増え、1オークションあたりの入札数が ~180件から ~2件程度まで
  薄まった。単調増加検出は同一オークションへ複数の同時入札が競合することに依存するため、
  出品レート(sellers数)や走行時間をさらに引き上げてオークションあたりの入札密度が今より
  下がった場合、検出確率が理論上さらに低下しうる。本タスクの検証(上記「単調増加検出器の
  再検証」)では既定フラグ・60秒走行という現状の条件下で1回目に検出できており、現時点では
  欠陥ではなく監視すべきリスクとして記録する。出品レートやシナリオ構成を変える際は
  このリスクを踏まえて再検証すること
- **`listingBoard`(`bench/load.go`)が closed になった listing を刈らない**: pubsub で
  配信された出品IDは走行終了までスライスに溜まり続けるため、走行後半にはボードの
  半分近くが既に closed になっている。`bidderIteration` がボードから引いた id の
  約25%は詳細取得後の status チェックで入札せずに抜けるだけの回になり、pubsub による
  「新規出品への集中」効果は走行序盤ほど強く、終盤ほど弱まる(意図とは逆方向)。
  固定長のリングバッファにする、または bidder が `closed` を観測した時点で該当idを
  ボードから外す、のいずれかで直せる。意図的な「遅さ」の仕様ではなく単なる
  未実装のため、修正候補として持ち越す
- **ネタバレ面の整理(リリース時に必須)**: 参照実装のソースに「意図的に遅い実装: bids に
  `auction_id` のインデックスが無い」のように**修正方法を名指しするコメントが12箇所ある**
  (`auctions.go` `auth.go` `bids.go` `closer.go` `feed.go` `notifications.go` `stats.go`)。
  設計ドキュメントは仕込みの解説を `docs/writeup.md` に「ネタバレ、別置き」と定めているが、
  インラインコメントはこの分離を壊している。`writeup.md` は読まない選択ができるのに対し、
  `feed.go` は読まずにチューニングできないため、競技者にとっては回避不能なネタバレになる。
  同様に `docs/phase3-notes.md`(本ファイル)はベンチの全検証項目・エラー文言・`FOR UPDATE`
  除去の検出方法まで記載しており、コメントより大きなネタバレ。`docs/phase2-notes.md` の
  仕込みインベントリも同様。
  —— **現時点では意図的に据え置く**。これらのコメントは開発中に実装者や
  エージェントが善意で最適化してしまうのを防ぐ防波堤として実際に機能しており、
  競技者がまだ存在しない段階で外すと保護だけ失う。
  —— **実際に競技で使ってもらう段階で外す**。OSS問題ではリポジトリ自体が配布物になるため、
  この作業は「競技者が受け取るものを定義する」工程と不可分であり、Phase 4 の
  レギュレーション文書作成に畳み込むこと。その際は (1) 参照実装からの修正方法を名指しする
  コメントの除去、(2) 保護を `CLAUDE.md` 等の開発者・エージェント向けの場所へ移設、
  (3) ネタバレを含む docs の扱い(別ディレクトリ/別ブランチ/明示的な警告)の3点をセットで扱う。
  Phase 5 の多言語参照実装では、移植担当者が「何を意図的に遅いまま保つか」を知る必要があるため、
  移設先は移植時にも参照される場所にすること
- **`GET /notifications`(`webapp/go/notifications.go`)に `LIMIT` が無い**: 応答サイズが
  走行時間とともに単調に増える。これは見落としというより意図的なトレードオフとして
  受け入れる: `LIMIT` を付けると `bench` の `validateNotifications` が行う
  `CountByType(ns, "outbid") >= MinOutbid` という下限チェック(台帳から導いた期待件数との
  突合)が、古い通知が切り捨てられることで成立しなくなる(欠落していないのに欠落したと
  誤検知するfalse-FAILを生む)。したがって全件返す実装を維持する必要があり、今後
  「直さない」判断として記録しておく。将来の担当者がこれを見て安易に `LIMIT` を
  追加すると、通知検証が壊れることに注意
