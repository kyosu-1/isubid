package main

import "testing"

// ExpectedOutbidCounts はファンアウト範囲(そのオークションに入札済みの全ユーザー)に
// 対応した「受け取るべき outbid 通知数の下限」を台帳から計算する。
func TestExpectedOutbidCounts(t *testing.T) {
	tests := []struct {
		name      string
		byAuction map[int64][]AcceptedBid
		want      map[int64]int64
	}{
		{
			name:      "入札なし",
			byAuction: map[int64][]AcceptedBid{},
			want:      map[int64]int64{},
		},
		{
			name: "単独入札者は誰にも抜かれないので0件",
			byAuction: map[int64][]AcceptedBid{
				1: {{BidID: 10, AuctionID: 1, UserID: 5, Amount: 1600}},
			},
			want: map[int64]int64{},
		},
		{
			name: "2人が交互に入札: 先行者は後続2件、後続者は先行者の2回目1件",
			byAuction: map[int64][]AcceptedBid{
				1: {
					{BidID: 10, AuctionID: 1, UserID: 5, Amount: 1600},
					{BidID: 11, AuctionID: 1, UserID: 6, Amount: 1700},
					{BidID: 12, AuctionID: 1, UserID: 5, Amount: 1800},
					{BidID: 13, AuctionID: 1, UserID: 6, Amount: 1900},
				},
			},
			// user 5 の最初は id=10。それより後の他ユーザー入札は id=11,13 の2件
			// user 6 の最初は id=11。それより後の他ユーザー入札は id=12 の1件
			want: map[int64]int64{5: 2, 6: 1},
		},
		{
			name: "台帳の並びが受理順でなくても BidID で正しく順序づけられる",
			byAuction: map[int64][]AcceptedBid{
				1: {
					{BidID: 13, AuctionID: 1, UserID: 6, Amount: 1900},
					{BidID: 10, AuctionID: 1, UserID: 5, Amount: 1600},
					{BidID: 12, AuctionID: 1, UserID: 5, Amount: 1800},
					{BidID: 11, AuctionID: 1, UserID: 6, Amount: 1700},
				},
			},
			want: map[int64]int64{5: 2, 6: 1},
		},
		{
			name: "複数オークションは合算される",
			byAuction: map[int64][]AcceptedBid{
				1: {
					{BidID: 10, AuctionID: 1, UserID: 5, Amount: 1600},
					{BidID: 11, AuctionID: 1, UserID: 6, Amount: 1700},
				},
				2: {
					{BidID: 20, AuctionID: 2, UserID: 5, Amount: 2600},
					{BidID: 21, AuctionID: 2, UserID: 7, Amount: 2700},
				},
			},
			want: map[int64]int64{5: 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpectedOutbidCounts(tt.byAuction)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for uid, want := range tt.want {
				if got[uid] != want {
					t.Errorf("user %d: got %d, want %d (全体: %v)", uid, got[uid], want, got)
				}
			}
		})
	}
}

func TestNotificationHelpers(t *testing.T) {
	ns := []Notification{
		{ID: 3, Type: "won", AuctionID: 4},
		{ID: 2, Type: "outbid", AuctionID: 1},
		{ID: 1, Type: "outbid", AuctionID: 1},
	}
	if got := CountByType(ns, "outbid"); got != 2 {
		t.Errorf("CountByType(outbid) = %d, want 2", got)
	}
	if got := CountByType(ns, "won"); got != 1 {
		t.Errorf("CountByType(won) = %d, want 1", got)
	}
	if !HasWonNotification(ns, 4) {
		t.Error("auction 4 の won 通知が見つからない")
	}
	if HasWonNotification(ns, 1) {
		t.Error("auction 1 に won 通知は無いはず")
	}

	if err := ValidateNotificationsOrdered(ns); err != nil {
		t.Errorf("id DESC の通知列が拒否された: %v", err)
	}
	asc := []Notification{{ID: 1}, {ID: 2}}
	if err := ValidateNotificationsOrdered(asc); err == nil {
		t.Error("id ASC が検出されなかった")
	}
}
