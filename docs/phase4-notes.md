# Phase 4 実装ノート

## 4-A 初期データジェネレータ

### 設計判断

- **生成データは Phase 1 シードを置き換えず id 13 から continue する**: `webapp/go` の
  テスト27本がシードの具体値(auction 1 の入札列 1500/1200/1000、seed_user_05、
  auction 11 の落札者12など)に依存しており、置き換えると値ベースのアサーションを
  性質ベースへ全面的に書き直すことになる。前置きに残せばテストスイートと
  Prepare の詳細検証が無傷で済み、書き直しは一覧レベルだけに限定できる
- **生成データの読み込みは `ISUBID_INITIAL_DATA_DIR` による明示的オプトイン**:
  生成物はコミットするため「ファイルが存在すれば読む」では条件として機能せず、
  `go test` が `/initialize` 経由で生成データを読み込んでテストが全滅する。
  同じ変数が読み込み経路(init.sh / Go)も切り替えるので、ホストに mysql クライアントが
  無い環境でも webapp のテストが動く
- **一覧の順序検証を完全一致から2性質へ**: live が約260件になるとシードと生成分が
  ends_at 順で交互に並び、期待 id 列との完全一致は同着やミリ秒のズレで壊れる。
  (a) ends_at が非減少 (b) id 昇順にソートされていない、の2つに置き換えた。
  `ORDER BY id ASC` への書き換えは両方に引っかかるため検出力は落ちない
- **`applyGeneratedSchedule` の `WHERE id > 12` が必須である**: シードは
  `applyRelativeSchedule` で既に現在時刻基準の絶対時刻になっており、そこへ固定エポック
  起点の変換を当てると `TIMESTAMPDIFF` が約8億3600万秒となり ends_at が2052年へ飛ぶ

### サイジング実測(2026-08-30)

計測はブランチ `phase-4a-initial-data` の HEAD(`ba0bd20`、`ff773f2` の charset 修正込み)を
`dev/compose.yaml` で都度ビルドし直した状態で行った。すべて実際に観測した出力からの転記であり、
未実測の数値は書いていない。

| scale | 生成物サイズ | ゲート1 initialize | ゲート2 Prepare | ゲート3 60秒走行 | ゲート4 FOR UPDATE除去 |
|---|---|---|---|---|---|
| small | 2.4M合計(users 44K / auctions 192K / bids 1.7M / notifications 562K, snapshot.json 38K) | 0.28〜0.34s、3/3 成功(HTTP 200・notifications件数4000で確認)。15s基準に対して十分な余裕 | 1.50〜1.62s、3連続PASS(6s基準内)。ただし下記ハザードによる非決定的FAILあり | Load フェーズへ到達した4回すべてFAIL。原因はスケールではなく下記「ハザードB」(bench側の既知の検証ギャップ)。単調増加検出とは無関係 | 3回目の試行で検出(`フィードの金額が単調増加でない` / `bids の金額が単調増加違反`, auction 1091)。復元後2回の走行で単調増加系criticalは0件に回帰 |

### 計測中に発見した2つの環境ハザード

このタスクは計測が目的だったが、実測の過程で `webapp/go` と `bench` それぞれに、生成データ
導入によって初めて表面化したと見られる問題を発見した。**タスクの制約により `webapp/`・`bench/`
は一切変更していない**(このコミットは本ファイルのみ)。以下はいずれも実際に踏んだ事象の記録であり、
next-step の判断材料として残す。

#### ハザードA: `runAuctionCloser` と `applyGeneratedSchedule` の実行順序による初期化レース

`webapp/go/closer.go` の `closerInterval = 1 * time.Second` により、終了処理バッチはアプリ起動中
常時1秒間隔で動き続けている。一方 `webapp/go/initialize.go` の `postInitialize` は
`loadViaInitScript`(生成データ含む全ダンプ投入)を完走させてから `applyRelativeSchedule` →
`applyGeneratedSchedule` の順で時刻を書き換える。生成データの `92_auctions.sql` は
`status IN ('live','upcoming')` の行を固定エポック(`generatedEpochLiteral = "2000-01-01 00:00:00"`)
起点の相対時刻のまま投入するため、`92_auctions.sql` 投入直後から `applyGeneratedSchedule` 完了までの
間、生成 live オークション(small で50件)は closer の `WHERE status='live' AND ends_at <= NOW(6)`
に恒常的に合致し続ける。この窓(93_bids.sql・94_notifications.sql の投入を含む)にバッチの1秒tickが
1回でも重なると、closer がその瞬間の live 該当行を**全件まとめて**閉じ、以下のいずれかを引き起こす。

