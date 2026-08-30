package main

import (
	"fmt"
	"strings"
	"time"
)

// expectedAuction は webapp/sql/90_seed_phase1.sql と
// webapp/go/initialize.go の auctionEndOffsets に一致させること(あちらが正)。
type expectedAuction struct {
	Title        string
	Description  string
	CurrentPrice int64
	BidCount     int64
	SellerID     int64
	CategoryID   int64
	EndsAtOffset int // ends_at = initialize時刻 + このオフセット(秒)
}

var expectedInitialAuctions = map[int64]expectedAuction{
	1:  {"ヘリテージ・ウィングチェア", "英国アンティークの本革ウィングチェア", 1500, 3, 1, 3, 3600},
	2:  {"エルゴホスト Model E", "長時間作業向けエルゴノミクスチェア", 2100, 1, 2, 1, 20},
	3:  {"ISUレーサー GT", "フルバケット型ゲーミングチェア", 3100, 1, 3, 2, 3660},
	4:  {"メッシュフロー 40", "通気性メッシュのタスクチェア", 4100, 1, 4, 1, 12},
	5:  {"ミッドセンチュリー・ラウンジ", "1960年代のラウンジチェア", 2500, 0, 5, 3, 3720},
	6:  {"ネオンストライク Z", "RGBライト内蔵ゲーミングチェア", 3000, 0, 6, 2, 36},
	7:  {"スタンドフレックス", "昇降デスク対応ハイチェア", 3500, 0, 7, 1, 3780},
	8:  {"チャーチチェア 1920", "教会で使われていた木製チェア", 4000, 0, 8, 3, 28},
	9:  {"プロシート・エディション", "eスポーツチーム監修モデル", 4500, 0, 9, 2, 3840},
	10: {"コンパクトワーク 01", "省スペース設計のワークチェア", 5000, 0, 10, 1, 44},
}

// initialAuctionOrder は ends_at 昇順に並べた期待 id 列。id 昇順と一致しないことが重要
// (一致していると ORDER BY id ASC への書き換えを検出できない)。
var initialAuctionOrder = []int64{4, 2, 8, 6, 10, 1, 3, 5, 7, 9}

// endsAtTolerance は ends_at 照合の許容幅。ベンチが initialize の応答を受け取った時刻を
// base とするが、アプリが基準時刻を採ったのはその少し前なので、初期化処理の所要時間ぶんの
// ずれを吸収する(Phase 2b-1 時点の Prepare 実測は 0.6〜1.2 秒)。
const endsAtTolerance = 5 * time.Second

type expectedBid struct {
	Amount   int64
	UserID   int64
	UserName string
}

// auction 1 の初期入札列(created_at DESC順)。90_seed_phase1.sql が正。
var expectedAuction1Bids = []expectedBid{
	{1500, 4, "seed_user_04"},
	{1200, 3, "seed_user_03"},
	{1000, 2, "seed_user_02"},
}

func pad2(n int64) string {
	return fmt.Sprintf("%02d", n)
}

// auctionsPerPage は参照実装の1ページ件数。
// webapp/go/auctions.go の同名定数と手で揃えること(モジュールが別なので
// コンパイル時に照合する手段が無い)。
const auctionsPerPage = 20

