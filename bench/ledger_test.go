package main

import (
	"sync"
	"testing"
)

func TestLedgerRecordAndQuery(t *testing.T) {
	l := NewLedger()
	l.Record(AcceptedBid{BidID: 1, AuctionID: 1, UserID: 5, Amount: 1600})
	l.Record(AcceptedBid{BidID: 2, AuctionID: 1, UserID: 6, Amount: 1700})
	l.Record(AcceptedBid{BidID: 3, AuctionID: 2, UserID: 5, Amount: 2200})

	byAuction := l.ByAuction()
	if len(byAuction[1]) != 2 || len(byAuction[2]) != 1 {
		t.Fatalf("byAuction = %+v", byAuction)
	}
	if max, ok := l.MaxAmount(1); !ok || max != 1700 {
		t.Errorf("MaxAmount(1) = %d, %v; want 1700, true", max, ok)
	}
	if _, ok := l.MaxAmount(99); ok {
		t.Error("MaxAmount(99) should be false")
	}
}

func TestLedgerIntentConfirm(t *testing.T) {
	l := NewLedger()
	id := l.Intent(1, 5, 1600)

	pending := l.PendingByAuction()
	if len(pending[1]) != 1 || pending[1][0].Amount != 1600 {
		t.Fatalf("PendingByAuction before confirm = %+v", pending)
	}

	l.Confirm(id, AcceptedBid{BidID: 42, AuctionID: 1, UserID: 5, Amount: 1600})

	pending = l.PendingByAuction()
	if len(pending[1]) != 0 {
		t.Errorf("PendingByAuction after confirm = %+v, want empty", pending)
	}
	if max, ok := l.MaxAmount(1); !ok || max != 1600 {
		t.Errorf("MaxAmount(1) = %d, %v; want 1600, true", max, ok)
	}
	byAuction := l.ByAuction()
	if len(byAuction[1]) != 1 || byAuction[1][0].BidID != 42 {
		t.Fatalf("ByAuction after confirm = %+v", byAuction)
	}
}

func TestLedgerIntentReject(t *testing.T) {
	l := NewLedger()
	id := l.Intent(1, 5, 1600)
	l.Reject(id)

	pending := l.PendingByAuction()
	if len(pending[1]) != 0 {
		t.Errorf("PendingByAuction after reject = %+v, want empty", pending)
	}
	if _, ok := l.MaxAmount(1); ok {
		t.Error("MaxAmount(1) should be false after reject (nothing accepted)")
	}
}

func TestLedgerIntentLeftPending(t *testing.T) {
	l := NewLedger()
	id1 := l.Intent(1, 5, 1600)
	id2 := l.Intent(1, 6, 1700)
	l.Confirm(id2, AcceptedBid{BidID: 2, AuctionID: 1, UserID: 6, Amount: 1700})
	// id1 の結果は不明のまま(転送エラー等)放置される。

	pending := l.PendingByAuction()
	if len(pending[1]) != 1 || pending[1][0].IntentID != id1 || pending[1][0].Amount != 1600 {
		t.Fatalf("PendingByAuction = %+v, want only id1 (amount 1600) left pending", pending)
	}
}

func TestLedgerConcurrentIntentConfirmReject(t *testing.T) {
	l := NewLedger()
	var wg sync.WaitGroup
	for i := 0; i < 150; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := l.Intent(int64(i%5), 1, int64(1000+i))
			switch i % 3 {
			case 0:
				l.Confirm(id, AcceptedBid{BidID: int64(i), AuctionID: int64(i % 5), UserID: 1, Amount: int64(1000 + i)})
			case 1:
				l.Reject(id)
			default:
				// leave pending (unknown outcome)
			}
		}(i)
	}
	wg.Wait()

	acceptedTotal := 0
	for _, bids := range l.ByAuction() {
		acceptedTotal += len(bids)
	}
	pendingTotal := 0
	for _, bids := range l.PendingByAuction() {
		pendingTotal += len(bids)
	}
	// i%3==0 -> confirmed (50), i%3==1 -> rejected (50), else left pending (50)
	if acceptedTotal != 50 {
		t.Errorf("acceptedTotal = %d, want 50", acceptedTotal)
	}
	if pendingTotal != 50 {
		t.Errorf("pendingTotal = %d, want 50", pendingTotal)
	}
}

func TestLedgerConcurrentRecord(t *testing.T) {
	l := NewLedger()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l.Record(AcceptedBid{BidID: int64(i), AuctionID: int64(i % 5), UserID: 1, Amount: int64(1000 + i)})
		}(i)
	}
	wg.Wait()
	total := 0
	for _, bids := range l.ByAuction() {
		total += len(bids)
	}
	if total != 100 {
		t.Errorf("total = %d, want 100", total)
	}
}
