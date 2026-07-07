package main

import "fmt"

// reconcileAuction は1オークションの詳細レスポンス d を、ベンチの台帳
// (確定受理された accepted と、結果不明のまま残っている pending)および
// シード時点の既知状態(seedCount/seedCurrent)と突き合わせ、違反を返す
// (空スライス = 問題なし)。
//
// ベンチ以外に書き込み手はいないため、あるオークションの入札集合は理論上
// 「シード入札(id<=8) ∪ 台帳が確定受理した入札 ∪ 結果不明のpending」に
// 一致するはずである。id>8 のうち accepted にも pending にも説明が付かない
// 入札は「台帳にない入札」= 二重適用の疑いとしてcriticalにする。
//
// current_price も同様に、seedCurrent と accepted の最大額から決まる期待値
// に一致しない場合、pending の中に current_price と同額のものがあれば
// (in-flight commitの疑いとして)許容し、なければcriticalにする。
func reconcileAuction(auctionID int64, d *AuctionDetail, seedCount, seedCurrent int64, accepted []AcceptedBid, pending []PendingBid) []error {
	var errs []error

	byID := make(map[int64]Bid, len(d.Bids))
	for _, b := range d.Bids {
		byID[b.ID] = b
	}

	// 1. 台帳が確定受理した入札が、実データに正しく反映されているか。
	consumed := make(map[int64]bool, len(accepted))
	for _, ab := range accepted {
		got, ok := byID[ab.BidID]
		if !ok {
			errs = append(errs, fmt.Errorf("auction %d: 受理された入札 id=%d が消失", auctionID, ab.BidID))
			continue
		}
		consumed[ab.BidID] = true
		if got.Amount != ab.Amount || got.User.ID != ab.UserID {
			errs = append(errs, fmt.Errorf("auction %d: 入札 id=%d の内容が改変 (got amount=%d user=%d, want amount=%d user=%d)",
				auctionID, ab.BidID, got.Amount, got.User.ID, ab.Amount, ab.UserID))
		}
	}

	// 2. id>8 の入札のうち、accepted で説明が付かないものは pending(未確定)と
	//    突き合わせる。それでも説明が付かなければ「台帳にない入札」。
	//    id<=8 (シード)の件数も同時に数える。
	remainingPending := append([]PendingBid(nil), pending...)
	var seedSeen int64
	for _, b := range d.Bids {
		if b.ID <= 8 {
			seedSeen++
			continue
		}
		if consumed[b.ID] {
			continue
		}
		matched := -1
		for i, p := range remainingPending {
			if p.UserID == b.User.ID && p.Amount == b.Amount {
				matched = i
				break
			}
		}
		if matched >= 0 {
			// 1つのpendingは1回だけ消費する(二重にマッチさせない)。
			remainingPending = append(remainingPending[:matched], remainingPending[matched+1:]...)
			continue
		}
		errs = append(errs, fmt.Errorf("auction %d: 台帳にない入札が存在(二重適用の疑い) (id=%d amount=%d user=%d)",
			auctionID, b.ID, b.Amount, b.User.ID))
	}
	if seedSeen != seedCount {
		errs = append(errs, fmt.Errorf("auction %d: シード入札の件数が %d件 (期待: %d件、消失の疑い)", auctionID, seedSeen, seedCount))
	}

	// 3. current_price = max(seedCurrent, acceptedの最大額) が原則。
	//    ずれている場合、pendingのAmountと一致し、かつそれが期待値を上回るなら
	//    (in-flight commitの疑いとして)許容する。
	expected := seedCurrent
	haveAccepted := false
	var acceptedMax int64
	for _, ab := range accepted {
		if !haveAccepted || ab.Amount > acceptedMax {
			acceptedMax = ab.Amount
			haveAccepted = true
		}
	}
	if haveAccepted && acceptedMax > expected {
		expected = acceptedMax
	}
	if d.CurrentPrice != expected {
		tolerated := false
		for _, p := range pending {
			if p.Amount == d.CurrentPrice && p.Amount > expected {
				tolerated = true
				break
			}
		}
		if !tolerated {
			errs = append(errs, fmt.Errorf("auction %d: current_price が %d (期待: %d)", auctionID, d.CurrentPrice, expected))
		}
	}

	if err := ValidateBidsInvariant(d.Bids); err != nil {
		errs = append(errs, fmt.Errorf("auction %d: %w", auctionID, err))
	}

	return errs
}