- 対象オークションにまだ bids が投入されていなければ通知は作られず(`sql.ErrNoRows` 分岐)、
  ステータスだけが無音で closed に変わる
- 既に bids が投入済みであれば `INSERT INTO notifications` を AUTO_INCREMENT 経由で行い、
  生成データ側が明示IDで後から同じ範囲に書き込もうとして `Duplicate entry` で衝突し、
  `POST /initialize` が 500 を返す(このとき `applyRelativeSchedule`/`applyGeneratedSchedule`
  は未実行のまま初期化が中断され、テーブルは不整合な状態で残る)

再現ログ(コンテナを `docker compose down -v` で完全に作り直した直後、1回目の `/initialize` から発生。
過去の操作の蓄積は無関係):

```
{"error":"init.sh: exit status 1: --------------\nINSERT INTO notifications ...
ERROR 1062 (23000) at line 1: Duplicate entry '1' for key 'notifications.PRIMARY'\n"}
```

直後の確認では `select count(*) from notifications` が `35`(closer が無音で作った分)。
一方 `HTTP 200` かつ `notifications=4000` が返った回では、生成 live 50件・upcoming 25件・closed 1000件
が毎回寸分違わず一致しており、成功時のデータは完全に正しいことも確認した(3回連続で確認)。
つまりこのレースは all-or-nothing で、成功すれば汚染は残らない。

観測された成功率は、単発の `curl -XPOST /initialize` で概ね 7〜8割、`bench` の `-prepare-only`
(内部で毎回 `Initialize()` を呼ぶ)でも概ね6〜7割で、単発呼び出しとしては致命的ではない。
ただし `bench -duration 60s` の Prepare 内 `Initialize()` は独立に同じレースに晒され、
この計測では素の webapp に対して 60秒走行を試みた7回中3回が Prepare 段階の
`Duplicate entry` で終了した(Load フェーズに到達したのは残り4回。下表・下記ログの
「Load フェーズへ到達した4回」はこの4回を指す)。窓の長さは生成データの投入時間に比例するため、
より大きいスケールほど失敗率が上がる方向に効くと考えられる(未検証の推測)。

回避策: 本タスクでは `webapp/go` を変更できないため、`POST /initialize` が 500 を返した場合は
その走行を破棄し、成功(HTTP 200 かつ件数一致)を確認してから次の手順に進むことで対処した。

#### ハザードB: bench の Validation フェーズが生成 live オークションを追跡していない

`bench/scenario.go` の `Validation` 関数は、入札を受理してよい「既知の」オークション集合
(`known`)を `expectedInitialAuctions`(シード id<=10、`bench/validate.go:19` 付近)と
`s.Ledger.Listings()`(bench 自身が `POST /auctions` で作った出品、`bench/load.go:331` で登録)
の和集合だけで構成している。生成データの live オークション(id 13以降)はこの `known` に一切
含まれない。一方 `bench/load.go` の bidder は `GET /auctions` の応答からランダムに入札先を選ぶ
(`targetID := list[rand.Intn(len(list))].ID`)ため、生成 live オークションへの入札は日常的に
発生し、201で受理された時点で `s.Ledger` に記録される。Validation はこれを
`想定外のauctionに入札が受理された` の critical として検出する。

これはスケール固有の問題ではなく、生成データを積んだ状態で `bench` の Validation
フェーズ(台帳突合、コミット `e11300e`)を60秒走行させた時点で常に顕在化する。実際、
Load フェーズへ到達した計測は8回すべて(素の webapp 4回・`FOR UPDATE` 除去後2回・
復元後2回)で `想定外のauctionに入札が受理された` が複数件(2〜37件)発生しており、
一度も `RESULT: PASS` を観測できなかった。Phase 3 時点(`docs/phase3-notes.md` 参照)は
生成 live オークションが存在しなかったためこのギャップは表面化していなかったと考えられる。

この事実により、ゲート3(60秒走行 PASS)は現状の `bench` では small を含むどのスケールでも
達成できない可能性が高い。ゲート4の判定(単調増加検出)には影響しない(下記の実測ログの通り、
`想定外のauctionに入札が受理された` と単調増加系 critical は独立に発生・消滅している)ため、
本タスクの主目的(採用スケールの決定)には支障ないと判断したが、`bench/` 側の修正
(`known` にスナップショットの live auction ID を含める等)は別途フォローアップが必要。

