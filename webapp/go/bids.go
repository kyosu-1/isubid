package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

type bidCreated struct {
	ID        int64     `json:"id"`
	AuctionID int64     `json:"auction_id"`
	UserID    int64     `json:"user_id"`
	Amount    int64     `json:"amount"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *handler) postBid(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	auctionID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid auction id")
		return
	}
	var req struct {
		Amount int64 `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tx, err := h.db.BeginTxx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()

	// 意図的に遅い実装: オークション行ロックで全入札を直列化し、
	// ロックを握ったまま bids 全件走査で現在価格を計算する。
	var a auctionRow
	err = tx.GetContext(r.Context(), &a,
		"SELECT "+auctionColumns+" FROM auctions WHERE id = ? FOR UPDATE", auctionID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "auction not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if a.Status != "live" {
		writeError(w, http.StatusBadRequest, "auction is not live")
		return
	}
	var maxAmount sql.NullInt64
	if err := tx.GetContext(r.Context(), &maxAmount,
		"SELECT MAX(amount) FROM bids WHERE auction_id = ?", auctionID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	current := a.StartingPrice
	if maxAmount.Valid {
		current = maxAmount.Int64
	}
	if req.Amount <= current {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":         "bid amount is too low",
			"current_price": current,
		})
		return
	}
	res, err := tx.ExecContext(r.Context(),
		"INSERT INTO bids (auction_id, user_id, amount) VALUES (?, ?, ?)",
		auctionID, userID, req.Amount)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bidID, _ := res.LastInsertId()
	var createdAt time.Time
	if err := tx.GetContext(r.Context(), &createdAt,
		"SELECT created_at FROM bids WHERE id = ?", bidID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, bidCreated{
		ID: bidID, AuctionID: auctionID, UserID: userID,
		Amount: req.Amount, CreatedAt: createdAt,
	})
}
