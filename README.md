# ISUBID

ISUCON形式の性能チューニング競技問題。題材は椅子専門のライブオークションサイト。

- お題アプリ(参照実装): `webapp/go`(わざと遅い初期実装)
- ベンチマーカー: `bench`(isucandarベース)
- 設計ドキュメント: `docs/superpowers/specs/2026-07-08-isubid-design.md`

## クイックスタート

```bash
# 1. フルスタック起動(nginx :8080 → app :8000 → mysql :3306)
docker compose -f dev/compose.yaml up -d --build

# 2. ベンチ実行(60秒の負荷走行+整合性検証)
cd bench && go run . -target http://localhost:8080

# worker 数を変える(既定: bidders 8 / watchers 4 / notifiers 2 / sellers 2)
go run . -target http://localhost:8080 -bidders 16 -sellers 4

# 整合性チェックのみ(負荷なし)
go run . -target http://localhost:8080 -prepare-only
```

60秒走行後に `SCORE: <点数>` と `RESULT: PASS` が出れば成功。`-prepare-only` では従来通り `PREPARE: PASS` が出れば疎通完了。

## 開発

```bash
# アプリのテスト(compose の mysql が必要)
docker compose -f dev/compose.yaml up -d mysql
cd webapp/go && go test ./...

# ベンチのテスト
cd bench && go test ./...
```

## ステータス

Phase 3(pub/sub要素)まで完了。入札フィード・通知ファンアウト・終了処理と落札確定・
出品者シナリオが動き、ベンチがそれぞれの整合性を検証する。
フロントエンド・検索・アイコンBLOB・初期データジェネレータ・レギュレーション文書は Phase 4。
