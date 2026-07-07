# Phase 2 持ち越し事項(Phase 1最終レビューのトリアージ結果)

Phase 1ブランチ全体レビュー(2d266e0..a452005)で「マージ可」と判定された際の、Phase 2で対処する項目。

## チェッカー強化バッチ(Loadフェーズ実装前に1セットでやる)

1. **順序の検証がない**: `ValidateInitialAuctionList` は一覧の `ends_at ASC` 順を検証しない(mapで照合しているため)。詳細の入札 `created_at DESC` 順も同様。N+1をJOINに書き換えた際に壊れやすいポイントなので、Prepareで順序をピン留めする
2. **Prepareがシード詳細を検証しない**: `GET /auctions/:id` の description / starting_price / シード入札列(auction 1: 1500/1200/1000, users 4/3/2)の期待値テーブルを追加する
3. **closedオークションをシードに追加**: 現状全件liveのため一覧のstatusフィルタが実証されていない(`WHERE status='live'` を落としてもPASSする)
4. **PostBidの5xx判別**: 現在は5xxも err=nil で返す。Loadシナリオではアプリエラーとして分類する
5. **register の一律409を1062判定に**: `mysql.MySQLError.Number == 1062` のときだけ409、他は500(現状はDB障害も「name already taken」になる)
6. **validate.go の未テスト分岐**(unknown id / not-live / seller不一致 / current_price<amount)にテスト追加
7. **invalid-id 400 / not-live 400 のハンドラテスト追加**(not-liveは3のシード追加後に可能)

## 設計判断が必要な事項

- **`GET /auctions/:id` の一貫性エンベロープ**: 詳細レスポンスは複数の非トランザクショナルreadの合成であり、並行入札下では `current_price < max(bids[].amount)` 等の内部不整合がありうる。Loadの検証を書く前に「参照実装がスナップショット読みに直す」か「ベンチが有界の stale を許容する」かをスペックに明記する

## 仕込み(意図的な遅さ)のインベントリ — writeupに記載、誤って"修正"しない

- bcrypt cost 12(auth.go)
- N+1: オークションごとの MAX/COUNT/seller、入札ごとの user(auctions.go)
- FOR UPDATE ロック中の MAX(amount) 全件走査(bids.go)
- セカンダリインデックスなし(00_schema.sql)
- nginx: upstream keep-alive なし、静的配信もアプリ経由(dev/nginx.conf)
- `db.SetMaxOpenConns(10)`(db.go)

## その他メモ

- initialize後に古いセッションCookieを使うと dangling user_id の入札が作れて詳細が500になる。Phase 2ベンチ設計では「initialize前のセッションを再利用しない」を守る(現状のPrepareは遵守済み)
- go.mod の goディレクティブがモジュール間で不揃い(webapp 1.26.1 / bench 1.26.4)。`go 1.26` に揃える
- compose: nginxのdepends_onにreadiness条件がなく起動直後に502ウィンドウあり(appにhealthcheck追加で解消可)
