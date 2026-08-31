# ISUBID

ISUCON形式の性能チューニング競技問題。題材は椅子専門のライブオークションサイト。

- お題アプリ(参照実装): `webapp/go`(わざと遅い初期実装)
- ベンチマーカー: `bench`(isucandarベース)
- 設計ドキュメント: `docs/superpowers/specs/2026-07-08-isubid-design.md`

## クイックスタート

```bash
# 1. 初期データを生成(初回のみ。生成物は initial-data/out/ にコミット済みなので通常は不要)
cd initial-data && go run . -scale small -out out && cd ..

# 2. フルスタック起動(nginx :8080 → app :8000 → mysql :3306)
docker compose -f dev/compose.yaml up -d --build

# 3. ベンチ実行(60秒の負荷走行+整合性検証。-snapshot で生成データの正解を渡す)
cd bench && go run . -target http://localhost:8080 -snapshot ../initial-data/out/snapshot.json

# worker 数を変える(既定: bidders 8 / watchers 4 / notifiers 2 / sellers 2 / visitors 2)
go run . -target http://localhost:8080 -bidders 16 -sellers 4 -visitors 4

# 整合性チェックのみ(負荷なし)
go run . -target http://localhost:8080 -prepare-only -snapshot ../initial-data/out/snapshot.json
```

60秒走行後に `SCORE: <点数>` と `RESULT: PASS` が出れば成功。`-prepare-only` では従来通り `PREPARE: PASS` が出れば疎通完了。

初期データ無しで動かす場合は `-snapshot` を省き、`dev/compose.yaml` の `ISUBID_INITIAL_DATA_DIR` を外す(シードデータのみで起動する)。

## 開発

```bash
# アプリのテスト(compose の mysql が必要)
docker compose -f dev/compose.yaml up -d mysql
cd webapp/go && go test ./...

# ベンチのテスト
cd bench && go test ./...
```

### フロントエンドを変更したとき

```bash
cd webapp/frontend && npm install && npm run build   # -> webapp/public/
cd ../../bench && go run ./cmd/genmanifest -public ../webapp/public -out assets/manifest.json
go test ./...                                        # マニフェストのドリフト検知
```

`webapp/public/` と `bench/assets/manifest.json` はセットでコミットする。
片方だけ更新すると Prepare がアセットのハッシュ不一致で FAIL する。

## ステータス

Phase 3(pub/sub要素)まで完了。入札フィード・通知ファンアウト・終了処理と落札確定・
出品者シナリオが動き、ベンチがそれぞれの整合性を検証する。
Phase 4-A(初期データジェネレータ)も完了。乱数シード固定・規模パラメータ化した
ジェネレータ(`initial-data/`)で `small`/`medium`/`full` の3規模を実測し、
4ゲート(`/initialize` 所要時間・Prepare所要時間・60秒走行・`FOR UPDATE`除去検出)を
すべて満たす `small` を採用した。`medium`/`full` は不採用で、不合格の原因は規模ごとに
異なる: `medium` はゲート4(`FOR UPDATE`除去時の単調増加検出)のみで不合格になり、
live auction が増えて入札が薄く分散したことによる検出器の感度低下が原因。`full` は
それとは別に、ゲート2(Prepareが6秒基準を大幅に超える11〜12秒)でも独立に不合格となり、
ゲート4の不合格も検出器の感度低下ではなく `GET /auctions` の全件タイムアウトで
入札が1件も成立しなかったことによる検出不能という、全く異なる機序だった。
詳細は `docs/phase4-notes.md` の「サイジング実測」節と4-Eへの持ち越しを参照。
Phase 4-B(一覧・検索)、4-C(アイコンBLOB)、4-D(SPAフロントエンドと静的配信)、
4-E1(ベンチの合否判定)も完了。SPA(React 18 + Vite 5 + TypeScript、6画面)の
ビルド成果物を `webapp/public/` にコミットし、アプリ経由の静的配信を攻略対象として
走行に乗せている。API は SPA のクライアントルートとの衝突を避けるため `/api` 配下。
**フロントエンドは参加者が触らない前提で、Prepare が成果物のハッシュを照合する。**

レギュレーション文書・マニュアル・writeup は Phase 4-F の残りタスク。
