package main

import (
	"testing"
	"time"
)

func TestReconcileAuction(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	seedBid := func(id int64, t time.Time) Bid {
		return Bid{ID: id, User: User{ID: 2}, Amount: 1000, CreatedAt: t}
	}

	tests := []struct {
		name       string
		d          *AuctionDetail
		seedCount  int64
		seedPrice  int64
		accepted   []AcceptedBid
		pending    []PendingBid
		wantErrLen int
	}{
		{
			name: "clean pass: only seed bids, current_price matches seed",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1000},
				Bids:           []Bid{seedBid(1, t0)},
			},
			seedCount:  1,
			seedPrice:  1000,
			wantErrLen: 0,
		},
		{
			name: "committed-but-unreceived bid: pending matches an id>8 bid, passes",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1600},
				Bids: []Bid{
					{ID: 9, User: User{ID: 5}, Amount: 1600, CreatedAt: t0.Add(time.Hour)},
					seedBid(1, t0),
				},
			},
			seedCount: 1,
			seedPrice: 1000,
			pending:   []PendingBid{{AuctionID: 1, UserID: 5, Amount: 1600}},
			// unmatched-bid check passes AND current_price check tolerated by same pending
			wantErrLen: 0,
		},
		{
			name: "duplicate-applied bid: id>8 bid unexplained by accepted or pending fails",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1000},
				Bids: []Bid{
					{ID: 9, User: User{ID: 5}, Amount: 900, CreatedAt: t0.Add(time.Hour)},
					seedBid(1, t0),
				},
			},
			seedCount:  1,
			seedPrice:  1000,
			wantErrLen: 1,
		},
		{
			name: "vanished ledger bid: accepted bid missing from detail fails",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1000},
				Bids:           []Bid{seedBid(1, t0)},
			},
			seedCount:  1,
			seedPrice:  1000,
			accepted:   []AcceptedBid{{BidID: 9, AuctionID: 1, UserID: 5, Amount: 1600}},
			wantErrLen: 2, // 消失 + current_price不一致(期待1600、実際1000)
		},
		{
			name: "vanished seed bid: fewer id<=8 bids than seedCount fails",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1000},
				Bids:           []Bid{seedBid(1, t0)},
			},
			seedCount:  2,
			seedPrice:  1000,
			wantErrLen: 1,
		},
		{
			name: "current_price matching pending passes (no other bid rows yet reflected)",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1700},
				Bids:           []Bid{seedBid(1, t0)},
			},
			seedCount: 1,
			seedPrice: 1000,
			pending:   []PendingBid{{AuctionID: 1, UserID: 6, Amount: 1700}},
			// current_price tolerated by pending; no extra bid rows so no bid-set error
			wantErrLen: 0,
		},
		{
			name: "current_price random too-high fails (no pending explains it)",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 9999},
				Bids:           []Bid{seedBid(1, t0)},
			},
			seedCount:  1,
			seedPrice:  1000,
			wantErrLen: 1,
		},
		{
			name: "accepted bid content mismatch (改変) fails",
			d: &AuctionDetail{
				AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1600},
				Bids: []Bid{
					{ID: 9, User: User{ID: 5}, Amount: 1650, CreatedAt: t0.Add(time.Hour)}, // amount differs from ledger
					seedBid(1, t0),
				},
			},
			seedCount:  1,
			seedPrice:  1000,
			accepted:   []AcceptedBid{{BidID: 9, AuctionID: 1, UserID: 5, Amount: 1600}},
			// current_price(1600) == expected(max(seed 1000, accepted 1600)) なので価格チェックは通る。
			// 改変チェックのみ1件。
			wantErrLen: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := reconcileAuction(1, tc.d, tc.seedCount, tc.seedPrice, tc.accepted, tc.pending)
			if len(errs) != tc.wantErrLen {
				t.Errorf("reconcileAuction() = %v errors (%v), want %d", len(errs), errs, tc.wantErrLen)
			}
		})
	}
}

func TestReconcileAuctionPendingConsumedOncePerMatch(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// Two id>8 bids with the same user/amount, but only one pending to explain them:
	// the second must fail as unexplained (pending consumed at most once).
	d := &AuctionDetail{
		AuctionSummary: AuctionSummary{ID: 1, CurrentPrice: 1600},
		Bids: []Bid{
			{ID: 10, User: User{ID: 5}, Amount: 1600, CreatedAt: t0.Add(2 * time.Hour)},
			{ID: 9, User: User{ID: 5}, Amount: 1600, CreatedAt: t0.Add(time.Hour)},
		},
	}
	pending := []PendingBid{{AuctionID: 1, UserID: 5, Amount: 1600}}
	errs := reconcileAuction(1, d, 0, 1000, nil, pending)
	if len(errs) != 1 {
		t.Fatalf("reconcileAuction() = %v errors (%v), want exactly 1 (one bid unexplained)", len(errs), errs)
	}
}
