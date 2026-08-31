package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// getAuctionBids は入札フィード。since より大きい id の入札を id 昇順で返す。
//
// bids.id は AUTO_INCREMENT であり、入札APIはオークション行を FOR UPDATE で
// 保持したまま INSERT するため、同一オークション内では id 順 = コミット順になる。
// したがって「id > since」のカーソルは取りこぼしも重複も起こさない。
//
// 意図的に遅い実装: bids に auction_id のインデックスが無くフルスキャンになり、
// さらに入札ごとにユーザーを引く(N+1)。
func (h *handler) getAuctionBids(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid auction id")
		return
	}
	var since int64
	if s := r.URL.Query().Get("since"); s != "" {
		since, err = strconv.ParseInt(s, 10, 64)
		if err != nil || since < 0 {
			writeError(w, http.StatusBadRequest, "invalid since")
			return
		}
	}

	var exists int
	err = h.db.GetContext(r.Context(), &exists, "SELECT 1 FROM auctions WHERE id = ?", id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "auction not found")
		return
	}
	if err != nil {
		writeInternalError(w, r, err)
		return
	}

	var rows []struct {
		ID        int64     `db:"id"`
		UserID    int64     `db:"user_id"`
		Amount    int64     `db:"amount"`
		CreatedAt time.Time `db:"created_at"`
	}
	if err := h.db.SelectContext(r.Context(), &rows,
		"SELECT id, user_id, amount, created_at FROM bids WHERE auction_id = ? AND id > ? ORDER BY id ASC",
		id, since); err != nil {
		writeInternalError(w, r, err)
		return
	}
	bids := make([]bidResponse, 0, len(rows))
	for _, b := range rows {
		var u userResponse
		// 意図的に遅い実装(N+1): 入札ごとにユーザーを引く
		if err := h.db.GetContext(r.Context(), &u,
			"SELECT id, name FROM users WHERE id = ?", b.UserID); err != nil {
			writeInternalError(w, r, err)
			return
		}
		bids = append(bids, bidResponse{ID: b.ID, User: u, Amount: b.Amount, CreatedAt: b.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"bids": bids})
}
