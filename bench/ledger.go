package main

import "sync"

// AcceptedBid はベンチが201を受け取った入札の記録。Validationフェーズで実データと突合する。
type AcceptedBid struct {
	BidID     int64
	AuctionID int64
	UserID    int64
	Amount    int64
}

// PendingBid は「送信済みだが結果が確定していない」入札の記録。
// POST /auctions/:id/bids はネットワークエラーやctxキャンセルで応答を受け取れないことがあるが、
// サーバー側では既にコミットされている可能性がある(in-flight commit)。この場合ベンチ側は
// 201を観測できず AcceptedBid を作れないため、Validationが「消えた入札」と誤検知(false-FAIL)
// してしまう。これを避けるため、送信前に Intent で先に記録し、結果が判明したら
// Confirm(201確定)/Reject(4xx確定で未コミット確定)で解消する。転送エラー等で結果不明のまま
// 残った PendingBid は「サーバーに実際にコミットされたかもしれない入札」として
// Validationで許容(tolerate)する。
type PendingBid struct {
	IntentID  int64
	AuctionID int64
	UserID    int64
	Amount    int64
}

type Ledger struct {
	mu       sync.Mutex
	accepted map[int64][]AcceptedBid // auctionID -> bids
	pending  map[int64]PendingBid    // intentID -> pending bid
	nextID   int64
}

func NewLedger() *Ledger {
	return &Ledger{
		accepted: map[int64][]AcceptedBid{},
		pending:  map[int64]PendingBid{},
	}
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

// Intent は POST 送信前に呼び、結果確定前の入札を pending として記録する。
// 戻り値の intent id を Confirm/Reject に渡して結果を確定させる。
func (l *Ledger) Intent(auctionID, userID, amount int64) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextID++
	id := l.nextID
	l.pending[id] = PendingBid{IntentID: id, AuctionID: auctionID, UserID: userID, Amount: amount}
	return id
}

// Confirm は intent が201で確定受理されたことを記録する。pendingを解消し、acceptedへ昇格させる。
func (l *Ledger) Confirm(intentID int64, b AcceptedBid) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.pending, intentID)
	l.accepted[b.AuctionID] = append(l.accepted[b.AuctionID], b)
}

// Reject は intent が(4xxなど)確定的に未コミットだったことを記録し、pendingから外す。
func (l *Ledger) Reject(intentID int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.pending, intentID)
}

// PendingByAuction は結果不明のまま残っている intent のコピーを auctionID ごとに返す。
func (l *Ledger) PendingByAuction() map[int64][]PendingBid {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[int64][]PendingBid, len(l.pending))
	for _, p := range l.pending {
		out[p.AuctionID] = append(out[p.AuctionID], p)
	}
	return out
}
