# Phase 4-D 設計: SPA フロントエンドとベンチのアセット追従検証

**日付**: 2026-08-30
**前提**: Phase 3(pubsub/フィード/通知/クローザ)完了、Phase 4-A(初期データジェネレータ)完了
**位置づけ**: Phase 4 分解表の 4-D。依存は「なし(並行可)」だが、実際には 4-B(検索)が先に入っている前提で書く

## 1. 目的

3つある。

1. **問題を「APIの集合」から「サイト」にする**。参加者と運営が実際に触れる UI が無いと、
   オークションという題材の面白さ(残り時間、競り上がり、通知)が体験として成立しない。
2. **静的配信をアプリ経由にすることで、序盤の王道改善を成立させる**。
   `docs/phase2-notes.md` の仕込みインベントリに「nginx: 静的配信もアプリ経由」と
   既に記載してあるが、配信すべき静的ファイルが存在しないため仕込みとして機能していない。
   本フェーズで初めて実体を持つ。
3. **アセットを壊す最適化をベンチが落とせるようにする**。静的配信を速くする改善は歓迎するが、
   「中身の違うファイルを返す」「参照を消す」は正しさの違反である。

## 2. スコープ

**やる**: API の `/api` 配下への移行、`GET /api/me` の追加、アプリによる意図的に遅い静的配信、
React SPA(6画面)、ビルド成果物の `webapp/public/` へのコミット、アセットマニフェストの生成と
ベンチへの埋め込み、Prepare でのハッシュ照合、Load でのページロード取り込みと配点。

**やらない**: `GET /users/:id/icon`(4-C)、検索のサーバ側実装(4-B)、負荷の段階増加(4-E)、
レギュレーション/マニュアル文書(4-F)、SSR、フロントエンドの単体テスト(理由は §9)。

## 3. 検討した選択肢と決定

### 3.1 SPA と API のパス衝突をどう解くか(最重要)

現状の API は `GET /auctions/{id}` である。SPA の詳細画面 URL を `/auctions/123` にすると、
同じパスが JSON と HTML の両方を意味する。chi は API ハンドラを先にマッチさせるため、
ディープリンクが JSON を返す。

| 案 | 内容 | 評価 |
|---|---|---|
| **A. `/api` prefix(採用)** | 全 API を `/api` 配下へ移す | ISUCON11/12/13 がいずれもこの形。理由もまさにこの衝突。nginx の分担が `location /api/ → app` / それ以外は静的、と教科書的に割れる |
| B. ハッシュルーティング | SPA を `/#/auctions/123` で回す | API 変更ゼロ。ただし URL が古び、将来の SSR/OGP の余地を捨てる |
| C. Content-Negotiation | `Accept` ヘッダで HTML/JSON を出し分ける | 同一パスに2つの意味が残り、キャッシュとの相性も悪い。却下 |

**A を採用する。** 移行コストは実測で小さいことを確認済み: ベンチ側のパス文字列は
`bench/client.go` の10箇所に集約されており、残りは `webapp/go/*_test.go` の機械的置換である。
公開前の今が最も安いタイミングであり、外部の利用者はまだ存在しない。

### 3.2 フロントエンドのツールチェーン

**React 18 + Vite 5 + TypeScript を採用する。**ビルド成果物を `webapp/public/` にコミットし、
参加者は触らない(ISUCON13方式)。素の ES Modules 案も検討したが、生成されるアセットが数個に
とどまり、アセット追従検証と静的配信ボトルネックの両方が薄くなるため退けた。

ただし**依存は最小に保つ**。状態管理ライブラリを入れず、CSS フレームワークも入れない。
理由は好みではなく配点設計である: バンドルが肥大すると Load 中の静的配信コストが支配的になり、
「入札が主役」というスコア設計(`bench/score.go` の配点表)が壊れる。

### 3.3 ベンチのアセット検証の深さ