// ValidatePagedListShape は一覧レスポンス1件だけで完結する不変条件を検証する。
//
// Load 中は終了処理バッチが live を減らし、出品ワーカーが増やすため、オフセット
// ページネーションのページ境界は足元で動く。したがって複数レスポンスにまたがる
// 検査(ページ間で id が重複しない・全ページの和が total_count と一致する 等)は
// ここでは決して行わない。それらは静穏期である Prepare の仕事。
//
// page は実際に要求したページ番号を渡す契約だが、page 未指定のリクエスト
// (AuctionListParams{} など、AuctionListParams.Page のゼロ値である 0 のまま
// 送信されるケース)を呼び出し側がそのまま渡すことがある。has_next の期待値計算は
// ページ番号に依存するため、正規化しないと page 未指定の呼び出しで total_count>0 の
// 限り必ず不一致になる(正しいアプリを false-FAIL させる)。
func ValidatePagedListShape(page int, l *AuctionList) error {
	if page <= 0 {
		page = 1
	}
	if l.Auctions == nil {
		return fmt.Errorf("GET /auctions?page=%d: auctions が null (期待: 空でも [])", page)
	}
	if len(l.Auctions) > auctionsPerPage {
		return fmt.Errorf("GET /auctions?page=%d: %d件 (期待: %d件以下)",
			page, len(l.Auctions), auctionsPerPage)
	}
	if l.TotalCount < int64(len(l.Auctions)) {
		return fmt.Errorf("GET /auctions?page=%d: total_count %d が返却件数 %d を下回る",
			page, l.TotalCount, len(l.Auctions))
	}
	if want := int64(page)*auctionsPerPage < l.TotalCount; l.HasNext != want {
		return fmt.Errorf("GET /auctions?page=%d: has_next が %v (期待: %v, total_count=%d)",
			page, l.HasNext, want, l.TotalCount)
	}
	for i := 1; i < len(l.Auctions); i++ {
		if l.Auctions[i].EndsAt.Before(l.Auctions[i-1].EndsAt) {
			return fmt.Errorf("GET /auctions?page=%d: ends_at が昇順でない (index %d: id=%d %v の前が id=%d %v)",
				page, i, l.Auctions[i].ID, l.Auctions[i].EndsAt,
				l.Auctions[i-1].ID, l.Auctions[i-1].EndsAt)
		}
	}
	for _, a := range l.Auctions {
		if a.Status != "live" {
			return fmt.Errorf("GET /auctions?page=%d: live以外が混入 (id=%d status=%q)",
				page, a.ID, a.Status)
		}
	}
	return nil
}

func ValidateInitialAuctionList(list []AuctionSummary, base time.Time) error {
	if len(list) != len(expectedInitialAuctions) {
		return fmt.Errorf("GET /auctions: 件数が %d (期待: %d)", len(list), len(expectedInitialAuctions))
	}
	var prevEndsAt time.Time
	for i, a := range list {
		if a.ID != initialAuctionOrder[i] {
			return fmt.Errorf("GET /auctions: %d番目が id=%d (期待: id=%d / ends_at ASC順)", i, a.ID, initialAuctionOrder[i])
		}
		if a.EndsAt.Before(prevEndsAt) {
			return fmt.Errorf("GET /auctions: ends_at が昇順でない (id=%d)", a.ID)
		}
		prevEndsAt = a.EndsAt
		want := expectedInitialAuctions[a.ID]
		wantEndsAt := base.Add(time.Duration(want.EndsAtOffset) * time.Second)
		if d := a.EndsAt.Sub(wantEndsAt); d > endsAtTolerance || d < -endsAtTolerance {
			return fmt.Errorf("auction %d: ends_at が %v (期待: %v ± %v)", a.ID, a.EndsAt, wantEndsAt, endsAtTolerance)
		}
		if a.Status != "live" {
			return fmt.Errorf("auction %d: status が %q (期待: live)", a.ID, a.Status)
		}
		if a.Title != want.Title {
			return fmt.Errorf("auction %d: title が %q (期待: %q)", a.ID, a.Title, want.Title)
		}
		if a.CurrentPrice != want.CurrentPrice {
			return fmt.Errorf("auction %d: current_price が %d (期待: %d)", a.ID, a.CurrentPrice, want.CurrentPrice)
		}
		if a.BidCount != want.BidCount {
			return fmt.Errorf("auction %d: bid_count が %d (期待: %d)", a.ID, a.BidCount, want.BidCount)
		}
		if a.CategoryID != want.CategoryID {
			return fmt.Errorf("auction %d: category_id が %d (期待: %d)", a.ID, a.CategoryID, want.CategoryID)
		}
		if a.Seller.ID != want.SellerID || a.Seller.Name != "seed_user_"+pad2(want.SellerID) {
			return fmt.Errorf("auction %d: seller が %+v (期待: id=%d)", a.ID, a.Seller, want.SellerID)
		}
	}
	return nil
}

// 検索検証に使うプローブ語。TestSearchProbesAreClassified が分類を固定する。
//
// Prepare は3つ全てを使う(スナップショットから description を知っているので
// 期待集合を計算できる)。Load は probeTitleOnly だけを使う ——
// 一覧レスポンスの AuctionSummary に description が無いため、description で
// 一致した行を Load 側は検証しようがなく、正しい実装を誤判定してしまう。
//
// プローブは必ず「語中」に出現する語を選ぶこと。initial-data の生成タイトルは
// chairNames の要素 + " " + 連番、description は chairDescs の要素そのものなので、
// chairNames/chairDescs の先頭語(例: "エルゴフロー" や "職人")を選ぶと
// 常に文字列の先頭で一致してしまい、LIKE '%q%' を LIKE 'q%'(前方一致)に
// 書き換える改悪が同じ集合を返して素通りする。
// "ワークス" は "メッシュワークス NNNNN" の途中、"手作業" は
// "職人による手作業の仕上げ" の途中にしか現れないため、前方一致に落とすと
// どちらも0件になり確実に検出できる。
const (
	probeTitleOnly       = "ワークス"
	probeDescriptionOnly = "手作業"
	probeNoMatch         = "ズンドコベロンチョ"
)

