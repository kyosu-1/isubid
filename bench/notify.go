package main

import "sort"

// ExpectedOutbidCounts は台帳の確定受理入札から、各ユーザーが受け取るべき
// outbid 通知数の「下限」を計算する。
//
// 参照実装のファンアウトは「そのオークションに入札済みの全ユーザー(今回の入札者を除く)」
// 宛なので、ユーザー U が受け取るべき件数は
//
//	U が入札した各オークション A について、A 上で U の最初の入札より後に
//	受理された他ユーザーの入札の件数。その総和。
//
// で決まる。受理順は BidID の昇順(入札APIがオークション行を FOR UPDATE で保持したまま
// INSERT するため、同一オークション内では id 順 = 受理順)で定める。
//
// これが「下限」なのは2点による:
//   - pending(結果不明)入札は数えていない
//   - ベンチはシードユーザーとしてログインするため、そのユーザーにはシード入札
//     (走行前の入札)がある場合があり、実際の期待件数はこれ以上になりうる
//
// したがって検証は「受信数 < 下限なら違反」とする。
func ExpectedOutbidCounts(byAuction map[int64][]AcceptedBid) map[int64]int64 {
	out := map[int64]int64{}
	for _, bids := range byAuction {
		sorted := append([]AcceptedBid(nil), bids...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].BidID < sorted[j].BidID })

		firstBidID := map[int64]int64{}
		for _, b := range sorted {
			if _, ok := firstBidID[b.UserID]; !ok {
				firstBidID[b.UserID] = b.BidID
			}
		}
		for uid, first := range firstBidID {
			var n int64
			for _, b := range sorted {
				if b.UserID != uid && b.BidID > first {
					n++
				}
			}
			if n > 0 {
				out[uid] += n
			}
		}
	}
	return out
}

// CountByType は指定 type の通知件数を返す。
func CountByType(ns []Notification, typ string) int64 {
	var n int64
	for _, x := range ns {
		if x.Type == typ {
			n++
		}
	}
	return n
}

// HasWonNotification は指定オークションの落札通知があるかを返す。
func HasWonNotification(ns []Notification, auctionID int64) bool {
	for _, x := range ns {
		if x.Type == "won" && x.AuctionID == auctionID {
			return true
		}
	}
	return false
}