**Prepare でハッシュ照合、Load で追従取得を採用する。**取得のみ(ハッシュ無し)では
「空ファイルを返す」最適化を検出できず、Prepare のみでは静的配信の負荷が走行に乗らない。

## 4. API の `/api` 移行

移行後のエンドポイント:

```
POST /api/initialize
POST /api/register
POST /api/login
GET  /api/me                      (新規)
GET  /api/auctions
POST /api/auctions
GET  /api/auctions/:id
GET  /api/auctions/:id/bids
POST /api/auctions/:id/bids
GET  /api/notifications
GET  /api/stats/me
```

`webapp/go/main.go` の `routerFor` を `r.Route("/api", func(r chi.Router) { ... })` で束ねる。
ハンドラ本体は一切変更しない。

**影響範囲**(すべて機械的):
`webapp/go/main.go` / `webapp/go/*_test.go` / `bench/client.go`(10箇所) /
`dev/nginx.conf`(変更なしで動くが §6 参照) / `README.md`。
`docs/superpowers/specs/2026-07-08-isubid-design.md` の API 表は 4-F で正とする(本フェーズでは触らない)。

### 4.1 `GET /api/me`

SPA はリロード後に「自分が誰か」を知る必要があるが、セッションは httpOnly Cookie なので
JS から読めない。ログイン応答をクライアント側に保存する案は Cookie と保存値が乖離しうるため退ける。

```
GET /api/me
  200 {"id": 5, "name": "seed_user_05"}       ログイン中
  401 {"error": "login required"}             未ログイン
```

実装は `SELECT id, name FROM users WHERE id = ?` 1本。**ここに意図的な遅さは入れない。**
セッション確認は改善対象ではなく、遅くしても改善路が増えず、全画面の初期表示に効いてしまうため。

ベンチにとっては「セッションが生きていること」の検証点が1つ増える利得がある。

## 5. 静的配信(意図的に遅い実装)

`webapp/public/` に置いたビルド成果物を、アプリが自前で配信する。
新規ファイル `webapp/go/static.go`。ルーティングは `/api/*` 以外のすべて。

### 5.1 配信規則

1. URL パスを `path.Clean("/" + p)` で正規化する。正規化後も `webapp/public` の外を指す
   パスは 400 で拒否する(ディレクトリトラバーサル対策。コンテスト問題であっても
   参照実装に実在の脆弱性を残さない)
2. `webapp/public` 配下に実ファイルがあればそれを返す
3. 無い場合、パスが `/assets/` で始まる、または拡張子を持つ(`path.Ext(p) != ""`)なら **404**
4. それ以外(SPA のクライアントルート `/auctions/123` など)は `index.html` を **200** で返す

規則3が必要なのは、存在しないアセットに対して index.html を200で返してしまうと、
「壊れているのに壊れていないように見える」状態が生まれるためである。

### 5.2 仕込む遅さ

`static.go` の冒頭コメントに明示する(4-F でこのコメントを外す運用は
`docs/phase2-notes.md` の方針に従う)。

- リクエストごとに `os.ReadFile` でファイル全体をメモリに読む。
  `http.ServeContent` / `http.FileServer` を使わない
- `Cache-Control` / `ETag` / `Last-Modified` を一切付けない → 条件付き GET は常に 200 になる
- gzip / brotli を行わない
- Content-Type は拡張子から自前の小さな表で決める(`.html .js .css .svg .ico .json`)

**改善路**: nginx での直配信、キャッシュヘッダの付与、gzip の有効化。
ベンチは 304 も gzip も許容する(§7.2)ため、どれをやってもスコアが伸びる方向にしか働かない。

## 6. nginx

`dev/nginx.conf` は**変更しない**。`location / { proxy_pass http://app:8000; }` のまま、
`/api` も静的ファイルも全部アプリへ流す。これが仕込みそのものであり、
先回りして `location /api/` を分けると参加者の改善余地を潰す。