// expectedLiveMatches は初期データのうち probe と categoryID に合致する live
// オークションの期待集合を返す。値はそのオークションの ends_at で、照合側が
// 期限切れを許容できるようにするために持たせる。
// probe が空なら語での絞り込み無し、categoryID が 0 ならカテゴリ絞り込み無し
// (両方の絞り込みが無い場合は「live 一覧全件」の期待集合になる)。
func expectedLiveMatches(probe string, categoryID int64, snap *Snapshot, base time.Time) map[int64]time.Time {
	want := map[int64]time.Time{}
	add := func(id int64, title, description string, cat int64, offset int) {
		if probe != "" && !strings.Contains(title, probe) && !strings.Contains(description, probe) {
			return
		}
		if categoryID != 0 && cat != categoryID {
			return
		}
		want[id] = base.Add(time.Duration(offset) * time.Second)
	}
	for id, e := range expectedInitialAuctions {
		add(id, e.Title, e.Description, e.CategoryID, e.EndsAtOffset)
	}
	if snap != nil {
		for i := range snap.Auctions {
			sa := &snap.Auctions[i]
			if sa.Status != "live" {
				continue
			}
			add(sa.ID, sa.Title, sa.Description, sa.CategoryID, sa.EndsAtOffset)
		}
	}
	return want
}

// ValidateSearchResult は一覧・検索・絞り込みの「全ページ走査で連結した結果」を
// 期待集合と照合する。Prepare 専用(Load 中は出品ワーカーが期待集合に無い
// オークションを増やすため成立しない)。
//
// E = 走査開始時点の期待集合(want)、D = 走査中に ends_at が到来した(かもしれない)
// E の部分集合、R = 実際に返ってきた行の集合として、次の3つだけを検査する。
// D の判定には endsAtTolerance を効かせる(下の期限判定のコメント参照)。
//
//	R ⊆ E                          期待集合に無い id が返ってきたら常に異常
//	E \ D ⊆ R                      期限がまだ来ていないものが欠けていたら異常
//	|E| - |D| ≤ total_count ≤ |E|
//
// len(R) == total_count の厳密一致は意図的に課さない。全ページを走査している
// 途中で先頭側のオークションの期限が到来し、終了処理バッチがそれを closed に
// すると、page 1 で既に返された行は最終ページ取得時点の total_count には
// 含まれない。すなわち len(R) > total_count が正しいアプリでも起きる
// (生成 live の最短期限は初期化から +15秒、シード auction 4 は +12秒で、
// ページ走査ぶんリクエスト数の増えた Prepare はこの窓に近い)。
//
// 検出力は落ちない。「total_count を len(auctions) で返す」改悪は page 1 で
// has_next=false になって走査が20件で止まるため、total_count が期限未到来の
// 期待件数を下回って捕まる。「LIMIT を無視して全件返す」改悪は
// ValidatePagedListShape の1ページ20件上限で捕まる。
//
// now は全ページを取り終えた後の時刻を渡すこと。取得前の時刻を渡すと
// 「取得中に期限が来た」ケースを許容できず false-FAIL になる。
func ValidateSearchResult(label string, got []AuctionSummary, totalCount int64,
	want map[int64]time.Time, now time.Time) error {

	gotIDs := make(map[int64]bool, len(got))
	for _, a := range got {
		if gotIDs[a.ID] {
			return fmt.Errorf("%s: id=%d が重複している(全ページを通して同じ id が2回返った)", label, a.ID)
		}
		gotIDs[a.ID] = true
		if _, ok := want[a.ID]; !ok {
			return fmt.Errorf("%s: 期待集合に無い auction %d (title=%q) が返った", label, a.ID, a.Title)
		}
		if a.Status != "live" {
			return fmt.Errorf("%s: auction %d の status が %q (期待: live)", label, a.ID, a.Status)
		}
	}

	var stillLive int64
	for id, endsAt := range want {
		// 期限判定にも endsAtTolerance を効かせる。base はアプリが基準時刻を採った後に
		// 採られるため、実際の ends_at は base+offset より最大 endsAtTolerance だけ手前に
		// なりうる。ここをゼロ許容にすると、正しいアプリが初期化所要時間ぶんだけ早く
		// closed にしたオークションに対して、まだ存在を要求してしまう。
		if !endsAt.Add(-endsAtTolerance).After(now) {
			continue // 期限到来済みかもしれない。欠けていてよい
		}
		stillLive++
		if !gotIDs[id] {
			return fmt.Errorf("%s: 期限前(%v)の auction %d が結果に含まれていない", label, endsAt, id)
		}
	}

	if totalCount > int64(len(want)) {
		return fmt.Errorf("%s: total_count が %d (期待: %d以下、期待集合の件数)", label, totalCount, len(want))
	}
	if totalCount < stillLive {
		return fmt.Errorf("%s: total_count が %d (期待: %d以上、期限未到来の期待件数)", label, totalCount, stillLive)
	}
	return nil
}

