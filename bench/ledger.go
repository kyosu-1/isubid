package main

import "sync"

// AcceptedBid はベンチが201を受け取った入札の記録。Validationフェーズで実データと突合する。
type AcceptedBid struct {
	BidID     int64
	AuctionID int64
	UserID    int64
	Amount    int64
}

type Ledger struct {
	mu       sync.Mutex
	accepted map[int64][]AcceptedBid // auctionID -> bids
}

func NewLedger() *Ledger {
	return &Ledger{accepted: map[int64][]AcceptedBid{}}
}

func (l *Ledger) Record(b AcceptedBid) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.accepted[b.AuctionID] = append(l.accepted[b.AuctionID], b)
}

// ByAuction は記録のコピーを返す(以降のRecordの影響を受けない)。
func (l *Ledger) ByAuction() map[int64][]AcceptedBid {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[int64][]AcceptedBid, len(l.accepted))
	for id, bids := range l.accepted {
		cp := make([]AcceptedBid, len(bids))
		copy(cp, bids)
		out[id] = cp
	}
	return out
}

func (l *Ledger) MaxAmount(auctionID int64) (int64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	bids, ok := l.accepted[auctionID]
	if !ok || len(bids) == 0 {
		return 0, false
	}
	max := bids[0].Amount
	for _, b := range bids[1:] {
		if b.Amount > max {
			max = b.Amount
		}
	}
	return max, true
}
