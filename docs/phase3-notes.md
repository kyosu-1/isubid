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