// ValidateAuctionListWithSnapshot は生成データ搭載時の一覧検証。
//
// Prepare 専用。Load からは呼んではならない: 全ページ分の連結済み列を要求する
// 時点で複数レスポンスにまたがる検査であり、しかも期待集合を初期データだけから
// 組み立てる。Load 中は出品ワーカーが初期データに無いオークションを増やすため、
// この関数が要求する「返ってきた id は全て期待集合に含まれる」は成立しない
// (正しいアプリを false-FAIL させる)。Load から呼べる単一レスポンス内不変条件は
// ValidatePagedListShape を使うこと。
//
// Phase 3 の ValidateInitialAuctionList は期待 id 列(initialAuctionOrder)との
// 完全一致で照合していたが、生成データが入ると live は合計60件(シード10 +
// 採用スケール small の生成50)になり、シードと生成分が ends_at 順で交互に並ぶ。
// 完全一致は同着やミリ秒単位のズレで壊れるため、次の性質に置き換える。
//
//	ends_at が非減少であること
//
// 一覧の ORDER BY ends_at ASC を ORDER BY id ASC に書き換える改変は、この
// 非減少性のチェックだけで検出できる。シードの ends_at 順(initialAuctionOrder =
// 4,2,8,6,10,1,3,5,7,9)と生成データの ends_at 順は、どちらも意図的に id 順と
// 相関しないよう作られているため(生成側は initial-data/generate_test.go の
// TestGeneratedLiveEndsAtNotCorrelatedWithID がそれを保証する)、ends_at 昇順の
// 一覧が同時に id 昇順にもなることはない。したがって id 昇順への書き換えは必ず
// このチェックに引っかかる。id 昇順である/でないを別の性質として直接
// 検査する必要はなく、むしろ id 相関の前提が崩れた場合に正しい一覧を誤検出
// しかねないため、あえて入れていない。
//
// 加えて、全ページ走査で連結した列に対して集合としての照合も行う。これは
// 絞り込み無し(probe 空・category 0)の期待集合を作って ValidateSearchResult に
// 委ねる —— すなわち「返ってきた id は全て期待集合に含まれる」「期限未到来の
// 期待要素は全て返ってきている」「total_count が期限未到来件数以上・期待集合の
// 件数以下」の3点で、id のページ跨ぎ重複もここで検出される。
//
// 「全ページ合計 == 期待件数」「total_count == 全ページ合計」の厳密一致は
// 意図的に課していない。走査の途中で先頭側のオークションの期限が到来して
// closed になると、正しいアプリでも両方が破れるため(理由の詳細は
// ValidateSearchResult のコメント参照)。
//
// now は全ページを取り終えた後の時刻を渡すこと。
func ValidateAuctionListWithSnapshot(all []AuctionSummary, totalCount int64, snap *Snapshot, base, now time.Time) error {
	want := expectedLiveMatches("", 0, snap, base)
	// スナップショット自身の整合性チェック。counts.live_auctions と auctions 配列の
	// live 件数が食い違うと期待集合が過小になり、正しいアプリを落としてしまう。
	// これはアプリではなくベンチ側データの不具合なので、そうと分かる文言にする。
	if wantCount := int64(len(expectedInitialAuctions)) + snap.Counts.LiveAuctions; int64(len(want)) != wantCount {
		return fmt.Errorf("スナップショットが不整合: 期待集合が %d件だが counts から導くと %d件 "+
			"(= シード %d + 生成 live %d)。auctions 配列に live が全て載っていない可能性がある",
			len(want), wantCount, len(expectedInitialAuctions), snap.Counts.LiveAuctions)
	}
	if err := ValidateSearchResult("GET /auctions", all, totalCount, want, now); err != nil {
		return err
	}

	// ends_at が非減少
	for i := 1; i < len(all); i++ {
		if all[i].EndsAt.Before(all[i-1].EndsAt) {
			return fmt.Errorf("GET /auctions: ends_at が昇順でない (index %d: id=%d %v の前が id=%d %v)",
				i, all[i].ID, all[i].EndsAt, all[i-1].ID, all[i-1].EndsAt)
		}
	}

	// 各行の中身を、シードは既存の期待値表、生成分はスナップショットと照合する
	// (status が live であることは上の ValidateSearchResult が既に検査している)
	for _, a := range all {
		if a.ID <= seedMaxAuctionID {
			w, ok := expectedInitialAuctions[a.ID]
			if !ok {
				return fmt.Errorf("GET /auctions: 想定外のシードauction %d が live 一覧にいる", a.ID)
			}
			if err := checkListRow(a, w.Title, w.CategoryID, w.SellerID,
				"seed_user_"+pad2(w.SellerID), w.CurrentPrice, w.BidCount,
				base.Add(time.Duration(w.EndsAtOffset)*time.Second)); err != nil {
				return err
			}
			continue
		}
		sa, ok := snap.ByID(a.ID)
		if !ok {
			return fmt.Errorf("GET /auctions: スナップショットに無い auction %d が live 一覧にいる", a.ID)
		}
		if err := checkListRow(a, sa.Title, sa.CategoryID, sa.SellerID, sa.SellerName,
			sa.CurrentPrice, sa.BidCount,
			base.Add(time.Duration(sa.EndsAtOffset)*time.Second)); err != nil {
			return err
		}
	}
	return nil
}

