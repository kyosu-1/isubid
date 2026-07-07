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
- **[解決済み 2b-1 fix-round] POST成功だがベンチが201を受け取れない場合のfalse-FAIL**: 従来はLoadが201受信時にのみ台帳(Ledger)へ記録しており、応答受信前のctxキャンセルや転送エラーで「サーバー側では実はコミット済み」の入札が台帳に載らず、Validationで「台帳にない/消えた入札」として誤検知(false-FAIL)しうる欠陥があった。`bench/ledger.go` にpending intentの概念を追加: POST送信前に `Ledger.Intent` で記録し、201確定で `Confirm`(acceptedへ昇格)、4xx確定で `Reject`(pending解消)、転送エラー/ctxキャンセル等の結果不明時は pending のまま残す。Validationは `bench/reconcile.go` の `reconcileAuction`(純粋関数、`bench/reconcile_test.go` でテーブル駆動テスト済み)で、シード入札(id<=8)∪台帳の確定受理入札∪結果不明pendingの3集合で各auctionの入札集合とcurrent_priceを両方向に突き合わせる(id>8の未説明入札は「台帳にない入札(二重適用の疑い)」でcritical、シード入札の消失も個別にcritical)。あわせてValidationの `GetAuction` はM1として最大3回・短backoffで再試行するようにした
- **[解決済み 2b-1 fix-round] addErrのctxキャンセル握り潰し範囲を限定**: 従来は `ctx.Err() != nil` なら種別を問わずエラーを握り潰しており、Load終了直前に成立した本物のcritical違反まで消えるリスクがあった。`context.Canceled`/`context.DeadlineExceeded` に起因するエラーだけを対象に限定(`bench/load.go`)
- **[解決済み 2b-1 fix-round] watcherにcurrent_price==max(bids)の不変条件チェックを追加**: 上記の「詳細は1トランザクションで一貫」という設計判断をドキュメントで謳っていたが、ウォッチャー側で未実証だったため `bench/load.go` の `watcherIteration` に追加(bids非0ならmax一致、0件ならstarting_price一致)
- **[解決済み 2b-1 monotonicity-round] 入札金額の単調増加(直列化)witnessを追加**: bid APIは「オークション行をFOR UPDATEでロックしたまま amount > 現在最高額 のときのみ受理」なので、受理順(created_at ASC, id ASC = ロック取得順)でamountは厳密単調増加になり、DESC順で返る一覧では厳密単調減少になるはず。`bench/validate.go` に `ValidateBidAmountsMonotonic(bids []Bid) error` を追加し、既存の `ValidateBidsOrdered`(順序)と合わせて呼ぶ `ValidateBidsInvariant(bids []Bid) error` を新設。呼び出し箇所は (1) `watcherIteration`(critical)、(2) `reconcile.go` の `reconcileAuction`(Validationフェーズ、critical)、(3) `Prepare` の auction 1 初期詳細(`ValidateInitialAuctionDetail` 経由)・入札後詳細・closed auction 11詳細、の計5箇所全て。単体テストはstrictly-decreasing(seed fixture 1500/1200/1000)がpass、同額・DESC内での金額逆転がfail、`ValidateBidsInvariant` が両検証を合成することを確認(`bench/validate_test.go`)。`bench/reconcile_test.go` の既存fixtureのうち、id>8の金額が偶然シード額を下回っていた1件はamountを単調性を満たす値に補正し、同額2件で「pending消費は1回まで」を検証していたfixtureは、同額そのものが単調性違反にも該当するため期待エラー数を1→2に修正(意味的に正しい: 同額受理はFOR UPDATE不在の兆候そのもの)。
  - **bench-of-bench実測(2026-07-08)**: `webapp/go/bids.go` の `FOR UPDATE` を一時的に除去してビルドし、`go run . -duration 15s -bidders 16` を実行したところ **1/1回目で検出**(3回まで許容されていたが初回でPASS→FAILへ倒れた。critical 1124件、`bids の金額が単調増加違反` を複数auctionでdetect、`RESULT: FAIL`)。直後に `git checkout webapp/go/bids.go` で復元・再ビルドし、`-duration 60s` の通常走行で `RESULT: PASS`(critical 0件)を再確認。

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
