package main

import (
	"database/sql"
	"net/http"
)

// getStatsMe は出品者の売上サマリを返す。
//
// 意図的に遅い実装: auctions に seller_id のインデックスが無いうえ、
// 1本のクエリにまとめず4回に分けて全走査する。
func (h *handler) getStatsMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	ctx := r.Context()

	var listed int64
	if err := h.db.GetContext(ctx, &listed,
		"SELECT COUNT(*) FROM auctions WHERE seller_id = ?", userID); err != nil {
		writeInternalError(w, r, err)
		return
	}
	var sold int64
	if err := h.db.GetContext(ctx, &sold,
		"SELECT COUNT(*) FROM auctions WHERE seller_id = ? AND status = 'closed' AND winner_id IS NOT NULL",
		userID); err != nil {
		writeInternalError(w, r, err)
		return
	}
	var total sql.NullInt64
	if err := h.db.GetContext(ctx, &total,
		"SELECT SUM(winning_price) FROM auctions WHERE seller_id = ? AND status = 'closed' AND winner_id IS NOT NULL",
		userID); err != nil {
		writeInternalError(w, r, err)
		return
	}
	var live int64
	if err := h.db.GetContext(ctx, &live,
		"SELECT COUNT(*) FROM auctions WHERE seller_id = ? AND status = 'live'", userID); err != nil {
		writeInternalError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]int64{
		"listed_count": listed,
		"sold_count":   sold,
		"total_sales":  total.Int64,
		"live_count":   live,
	})
}
