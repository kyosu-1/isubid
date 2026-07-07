# Phase 2 持ち越し事項(Phase 1最終レビューのトリアージ結果)

Phase 1ブランチ全体レビュー(2d266e0..a452005)で「マージ可」と判定された際の、Phase 2で対処する項目。

## チェッカー強化バッチ(Loadフェーズ実装前に1セットでやる)

1. [済] **順序の検証がない**: `ValidateInitialAuctionList` は一覧の `ends_at ASC` 順を検証しない(mapで照合しているため)。詳細の入札 `created_at DESC` 順も同様。N+1をJOINに書き換えた際に壊れやすいポイントなので、Prepareで順序をピン留めする
2. [済] **Prepareがシード詳細を検証しない**: `GET /auctions/:id` の description / starting_price / シード入札列(auction 1: 1500/1200/1000, users 4/3/2)の期待値テーブルを追加する
3. [済] **closedオークションをシードに追加**: 現状全件liveのため一覧のstatusフィルタが実証されていない(`WHERE status='live'` を落としてもPASSする)
4. [済(クライアント側)] **PostBidの5xx判別**: 現在は5xxも err=nil で返す。Loadシナリオではアプリエラーとして分類する(クライアント側でerr扱いにするのは済。Loadシナリオでの5xx分類はPhase 2bで実装)
5. [済] **register の一律409を1062判定に**: `mysql.MySQLError.Number == 1062` のときだけ409、他は500(現状はDB障害も「name already taken」になる)
6. [済] **validate.go の未テスト分岐**(unknown id / not-live / seller不一致 / current_price<amount)にテスト追加
7. [済] **invalid-id 400 / not-live 400 のハンドラテスト追加**(not-liveは3のシード追加後に可能)

## 設計判断が必要な事項

- **[解決済み 2b-1] `GET /auctions/:id` の一貫性エンベロープ**: 「参照実装がスナップショット読みに直す」を採用(2b-1のLoad実走行で bid_count と bids 件数の不整合レースが実証されたため)。詳細レスポンスの内部一貫性(bid_count == bids件数、current_price == max(bids))は**レギュレーション不変条件**で、ベンチのウォッチャーが検証する。参照実装は読み取りを1トランザクション(REPEATABLE READスナップショット)に包み、N+1等の意図的なボトルネックはその中に温存
- **[未対応・バックログ] `GET /auctions`(一覧)にも同型のレースが残存**: summarize がオークションごとに非トランザクショナルに呼ばれる。現状ベンチは一覧のフィールド間不変条件を検証していないため誤検知はないが、一覧レベルの検証を強化する際は先に参照実装側の一貫化(または検証許容幅の明記)が必要

## 仕込み(意図的な遅さ)のインベントリ — writeupに記載、誤って"修正"しない

- bcrypt cost 12(auth.go)
- N+1: オークションごとの MAX/COUNT/seller、入札ごとの user(auctions.go)
- FOR UPDATE ロック中の MAX(amount) 全件走査(bids.go)
- セカンダリインデックスなし(00_schema.sql)
- nginx: upstream keep-alive なし、静的配信もアプリ経由(dev/nginx.conf)
- `db.SetMaxOpenConns(10)`(db.go)

## その他メモ

- **[解消 2b-1]** `bench/scenario.go` の `Load`/`Validation` は2b-1で本実装済み(no-opスタブだった経緯はコメントに残置)。isucandarはこの2フェーズの実装がゼロだと `parallel.Wait()` がデッドロックしうるため、実装を削除しないことだけ引き続き注意
- initialize後に古いセッションCookieを使うと dangling user_id の入札が作れて詳細が500になる。Phase 2ベンチ設計では「initialize前のセッションを再利用しない」を守る(現状のPrepareは遵守済み)
- go.mod の goディレクティブがモジュール間で不揃い(webapp 1.26.1 / bench 1.26.4)。`go 1.26` に揃える
- compose: nginxのdepends_onにreadiness条件がなく起動直後に502ウィンドウあり(appにhealthcheck追加で解消可)

## Phase 2b バックログ(2a最終レビューの残穴指摘)

Prepareに安く足せるもの(2b冒頭で対応):
- [済] auction 11/12(not-live)への入札が400になることをベンチで実証(シード投資の回収)
- [済] closed(11)/upcoming(12)の詳細ページ検証(12000/2件/closed、upcoming/0件)
- [済] `expectedInitialAuctions` に ends_at 期待値を追加して階段値を厳密照合(現状はID順チェック頼み)
- [済] `ValidateInitialAuctionDetail` に Title/Status/BidCount/CategoryID を追加(fixtureの BidCount:0 も修正)
- [済] too-low 400 の body(`current_price`)のパースと検証、エラーボディ形 `{"error":...}` の検証
- [済] `bench/main.go` に `isucandar.WithLoadTimeout(...)` を追加(Loadタイムアウト未設定はLoad実装後も同型のハング要因)

構造的な課題(2b設計に織り込む):
- 現シードは id順=ends_at順 が相関しているため `ORDER BY id ASC` と区別不能。Loadフェーズで非相関な出品を作って初めて判別可能
- category_id はどこでも未検証。winner_id/winning_price はAPIに露出しておらず落札結果をE2Eで検証できない(2bでのAPI設計判断)
