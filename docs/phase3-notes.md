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