ただし `dev/compose.yaml` では `webapp/public` を **nginx コンテナにもマウントする**。
nginx がビルド成果物を持っていないと「静的配信を nginx へ移す」という改善路が
そもそも実行できず、仕込みが「遅い」ではなく「不可能」になってしまう。
**ファイルは見えるが設定が使っていない**、という状態が正しい初期状態である。

## 7. SPA

### 7.1 画面とルート

`webapp/frontend/`(React 18 + Vite 5 + TypeScript)。`react-router-dom` の `BrowserRouter`。

| ルート | 画面 | 使う API |
|---|---|---|
| `/` | 一覧(検索・カテゴリ絞り込み) | `GET /api/auctions` |
| `/auctions/:id` | 詳細(入札フォーム + 入札フィード) | `GET /api/auctions/:id`, `POST /api/auctions/:id/bids`, `GET /api/auctions/:id/bids?since=` |
| `/sell` | 出品 | `POST /api/auctions` |
| `/notifications` | 通知一覧 | `GET /api/notifications` |
| `/stats` | 売上ダッシュボード | `GET /api/stats/me` |
| `/login` | ログイン / 新規登録(タブ切替) | `POST /api/login`, `POST /api/register` |

- 起動時に `GET /api/me` を1回叩いて認証状態を解決する。401 ならゲスト
- 詳細画面の入札フィードは 1 秒間隔で `?since=<最後のbid_id>` をポーリングする。
  これは Phase 3 でベンチが前提にしている形と同じであり、参加者が pub/sub 化する終盤の的でもある
- API クライアントは `src/api.ts` に集約する(`fetch`, `credentials: "same-origin"`)
- 状態管理は `useState` / `useEffect` と認証用の Context 1つのみ。ライブラリを足さない
- CSS はプレーンな 1 枚(`src/styles.css`)

**バンドルサイズを設計制約にする**: `webapp/public/` 配下の合計(html + js + css + favicon、未圧縮)を
**300KB 未満**に保つ。React + ReactDOM の production build が約 140KB なので十分な余裕がある。
この上限は §8.2 のテストで機械的に守る。

### 7.2 4-B / 4-C との関係

- 一覧画面の検索・カテゴリ絞り込みは `?q=` / `?category=` を送る。**4-B がサーバ側に入っている前提**。
  入っていなければ UI は動くが結果は絞られない(壊れはしない)
- ユーザーアイコン(4-C)は `<img src="/api/users/:id/icon">` で参照する。
  `ProcessHTML` はこれを自動で辿るが、**内容が DB 由来で可変なのでマニフェストの照合対象にはしない**。
  4-C 側で「マニフェスト対象外パスの扱い」を決める必要がある(§11 の持ち越し)

## 8. アセットマニフェストとベンチ検証

### 8.1 マニフェストの生成と埋め込み

`bench/cmd/genmanifest`(Go)が `webapp/public` を歩き、`bench/assets/manifest.json` を出力する。

```json
{
  "generated_at": "2026-08-30T12:34:56Z",
  "files": [
    {"path": "/index.html", "sha256": "...", "size": 512, "content_type": "text/html; charset=utf-8"},
    {"path": "/assets/index-abc123.js", "sha256": "...", "size": 145678, "content_type": "text/javascript; charset=utf-8"},
    {"path": "/assets/index-def456.css", "sha256": "...", "size": 8123, "content_type": "text/css; charset=utf-8"},
    {"path": "/favicon.svg", "sha256": "...", "size": 412, "content_type": "image/svg+xml"}
  ]
}
```

`//go:embed assets/manifest.json` でベンチバイナリに埋め込む。
**4-A のスナップショット(`-snapshot` フラグでファイルを渡す)と方式を変える**のは意図的である:
スナップショットは初期データの規模で変わるためファイル渡しが適切だが、マニフェストは
「参加者が差し替えられないこと」自体が検証の前提なので、埋め込みでなければ意味がない。

