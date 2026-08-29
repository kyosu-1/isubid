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
| フィード応答を3秒遅延 | `RESULT: PASS` / critical **0件**(期待した `2s 以内にフィードへ反映されない` は検出されず。既知の問題として後述) |
| フィードを `ORDER BY id DESC` に変更 | `RESULT: FAIL` / critical 105件 / `フィードが id 昇順でない` |
| 復元後 | `RESULT: PASS` / critical 0件 / `SCORE: 32428 (raw 32428, penalty 0)` |

### 既知の問題: 3秒遅延では critical が検出されない

Step 1(`getAuctionBids` 冒頭に `time.Sleep(3 * time.Second)` を挿入)は `-duration 30s` で
2回実行したが、いずれも `RESULT: PASS` / `ERRORS: 0件` だった(下記実測ログ参照)。ブリーフの
期待(`2s 以内にフィードへ反映されない` の critical)は一度も検出できず、再現性も確認済み
(2回とも同じ結果)。

原因は `bench/load.go` の `awaitFeedReflection`(44–77行目)の実装にある:

```go
deadline := time.Now().Add(feedReflectDeadline)
for {
    feed, err := c.GetBidFeed(ctx, auctionID, since)   // ここが3秒ブロックする
    ...
    for _, b := range feed {
        if b.ID == bidID {
            return // 反映を確認できた ← ここでは経過時間を見ていない
        }
    }
    if time.Now().After(deadline) { ... }              // 見つからなかった時だけ判定
    ...
}
```

`POST /auctions/:id/bids` が201を返した時点で入札は既にコミット済みなので、その後に打つ
最初の `GET /auctions/:id/bids` は(応答が3秒遅くても)クエリ自体は最新のコミット結果を
返す。ループは「見つかった」を「見つからなかった」より先にチェックしているため、1回目の
応答がどれだけ遅れて届いても、その中に対象の bid が含まれていれば即座に成功として返って
しまう。経過時間のチェックは bid が見つからなかった場合の分岐にしか無く、見つかった場合の
分岐では一度も経過時間を見ていない。つまりこの check は「反映が2秒以内か」ではなく
「(応答が返ってきさえすれば)最終的に見つかるか」しか検証できておらず、応答レイテンシに
起因する反映遅延を検出できない。

この checkのバグ自体は本タスクのスコープ外(`webapp/` `bench/` の変更は許可されておらず、
実際どちらも変更していない)のため未修正のまま記録する。修正には `awaitFeedReflection` を
「bid が見つかった時点でも経過時間を確認し、`feedReflectDeadline` を超えていたら critical
にする」よう改める必要がある。

### 実測ログ(抜粋)

**Step 1: `webapp/go/feed.go` の `getAuctionBids` 冒頭に `time.Sleep(3 * time.Second)` を挿入(`-duration 30s` で2回実行)**

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

Step 1・2 とも `docker compose -f dev/compose.yaml up -d --build` で再ビルドしたうえで走行。
2回ともハングせず、想定外の種類のエラー(不正なJSON・想定外ステータスコードなど)も出ず、
`RESULT` が確定して正常終了した。

ただし「ポーリングループがデッドライン分岐・ポーリング間隔待ちに初めて到達した」とまでは
言えない: `POST /auctions/:id/bids` 80回に対し `GET /auctions/:id/bids` は72回しか記録されて
おらず(2回とも同じ72回)、比率はほぼ1:1で、差分は走行終了間際に ctx キャンセルされた分と
辻褄が合う。これは「見つからずに複数回ポーリングした」ケースがほぼ無かった(=ほぼ全件が
1回目のポーリングで即成功した)ことを示唆している。前述の原因分析(bid は POST の201時点で
既にコミット済みなので、応答が遅くても1回目のクエリで見つかる)と整合的であり、むしろ
このデッドライン分岐・ポーリング間隔待ちの分岐が、この Step 1 の実行でも依然として
実際には踏まれていない可能性が高い、という点こそがこの既知の問題の裏付けになっている。

破壊は毎回 `git checkout webapp/go/feed.go` で復元し、最終的に `git status --short` /
`git diff HEAD -- webapp/ bench/` が空であることを確認済み。
