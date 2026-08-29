package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

// closerInterval は終了処理バッチの実行間隔。
const closerInterval = 1 * time.Second

// closeDueAuctions は終了時刻を過ぎた live オークションを closed にする。戻り値は処理した件数。
//
// 意図的に遅い実装: status/ends_at にインデックスが無いためフルスキャンになり、
// 該当を1件ずつ逐次処理する(まとめて UPDATE しない)。
func (h *handler) closeDueAuctions(ctx context.Context) (int, error) {
	var ids []int64
	if err := h.db.SelectContext(ctx, &ids,
		"SELECT id FROM auctions WHERE status = 'live' AND ends_at <= NOW(6)"); err != nil {
		return 0, err
	}
	closed := 0
	for _, id := range ids {
		if err := h.closeAuction(ctx, id); err != nil {
			// 1件の失敗でバッチ全体を止めない
			log.Printf("closeAuction(%d): %v", id, err)
			continue
		}
		closed++
	}
	return closed, nil
}

// closeAuction は1オークションの終了処理をトランザクションで行う。
// 既に closed なら何もしない(冪等)。
//
// オークション行を FOR UPDATE で保持したまま最高額を求めて確定するため、
// 入札API(同じ行ロックを取る)とは直列化される。したがって
// 「落札額 = そのオークションの入札の最大額」が厳密に成立する。
func (h *handler) closeAuction(ctx context.Context, auctionID int64) error {
	tx, err := h.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var a auctionRow
	if err := tx.GetContext(ctx, &a,
		"SELECT "+auctionColumns+" FROM auctions WHERE id = ? FOR UPDATE", auctionID); err != nil {
		return err
	}
	if a.Status != "live" {
		return nil // 既に処理済み
	}

	var top struct {
		UserID int64 `db:"user_id"`
		Amount int64 `db:"amount"`
	}
	// 意図的に遅い実装: bids に auction_id のインデックスが無い。
	// amount DESC, id ASC で最高額を一意に定める(同額はFOR UPDATE不在の兆候であり、
	// ベンチの単調増加検証が別途 critical で捕まえる)。
	err = tx.GetContext(ctx, &top,
		"SELECT user_id, amount FROM bids WHERE auction_id = ? ORDER BY amount DESC, id ASC LIMIT 1", auctionID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx,
			"UPDATE auctions SET status = 'closed' WHERE id = ?", auctionID); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if _, err := tx.ExecContext(ctx,
			"UPDATE auctions SET status = 'closed', winner_id = ?, winning_price = ? WHERE id = ?",
			top.UserID, top.Amount, auctionID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO notifications (user_id, type, auction_id, message) VALUES (?, 'won', ?, ?)",
			top.UserID, auctionID, "「"+truncateForNotification(a.Title)+"」を落札しました"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// runAuctionCloser は closerInterval 間隔で closeDueAuctions を呼び続ける。
func (h *handler) runAuctionCloser(ctx context.Context) {
	t := time.NewTicker(closerInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := h.closeDueAuctions(ctx); err != nil {
				log.Printf("closeDueAuctions: %v", err)
			}
		}
	}
}