### 8.2 ドリフト検知

`bench/assets_test.go` の `TestManifestMatchesPublicDir` が `../webapp/public` を歩いて
埋め込みマニフェストと突合する。ディレクトリが無ければ `t.Skip`(ベンチ単体で配布された場合)。
同じテストで §7.1 のバンドル上限 300KB も検証する。

これが無いと「フロントエンドを再ビルドしたがマニフェストを更新し忘れた」状態が
Prepare の critical として初めて露見することになり、原因が分かりにくい。

### 8.3 Prepare での検証(`bench/assets.go`)

1. `GET /` が 200、`Content-Type` が `text/html`、本文の sha256 がマニフェストの `/index.html` と一致
2. `agent.ProcessHTML` で辿れたリソース集合が、マニフェストの `/index.html` **以外の全アセットを
   覆っている**こと(HTML から `<script>` や `<link>` が消えていないこと)。
   `/index.html` は手順1で直接取得済みなので対象外
3. 各リソースが 200 または **304**、デコード後バイト列の sha256 がマニフェストと一致
4. マニフェストに無いパスが返ってきても無視する(参加者が独自にアセットを足すのは自由)
5. Content-Type の検証は限定する。`/index.html` は `text/html` で始まること。
   それ以外のアセットは**「`text/html` でないこと」だけを検証する**

規則5をこの形にする理由: `application/javascript` と `text/javascript` のように、
nginx の既定 mime 表とアプリの自前表で正当に食い違う値がある。完全一致を要求すると
正しい改善(nginx 直配信)が false-FAIL する。一方で「アセットが `text/html` で返る」は
§5.1 の規則3が壊れて SPA フォールバックがアセット要求を飲み込んだ状態そのものであり、
実際にページが壊れる。マニフェストの `content_type` は文書としての記録であって
照合基準ではない。

いずれかが破れたら `ErrCritical` で即 FAIL。

**304 と gzip を許容できる根拠は実物で確認済みである。** isucandar の `agent.Do` は
`newCache` の中で 304 応答の `res.Body` をキャッシュ済みボディに差し戻す
(`agent/cache.go`)。また `decompress` が `Content-Encoding: gzip/br/deflate` を透過的に
解凍する(`agent/decompress.go`)。したがって `io.ReadAll(res.Body)` は 200/304・
圧縮の有無によらず常にデコード後の実バイト列を返し、ハッシュ照合は一様に書ける。

### 8.4 Load での取り込みと配点

各仮想ユーザーは**シナリオ開始時に1回だけ**「ページロード」を行う:
`GET /` → `ProcessHTML` → 全リソース取得 → ハッシュ照合。以降のイテレーションでは行わない。
実ブラウザの初回訪問と同じ形である。

Agent は自前の `CacheStore` を持つため、参加者がキャッシュヘッダを付ければ
2回目以降は条件付き GET になり 304 が返る。

配点: **`ScoreGETPage` をページロード1回につき 1 点**。アセット1本ごとには加点しない。

> **この配点は意図的である。** アセット単位で加点すると、キャッシュを効かせて取得回数を
> 減らすという正しい最適化がスコアを下げてしまう。ページロード単位なら、静的配信が速いほど
> 1イテレーションが短くなり他のスコアが伸びる、という正しい向きだけが残る。
> Phase 3 で `ScoreGETFeed` をポーリング1回ごとに加点し「遅いフィードほど高得点」に
> なりかけた失敗と同型なので、ここで先に潰しておく。

エラー分類:
- アセット取得の 5xx / 接続エラー → `ErrApplication`(減点)
- ハッシュ不一致 / 参照の欠落 → `ErrCritical`(Load 中でも FAIL)

## 9. テスト

- `webapp/go/static_test.go`: 正常配信、SPA フォールバック、`/assets/` 配下の404、
  拡張子付きパスの404、パストラバーサル拒否、Content-Type の決定
