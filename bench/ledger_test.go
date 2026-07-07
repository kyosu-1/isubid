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