// seedMaxAuctionID は webapp/sql/90_seed_phase1.sql が占める auction id の上端。
// webapp/go/initialize.go の同名定数と揃えること。
const seedMaxAuctionID = 12

// seedMaxBidID は webapp/sql/90_seed_phase1.sql が占める bid id の上端。
// initial-data/config.go の SeedMaxBidID と揃えること。reconcileAuction の
// preexistingMaxBidID として、シードauctionとベンチ出品のlistingの両方に渡す
// (どちらも走行開始前の入札はシードの8件しか持ち得ない)。生成auctionでは
// seedMaxBidID + snapshot.Counts.Bids を渡す(bench/scenario.go Validation参照)。
const seedMaxBidID = 8

func checkListRow(a AuctionSummary, title string, categoryID, sellerID int64,
	sellerName string, currentPrice, bidCount int64, wantEndsAt time.Time) error {
	if a.Title != title {
		return fmt.Errorf("auction %d: title が %q (期待: %q)", a.ID, a.Title, title)
	}
	if a.CategoryID != categoryID {
		return fmt.Errorf("auction %d: category_id が %d (期待: %d)", a.ID, a.CategoryID, categoryID)
	}
	if a.Seller.ID != sellerID || a.Seller.Name != sellerName {
		return fmt.Errorf("auction %d: seller が %+v (期待: id=%d name=%q)", a.ID, a.Seller, sellerID, sellerName)
	}
	if a.CurrentPrice != currentPrice {
		return fmt.Errorf("auction %d: current_price が %d (期待: %d)", a.ID, a.CurrentPrice, currentPrice)
	}
	if a.BidCount != bidCount {
		return fmt.Errorf("auction %d: bid_count が %d (期待: %d)", a.ID, a.BidCount, bidCount)
	}
	if d := a.EndsAt.Sub(wantEndsAt); d > endsAtTolerance || d < -endsAtTolerance {
		return fmt.Errorf("auction %d: ends_at が %v (期待: %v ± %v)", a.ID, a.EndsAt, wantEndsAt, endsAtTolerance)
	}
	return nil
}