### 実測ログ(抜粋)

**ゲート1(3回、`docker compose up -d --build` 直後)**

```
gate1 attempt 1: http=200 elapsed=0.34s notif_count=4000
gate1 attempt 2: http=200 elapsed=0.32s notif_count=4000
gate1 attempt 3: http=200 elapsed=0.34s notif_count=4000
```

**ゲート2(初期化直後に3連続試行、うち1回は最初の素朴な試行でハザードAにより非決定的FAIL。
3連続PASSを得るまでの実測をすべて記録)**

初回(素朴に「ゲート1の直後」に3連続実行。累積経過時間が seed auction 4 の +12秒を
超えFAIL、レギュレーション文書済みのハザード):
```
run 1: PREPARE: FAIL / real 0.71
run 2: PREPARE: PASS / real 1.59
run 3: PREPARE: FAIL / real 0.34
```

初期化直後に取り直した6回中の3連続PASS(ハザードAにより間に2回FAILを挟む):
```
attempt 3: PREPARE: PASS / real 1.55
attempt 4: PREPARE: PASS / real 1.59
attempt 5: PREPARE: PASS / real 1.50
```

**ゲート3(素のwebapp、Load フェーズへ到達した4回、すべてFAIL)**

```
SCORE: 7361  (raw 7363, penalty 2)   ERRORS: 2件 (critical: 2件)   RESULT: FAIL
SCORE: 6184  (raw 6209, penalty 25)  ERRORS: 25件 (critical: 25件) RESULT: FAIL
SCORE: 6655  (raw 6687, penalty 32)  ERRORS: 32件 (critical: 32件) RESULT: FAIL
SCORE: 6488  (raw 6517, penalty 29)  ERRORS: 29件 (critical: 29件) RESULT: FAIL
```
critical は全件 `想定外のauctionに入札が受理された (auction 10xx)` (ハザードB)。

**ゲート4(`FOR UPDATE` 除去、Load フェーズへ到達した回)**

なお、以下の3回の前にもう1回 Load フェーズへ到達した試行があったが、その回は
`tail -5` で末尾のみ確認し(`ERRORS: 34件 (critical: 34件)` / `RESULT: FAIL`)、
critical の内訳を記録していない。したがって単調増加系 critical が含まれていたかは
不明であり、以下の「1回目」「2回目」「3回目」は内訳まで確認できた3回を指す
(ブリーフの「3回まで」の範囲には収まっている)。

1回目・2回目は検出せず:
```
[1回目] ERRORS: 33件 (critical: 33件) — 全件 想定外のauctionに入札が受理された。単調増加違反なし
[2回目] ERRORS: 39件 (critical: 39件) — 想定外37件 + winner_id不一致1件 + outbid通知欠落1件。単調増加違反なし
```

3回目で検出:
```
ERR: load: critical: auction 1091: フィードの金額が単調増加でない (id=30013(amount=3598) の次に id=30015(amount=3578))
ERR: load: critical: auction 1091: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=30015(amount=3578) の直後に id=30013(amount=3598) が来ており単調減少でない)
ERR: validation: critical: auction 1091: bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=30015(amount=3578) の直後に id=30013(amount=3598) が来ており単調減少でない)
SCORE: 6397  (raw 6434, penalty 37)
ERRORS: 37件 (critical: 37件)
RESULT: FAIL
```

**復元確認(`git checkout webapp/go/bids.go` → rebuild、Load フェーズへ到達した2回)**

```
[1回目] SCORE: 6306 (raw 6336, penalty 30) / ERRORS: 30件 / 全件 想定外のauctionに入札が受理された。単調増加系critical 0件
[2回目] SCORE: 6044 (raw 6071, penalty 27) / ERRORS: 27件 / 全件 想定外のauctionに入札が受理された。単調増加系critical 0件
```

`git diff webapp/go/bids.go` は空(`FOR UPDATE` が復元されていることを確認)。
`RESULT` 自体は上記ハザードBにより両回とも `FAIL` のままだが、ゲート4が検出対象とする
単調増加系 critical は復元後は0件であり、`FOR UPDATE` の復元が機能していることは確認できた。
