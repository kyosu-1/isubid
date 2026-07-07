package main

import (
	"fmt"
)

// expectedAuction は webapp/sql/90_seed_phase1.sql と一致させること(あちらが正)。
type expectedAuction struct {
	Title        string
	CurrentPrice int64
	BidCount     int64
	SellerID     int64
}

var expectedInitialAuctions = map[int64]expectedAuction{
	1:  {"ヘリテージ・ウィングチェア", 1500, 3, 1},
	2:  {"エルゴホスト Model E", 2100, 1, 2},
	3:  {"ISUレーサー GT", 3100, 1, 3},
	4:  {"メッシュフロー 40", 4100, 1, 4},
	5:  {"ミッドセンチュリー・ラウンジ", 2500, 0, 5},
	6:  {"ネオンストライク Z", 3000, 0, 6},
	7:  {"スタンドフレックス", 3500, 0, 7},
	8:  {"チャーチチェア 1920", 4000, 0, 8},
	9:  {"プロシート・エディション", 4500, 0, 9},
	10: {"コンパクトワーク 01", 5000, 0, 10},
}

func pad2(n int64) string {
	return fmt.Sprintf("%02d", n)
}

func ValidateInitialAuctionList(list []AuctionSummary) error {
	if len(list) != len(expectedInitialAuctions) {
		return fmt.Errorf("GET /auctions: 件数が %d (期待: %d)", len(list), len(expectedInitialAuctions))
	}
	for _, a := range list {
		want, ok := expectedInitialAuctions[a.ID]
		if !ok {
			return fmt.Errorf("GET /auctions: 未知のオークション id=%d", a.ID)
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
		if a.Seller.ID != want.SellerID || a.Seller.Name != "seed_user_"+pad2(want.SellerID) {
			return fmt.Errorf("auction %d: seller が %+v (期待: id=%d)", a.ID, a.Seller, want.SellerID)
		}
	}
	return nil
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