// ValidateSnapshotAuctionDetail は代表サンプルの詳細をスナップショットと照合する。
//
// スナップショットには closed オークションのサンプルも含まれるが、closed は
// live 一覧に現れないため、この関数がそれらを検証する唯一の場所になる。
// category_id・seller・ends_at を落とすと closed の誤りが一切検出できなく
// なるため、ここで必ず照合する(ends_at は closed だと offset=0 で意味を
// 持たないため live/upcoming のみ)。
func ValidateSnapshotAuctionDetail(d *AuctionDetail, sa *SnapshotAuction, base time.Time) error {
	if d.ID != sa.ID {
		return fmt.Errorf("auction detail: id が %d (期待: %d)", d.ID, sa.ID)
	}
	if d.Title != sa.Title {
		return fmt.Errorf("auction %d: title が %q (期待: %q)", d.ID, d.Title, sa.Title)
	}
	if d.Status != sa.Status {
		return fmt.Errorf("auction %d: status が %q (期待: %q)", d.ID, d.Status, sa.Status)
	}
	if d.CategoryID != sa.CategoryID {
		return fmt.Errorf("auction %d: category_id が %d (期待: %d)", d.ID, d.CategoryID, sa.CategoryID)
	}
	if d.Seller.ID != sa.SellerID || d.Seller.Name != sa.SellerName {
		return fmt.Errorf("auction %d: seller が %+v (期待: id=%d name=%q)", d.ID, d.Seller, sa.SellerID, sa.SellerName)
	}
	if d.StartingPrice != sa.StartingPrice {
		return fmt.Errorf("auction %d: starting_price が %d (期待: %d)", d.ID, d.StartingPrice, sa.StartingPrice)
	}
	if d.CurrentPrice != sa.CurrentPrice {
		return fmt.Errorf("auction %d: current_price が %d (期待: %d)", d.ID, d.CurrentPrice, sa.CurrentPrice)
	}
	if d.BidCount != sa.BidCount {
		return fmt.Errorf("auction %d: bid_count が %d (期待: %d)", d.ID, d.BidCount, sa.BidCount)
	}
	if int64(len(d.Bids)) != sa.BidCount {
		return fmt.Errorf("auction %d: bids が %d件 (期待: %d件)", d.ID, len(d.Bids), sa.BidCount)
	}
	if sa.Status == "live" || sa.Status == "upcoming" {
		wantEndsAt := base.Add(time.Duration(sa.EndsAtOffset) * time.Second)
		if diff := d.EndsAt.Sub(wantEndsAt); diff > endsAtTolerance || diff < -endsAtTolerance {
			return fmt.Errorf("auction %d: ends_at が %v (期待: %v ± %v)", d.ID, d.EndsAt, wantEndsAt, endsAtTolerance)
		}
	}
	if sa.Status == "closed" {
		if (d.WinnerID == nil) != (sa.WinnerID == nil) {
			return fmt.Errorf("auction %d: winner_id が %v (期待: %v)", d.ID, d.WinnerID, sa.WinnerID)
		}
		if d.WinnerID != nil && *d.WinnerID != *sa.WinnerID {
			return fmt.Errorf("auction %d: winner_id が %d (期待: %d)", d.ID, *d.WinnerID, *sa.WinnerID)
		}
		if (d.WinningPrice == nil) != (sa.WinningPrice == nil) {
			return fmt.Errorf("auction %d: winning_price が %v (期待: %v)", d.ID, d.WinningPrice, sa.WinningPrice)
		}
		if d.WinningPrice != nil && sa.WinningPrice != nil && *d.WinningPrice != *sa.WinningPrice {
			return fmt.Errorf("auction %d: winning_price が %d (期待: %d)", d.ID, *d.WinningPrice, *sa.WinningPrice)
		}
	}
	return ValidateBidsInvariant(d.Bids)
}

