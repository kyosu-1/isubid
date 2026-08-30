package main

import (
	"fmt"
	"time"
)

// expectedAuction は webapp/sql/90_seed_phase1.sql と
// webapp/go/initialize.go の auctionEndOffsets に一致させること(あちらが正)。
type expectedAuction struct {
	Title        string
	CurrentPrice int64
	BidCount     int64
	SellerID     int64
	CategoryID   int64
	EndsAtOffset int // ends_at = initialize時刻 + このオフセット(秒)
}

var expectedInitialAuctions = map[int64]expectedAuction{
	1:  {"ヘリテージ・ウィングチェア", 1500, 3, 1, 3, 3600},
	2:  {"エルゴホスト Model E", 2100, 1, 2, 1, 20},
	3:  {"ISUレーサー GT", 3100, 1, 3, 2, 3660},
	4:  {"メッシュフロー 40", 4100, 1, 4, 1, 12},
	5:  {"ミッドセンチュリー・ラウンジ", 2500, 0, 5, 3, 3720},
	6:  {"ネオンストライク Z", 3000, 0, 6, 2, 36},
	7:  {"スタンドフレックス", 3500, 0, 7, 1, 3780},
	8:  {"チャーチチェア 1920", 4000, 0, 8, 3, 28},
	9:  {"プロシート・エディション", 4500, 0, 9, 2, 3840},
	10: {"コンパクトワーク 01", 5000, 0, 10, 1, 44},
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

// ValidateAuctionListWithSnapshot は生成データ搭載時の一覧検証。
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
func ValidateAuctionListWithSnapshot(list []AuctionSummary, snap *Snapshot, base time.Time) error {
	want := int64(len(expectedInitialAuctions)) + snap.Counts.LiveAuctions
	if int64(len(list)) != want {
		return fmt.Errorf("GET /auctions: 件数が %d (期待: %d = シード %d + 生成 %d)",
			len(list), want, len(expectedInitialAuctions), snap.Counts.LiveAuctions)
	}

	// ends_at が非減少
	for i := 1; i < len(list); i++ {
		if list[i].EndsAt.Before(list[i-1].EndsAt) {
			return fmt.Errorf("GET /auctions: ends_at が昇順でない (index %d: id=%d %v の前が id=%d %v)",
				i, list[i].ID, list[i].EndsAt, list[i-1].ID, list[i-1].EndsAt)
		}
	}

	// 各行の中身を、シードは既存の期待値表、生成分はスナップショットと照合する
	for _, a := range list {
		if a.Status != "live" {
			return fmt.Errorf("auction %d: status が %q (期待: live)", a.ID, a.Status)
		}
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
