package main

import (
	"fmt"
	"time"
)

// expectedAuction は webapp/sql/90_seed_phase1.sql と一致させること(あちらが正)。
type expectedAuction struct {
	Title        string
	CurrentPrice int64
	BidCount     int64
	SellerID     int64
	CategoryID   int64
	EndsAtHour   int // ends_at = 2030-01-01 <hour>:00:00 UTC(シードの階段配置)
}

var expectedInitialAuctions = map[int64]expectedAuction{
	1:  {"ヘリテージ・ウィングチェア", 1500, 3, 1, 3, 1},
	2:  {"エルゴホスト Model E", 2100, 1, 2, 1, 2},
	3:  {"ISUレーサー GT", 3100, 1, 3, 2, 3},
	4:  {"メッシュフロー 40", 4100, 1, 4, 1, 4},
	5:  {"ミッドセンチュリー・ラウンジ", 2500, 0, 5, 3, 5},
	6:  {"ネオンストライク Z", 3000, 0, 6, 2, 6},
	7:  {"スタンドフレックス", 3500, 0, 7, 1, 7},
	8:  {"チャーチチェア 1920", 4000, 0, 8, 3, 8},
	9:  {"プロシート・エディション", 4500, 0, 9, 2, 9},
	10: {"コンパクトワーク 01", 5000, 0, 10, 1, 10},
}

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

func ValidateInitialAuctionList(list []AuctionSummary) error {
	if len(list) != len(expectedInitialAuctions) {
		return fmt.Errorf("GET /auctions: 件数が %d (期待: %d)", len(list), len(expectedInitialAuctions))
	}
	var prevEndsAt time.Time
	for i, a := range list {
		// シードは id昇順 = ends_at昇順 の階段配置
		if a.ID != int64(i+1) {
			return fmt.Errorf("GET /auctions: %d番目が id=%d (期待: id=%d / ends_at ASC順)", i, a.ID, i+1)
		}
		if a.EndsAt.Before(prevEndsAt) {
			return fmt.Errorf("GET /auctions: ends_at が昇順でない (id=%d)", a.ID)
		}
		prevEndsAt = a.EndsAt
		want := expectedInitialAuctions[a.ID]
		// ends_at の絶対値照合(2030-01-01 0N:00:00 UTC)
		if wantEndsAt := time.Date(2030, 1, 1, want.EndsAtHour, 0, 0, 0, time.UTC); !a.EndsAt.Equal(wantEndsAt) {
			return fmt.Errorf("auction %d: ends_at が %v (期待: %v)", a.ID, a.EndsAt, wantEndsAt)
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