// ValidateInitialAuctionDetail は初期状態の auction 1 詳細を照合する(入札で汚す前に呼ぶこと)。
func ValidateInitialAuctionDetail(d *AuctionDetail) error {
	if d.ID != 1 {
		return fmt.Errorf("auction detail: id が %d (期待: 1)", d.ID)
	}
	if d.Title != "ヘリテージ・ウィングチェア" {
		return fmt.Errorf("auction 1: title が %q", d.Title)
	}
	if d.Status != "live" {
		return fmt.Errorf("auction 1: status が %q (期待: live)", d.Status)
	}
	if d.CategoryID != 3 {
		return fmt.Errorf("auction 1: category_id が %d (期待: 3)", d.CategoryID)
	}
	if d.BidCount != int64(len(expectedAuction1Bids)) {
		return fmt.Errorf("auction 1: bid_count が %d (期待: %d)", d.BidCount, len(expectedAuction1Bids))
	}
	if d.Description != "英国アンティークの本革ウィングチェア" {
		return fmt.Errorf("auction 1: description が %q", d.Description)
	}
	if d.StartingPrice != 1000 {
		return fmt.Errorf("auction 1: starting_price が %d (期待: 1000)", d.StartingPrice)
	}
	if d.CurrentPrice != 1500 {
		return fmt.Errorf("auction 1: current_price が %d (期待: 1500)", d.CurrentPrice)
	}
	if len(d.Bids) != len(expectedAuction1Bids) {
		return fmt.Errorf("auction 1: bids が %d件 (期待: %d件)", len(d.Bids), len(expectedAuction1Bids))
	}
	for i, want := range expectedAuction1Bids {
		b := d.Bids[i]
		if b.Amount != want.Amount || b.User.ID != want.UserID || b.User.Name != want.UserName {
			return fmt.Errorf("auction 1: bids[%d] が amount=%d user=%d/%q (期待: %d/%d/%q)",
				i, b.Amount, b.User.ID, b.User.Name, want.Amount, want.UserID, want.UserName)
		}
	}
	return ValidateBidsInvariant(d.Bids)
}

// ValidateBidsOrdered は入札列が created_at DESC, id DESC で並んでいることを検証する。
func ValidateBidsOrdered(bids []Bid) error {
	for i := 1; i < len(bids); i++ {
		prev, cur := bids[i-1], bids[i]
		if cur.CreatedAt.After(prev.CreatedAt) ||
			(cur.CreatedAt.Equal(prev.CreatedAt) && cur.ID > prev.ID) {
			return fmt.Errorf("bids の順序が created_at DESC, id DESC でない (index %d: id=%d)", i, cur.ID)
		}
	}
	return nil
}

// ValidateBidAmountsMonotonic は入札列(created_at DESC, id DESC順)の金額が
// 厳密単調減少であることを検証する。
//
// bid APIはオークション行をFOR UPDATEでロックしたまま「amount > 現在の最高額」の
// 場合のみ入札を受理するため、受理順(created_at ASC, id ASC = ロック取得順)で
// amountは厳密単調増加になる。したがってDESC順で返される一覧では厳密単調減少に
// なるはずである。FOR UPDATEを外す(直列化を壊す)と、複数の入札が同時に
// 「現在の最高額」を読んで両方受理されてしまい、受理順の金額が非増加(同額や逆転)に
// なり得る — 本関数はその違反をDESC順一覧上の非減少として検出する。
func ValidateBidAmountsMonotonic(bids []Bid) error {
	for i := 0; i+1 < len(bids); i++ {
		cur, next := bids[i], bids[i+1]
		if cur.Amount <= next.Amount {
			return fmt.Errorf(
				"bids の金額が単調増加違反 (受理順で単調増加のはずが、created_at DESC順で id=%d(amount=%d) の直後に id=%d(amount=%d) が来ており単調減少でない)",
				cur.ID, cur.Amount, next.ID, next.Amount)
		}
	}
	return nil
}

// ValidateBidsInvariant は入札列に対する不変条件(順序 + 金額の単調性)を
// まとめて検証するヘルパー。ValidateBidsOrdered → ValidateBidAmountsMonotonic の順に
// 検査し、最初に見つかったエラーを返す。
func ValidateBidsInvariant(bids []Bid) error {
	if err := ValidateBidsOrdered(bids); err != nil {
		return err
	}
	return ValidateBidAmountsMonotonic(bids)
}

func ValidateBidReflected(d *AuctionDetail, bid *BidCreated) error {
	if d.CurrentPrice < bid.Amount {
		return fmt.Errorf("auction %d: current_price %d が入札額 %d より小さい", d.ID, d.CurrentPrice, bid.Amount)
	}
	for _, b := range d.Bids {
		if b.ID == bid.ID {
			if b.Amount != bid.Amount || b.User.ID != bid.UserID {
				return fmt.Errorf("auction %d: 入札 id=%d の内容が不一致 (got amount=%d user=%d)", d.ID, bid.ID, b.Amount, b.User.ID)
			}
			return nil
		}
	}
	return fmt.Errorf("auction %d: 入札 id=%d が bids に見つからない", d.ID, bid.ID)
}

