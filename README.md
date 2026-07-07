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

Phase 2b-1(負荷走行・スコアリング)まで完了。初期データジェネレータ・pub/sub要素・フロントエンドは今後のPhase。
