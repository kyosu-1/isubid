package main

import (
	"context"
	"testing"
)

// newTestHandler はテスト用にDB直結の handler を返す。
func newTestHandler(t *testing.T) *handler {
	t.Helper()
	db, err := connectDB()
	if err != nil {
		t.Fatalf("connectDB: %v (dev/compose.yaml の mysql は起動していますか?)", err)
	}
	t.Cleanup(func() { db.Close() })
	return &handler{db: db}
}

func TestCloseDueAuctions(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	h := newTestHandler(t)
	ctx := context.Background()

	// auction 1: seed入札3件、最高額 1500 / user 4
	// auction 5: 入札0件
	// どちらも終了時刻を過去にする
	if _, err := h.db.ExecContext(ctx,
		"UPDATE auctions SET ends_at = DATE_SUB(NOW(6), INTERVAL 1 SECOND) WHERE id IN (1, 5)"); err != nil {
		t.Fatal(err)
	}

	n, err := h.closeDueAuctions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("closed = %d, want 2", n)
	}

	var a1 struct {
		Status       string `db:"status"`
		WinnerID     *int64 `db:"winner_id"`
		WinningPrice *int64 `db:"winning_price"`
	}
	if err := h.db.GetContext(ctx, &a1,
		"SELECT status, winner_id, winning_price FROM auctions WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if a1.Status != "closed" {
		t.Errorf("auction 1 status = %q, want closed", a1.Status)
	}
	if a1.WinnerID == nil || *a1.WinnerID != 4 {
		t.Errorf("auction 1 winner_id = %v, want 4", a1.WinnerID)
	}
	if a1.WinningPrice == nil || *a1.WinningPrice != 1500 {
		t.Errorf("auction 1 winning_price = %v, want 1500", a1.WinningPrice)
	}

	var a5 struct {
		Status   string `db:"status"`
		WinnerID *int64 `db:"winner_id"`
	}
	if err := h.db.GetContext(ctx, &a5,
		"SELECT status, winner_id FROM auctions WHERE id = 5"); err != nil {
		t.Fatal(err)
	}
	if a5.Status != "closed" {
		t.Errorf("auction 5 status = %q, want closed", a5.Status)
	}
	if a5.WinnerID != nil {
		t.Errorf("auction 5 winner_id = %v, want nil (入札0件)", a5.WinnerID)
	}

	// 冪等性: もう一度呼んでも対象が無い
	n2, err := h.closeDueAuctions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("2回目の closed = %d, want 0", n2)
	}
}

// closeAuction は ends_at が未来のオークションには手を出してはならない。
//
// Critical回帰テスト: closeDueAuctions は status='live' AND ends_at<=NOW(6) で
// 対象idを集めてから1件ずつ closeAuction を呼ぶが、両者の間には(意図的に遅い)
// 全件スキャンとbids非インデックス検索を挟むため、実運用では数百msの間隔が空く。
// POST /initialize は明示idでテーブルを丸ごと入れ替えるため、その間隔の間に
// 別のゴルーチン(あるいは次のベンチ実行)が /initialize を叩くと、収集済みの
// idが「たまたま同じidを持つ、期限がまだ先の新しいオークション」を指してしまう
// ことがある。closeAuction が ends_at を再チェックしないと、そのオークションを
// 誤って closed にしてしまう。ここでは間隔を空けず直接 closeAuction を呼ぶことで
// 同じ入力(ends_at が未来のauction id)を再現する。
func TestCloseAuctionDoesNotCloseFutureAuction(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	h := newTestHandler(t)
	ctx := context.Background()

	// auction 1 は auctionEndOffsets(initialize.go)で +3600秒。initialize直後は
	// status='live' のまま ends_at が1時間先にあり、closeDueAuctions が拾う対象では
	// ないはずの行である。
	if err := h.closeAuction(ctx, 1); err != nil {
		t.Fatal(err)
	}

	var a struct {
		Status   string `db:"status"`
		WinnerID *int64 `db:"winner_id"`
	}
	if err := h.db.GetContext(ctx, &a,
		"SELECT status, winner_id FROM auctions WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if a.Status != "live" {
		t.Errorf("auction 1 status = %q, want live (ends_at はまだ未来のはず)", a.Status)
	}
	if a.WinnerID != nil {
		t.Errorf("auction 1 winner_id = %v, want nil (未終了のはず)", a.WinnerID)
	}
}

// closeAuction は既に closed のオークションに対して何もしない(冪等)。
func TestCloseAuctionIdempotent(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	h := newTestHandler(t)
	ctx := context.Background()

	// auction 11 は seed で closed / winner 12 / 12000
	if err := h.closeAuction(ctx, 11); err != nil {
		t.Fatal(err)
	}
	var a struct {
		WinnerID     *int64 `db:"winner_id"`
		WinningPrice *int64 `db:"winning_price"`
	}
	if err := h.db.GetContext(ctx, &a,
		"SELECT winner_id, winning_price FROM auctions WHERE id = 11"); err != nil {
		t.Fatal(err)
	}
	if a.WinnerID == nil || *a.WinnerID != 12 || a.WinningPrice == nil || *a.WinningPrice != 12000 {
		t.Errorf("auction 11 が書き換えられた: winner=%v price=%v, want 12/12000", a.WinnerID, a.WinningPrice)
	}
}

// 落札確定時、落札者に won 通知が1件入る。
func TestCloseAuctionNotifiesWinner(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	h := newTestHandler(t)
	ctx := context.Background()

	if _, err := h.db.ExecContext(ctx,
		"UPDATE auctions SET ends_at = DATE_SUB(NOW(6), INTERVAL 1 SECOND) WHERE id IN (1, 5)"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.closeDueAuctions(ctx); err != nil {
		t.Fatal(err)
	}

	// auction 1 の落札者は user 4
	var n int64
	if err := h.db.GetContext(ctx, &n,
		"SELECT COUNT(*) FROM notifications WHERE user_id = 4 AND auction_id = 1 AND type = 'won'"); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("won 通知が %d件, want 1", n)
	}

	// auction 5 は入札0件なので won 通知は出ない
	if err := h.db.GetContext(ctx, &n,
		"SELECT COUNT(*) FROM notifications WHERE auction_id = 5 AND type = 'won'"); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("入札0件の auction 5 に won 通知が %d件, want 0", n)
	}

	// 冪等性: closeAuction を2回目に呼んでも won 通知は増えない
	if err := h.closeAuction(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := h.db.GetContext(ctx, &n,
		"SELECT COUNT(*) FROM notifications WHERE user_id = 4 AND auction_id = 1 AND type = 'won'"); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("2回目の closeAuction 後、won 通知が %d件, want 1", n)
	}
}