// closeGrace は終了処理の猶予。バッチは1秒間隔で回るため、ends_at 直後の短い
// あいだ live のままなのは正常。ベンチとアプリは同一ホストで動く前提で、
// 時計ずれは考慮しない。
//
// これは初期シードauction(expectedInitialAuctions, id<=10)向け。Validation実行時点で
// これらは(duration設定上)既に18〜50秒ends_atを過ぎており、closerが正常なら猶予5秒の
// 中で確実にclosed化が終わっているはずなので厳しい値のままでよい。ベンチがLoad中に
// 出品したlisting向けには猶予が薄すぎる(listingCloseGrace参照)ため、別定数を使う。
const closeGrace = 5 * time.Second

// listingCloseGrace はベンチが Load 中に作成した出品(sellerIteration)向けの猶予。
//
// 出品の duration は20〜40秒で、Validation は Load 終了直後に始まる。つまり Validation
// 開始時刻は「直前の20〜40秒間に作られた出品が続々と ends_at を迎える」タイミングに
// ちょうど重なり、closeDueAuctions のバックログが1本の走行の中で最も積み上がる瞬間である。
// closeDueAuctions は該当行を1件ずつ逐次トランザクション処理し、しかも bids/auctions には
// (意図的に)status/ends_at や auction_id のインデックスが無く、HTTPハンドラと共有する
// 10コネクションのプールを取り合う。したがってこの波を捌き切るのに、seed auction 側で
// 想定している5秒の猶予より数秒〜十数秒余計にかかってもおかしくない。closeGrace のまま
// listing にも適用すると、ベンチ自身が作った出品ラッシュに起因する遅延を受験者の
// 不具合と誤って critical にしてしまう(false-FAIL)。
//
// 30秒は、このバックログの波(同時に期限を迎える出品はどれだけ多くても走行スケールの
// 出品ワーカー数に比例した程度で、逐次処理でも十分に秒〜十数秒オーダーで捌ける規模)を
// 余裕を持って吸収しつつ、closer が本当に止まっているケースを見逃さない値として選んでいる。
const listingCloseGrace = 30 * time.Second

// ValidateAuctionClosedIfDue は ends_at を過ぎたオークションが closed に
// なっていることを検証する。終了処理バッチが動いていないことを検出する。
func ValidateAuctionClosedIfDue(d *AuctionDetail, now time.Time, grace time.Duration) error {
	if d.Status == "closed" {
		return nil
	}
	if d.EndsAt.Add(grace).Before(now) {
		return fmt.Errorf("auction %d: ends_at (%s) を過ぎているのに status が %q (期待: closed)",
			d.ID, d.EndsAt.Format(time.RFC3339), d.Status)
	}
	return nil
}

// ValidateFeedPage はフィード1ページ分の不変条件を検証する。
//
//	(1) すべての id が since より大きい
//	(2) id 昇順
//	(3) 金額が厳密単調増加
//
// (3)は詳細APIの ValidateBidAmountsMonotonic と同じ根拠(受理順で単調増加)を
// 昇順の並びに対して見たもの。同額や逆転は FOR UPDATE 不在の兆候である。
func ValidateFeedPage(bids []Bid, since int64) error {
	for i, b := range bids {
		if b.ID <= since {
			return fmt.Errorf("フィードに since(%d) 以下の入札が含まれる (index %d: id=%d)", since, i, b.ID)
		}
		if i == 0 {
			continue
		}
		prev := bids[i-1]
		if b.ID <= prev.ID {
			return fmt.Errorf("フィードが id 昇順でない (index %d: id=%d の前が id=%d)", i, b.ID, prev.ID)
		}
		if b.Amount <= prev.Amount {
			return fmt.Errorf("フィードの金額が単調増加でない (id=%d(amount=%d) の次に id=%d(amount=%d))",
				prev.ID, prev.Amount, b.ID, b.Amount)
		}
	}
	return nil
}

// ValidateNotificationsOrdered は通知一覧が id 降順であることを検証する。
func ValidateNotificationsOrdered(ns []Notification) error {
	for i := 1; i < len(ns); i++ {
		if ns[i].ID >= ns[i-1].ID {
			return fmt.Errorf("GET /notifications: id 降順でない (index %d: id=%d の前が id=%d)",
				i, ns[i].ID, ns[i-1].ID)
		}
	}
	return nil
}