- `webapp/go/me_test.go`: ログイン時 200、未ログイン 401
- `bench/assets_test.go`: マニフェストのドリフト検知、バンドル上限
- **フロントエンドの単体テストは置かない。** 成果物はコミット済みの固定物であり、
  ベンチが index.html とアセットのバイト一致を検証する。画面の動作確認は人間が行う。
  JS のテストランナーをもう1つ保守するコストに見合わない

## 10. 受け入れゲート(4-A と同じ思想: 検出できることを実証する)

| ゲート | 内容 | 期待 |
|---|---|---|
| G1 | 通常の 60 秒走行 | `RESULT: PASS`、critical 0 件 |
| G2 | `webapp/public/assets/*.js` を1バイト書き換える | Prepare が critical で FAIL |
| G3 | `index.html` から `<script>` タグを消す | critical で FAIL |
| G4 | nginx に `location /assets/ { root ...; gzip on; expires 1h; }` を足す | PASS のまま、**スコアが上がる** |

G4 の「上がる」は測定ノイズと区別できなければ意味がない。このマシンは同一セッション内でも
スコアが約 7〜12% 振れることが Phase 3 で分かっている。したがって判定は
**変更前 3 回・変更後 3 回を走らせ、中央値の改善が変更前 3 回の最大値と最小値の幅を
上回ること**とし、6 回分の生の数値をすべて記録する。上回らなかった場合は「改善が
ノイズに埋もれた」と正直に記録し、静的配信の重みが足りていない可能性として 4-E へ送る。

G4 が本フェーズで最も重要である。仕込んだボトルネックに対して意図した改善路が実際に効き、
かつベンチがそれを正しく報酬として返すことの実証であり、これが取れなければ
「静的配信をアプリ経由にする」仕込みは出題として成立していない。

## 11. リポジトリ構成

```
webapp/
  frontend/                     # SPA ソース
    package.json  vite.config.ts  tsconfig.json  index.html
    src/{main.tsx,api.ts,auth.tsx,styles.css,pages/*.tsx}
  public/                       # ビルド成果物(コミットする)
    index.html
    assets/index-<hash>.js
    assets/index-<hash>.css
    favicon.svg
  go/
    static.go  static_test.go
    me.go      me_test.go
bench/
  assets.go  assets_test.go
  assets/manifest.json          # go:embed される
  cmd/genmanifest/main.go
```

## 12. 実装順序(計画の下敷き)

1. `/api` 移行(webapp ルータ + テスト + `bench/client.go` + README)
2. `GET /api/me`
3. `webapp/go/static.go`(意図的に遅い配信 + SPA フォールバック)+ テスト
4. SPA スケルトン(Vite セットアップ、`api.ts`、ルーティング、ログイン画面)
5. 残り 5 画面
6. ビルド → `webapp/public/` をコミット、`cmd/genmanifest` + マニフェスト + ドリフトテスト
7. `bench/assets.go`(Prepare のハッシュ照合)
8. Load へのページロード組み込みと `ScoreGETPage`
9. 通し走行と G1〜G4 の実証

## 13. 持ち越し

- **4-C(アイコン BLOB)**: `<img src="/api/users/:id/icon">` は `ProcessHTML` が自動で辿る。
  内容が可変なのでマニフェスト照合の対象外とする扱いを 4-C で決める。
  「マニフェストに無いパスは無視」(§8.3 の規則4)がそのまま効くので、原則追加作業は不要のはず
- **4-E(負荷の段階増加)**: ページロードが1仮想ユーザーにつき1回入るため、
  並列数を上げたときの静的配信の負荷が線形に増える。段階増加の刻みを決める際の入力になる
- **4-F(文書)**: `/api` 移行に伴い `2026-07-08-isubid-design.md` の API 表が古くなる。
  レギュレーション文書を書く際に正とする。`static.go` の「意図的に遅い実装」コメントの
  扱いも同文書の方針に従う
