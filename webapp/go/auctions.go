package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

type auctionRow struct {
	ID            int64     `db:"id"`
	SellerID      int64     `db:"seller_id"`
	CategoryID    int64     `db:"category_id"`
	Title         string    `db:"title"`
	Description   string    `db:"description"`
	StartingPrice int64     `db:"starting_price"`
	StartsAt      time.Time `db:"starts_at"`
	EndsAt        time.Time `db:"ends_at"`
	Status        string    `db:"status"`
}

const auctionColumns = "id, seller_id, category_id, title, description, starting_price, starts_at, ends_at, status"

type auctionSummary struct {
	ID           int64        `json:"id"`
	Title        string       `json:"title"`
	CategoryID   int64        `json:"category_id"`
	Seller       userResponse `json:"seller"`
	CurrentPrice int64        `json:"current_price"`
	BidCount     int64        `json:"bid_count"`
	StartsAt     time.Time    `json:"starts_at"`
	EndsAt       time.Time    `json:"ends_at"`
	Status       string       `json:"status"`
}

type bidResponse struct {
	ID        int64        `json:"id"`
	User      userResponse `json:"user"`
	Amount    int64        `json:"amount"`
	CreatedAt time.Time    `json:"created_at"`
}

type auctionDetail struct {
	auctionSummary
	Description   string        `json:"description"`
	StartingPrice int64         `json:"starting_price"`
	Bids          []bidResponse `json:"bids"`
}

// summarize は1オークションあたり3クエリを発行する。意図的に遅い実装(N+1)。
func (h *handler) summarize(r *http.Request, a *auctionRow) (*auctionSummary, error) {
	var maxAmount sql.NullInt64
	if err := h.db.GetContext(r.Context(), &maxAmount,
		"SELECT MAX(amount) FROM bids WHERE auction_id = ?", a.ID); err != nil {
		return nil, err
	}
	price := a.StartingPrice
	if maxAmount.Valid {
		price = maxAmount.Int64
	}
	var bidCount int64
	if err := h.db.GetContext(r.Context(), &bidCount,
		"SELECT COUNT(*) FROM bids WHERE auction_id = ?", a.ID); err != nil {
		return nil, err
	}
	var seller userResponse
	if err := h.db.GetContext(r.Context(), &seller,
		"SELECT id, name FROM users WHERE id = ?", a.SellerID); err != nil {
		return nil, err
	}
	return &auctionSummary{
		ID: a.ID, Title: a.Title, CategoryID: a.CategoryID, Seller: seller,
		CurrentPrice: price, BidCount: bidCount,
		StartsAt: a.StartsAt, EndsAt: a.EndsAt, Status: a.Status,
	}, nil
}

func (h *handler) getAuctions(w http.ResponseWriter, r *http.Request) {
	var rows []auctionRow
	if err := h.db.SelectContext(r.Context(), &rows,
		"SELECT "+auctionColumns+" FROM auctions WHERE status = 'live' ORDER BY ends_at ASC, id ASC"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	summaries := make([]auctionSummary, 0, len(rows))
	for i := range rows {
		s, err := h.summarize(r, &rows[i]) // 意図的に遅い実装(N+1)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		summaries = append(summaries, *s)
	}
	writeJSON(w, http.StatusOK, summaries)
}

func (h *handler) getAuction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid auction id")
		return
	}
	var a auctionRow
	err = h.db.GetContext(r.Context(), &a,
		"SELECT "+auctionColumns+" FROM auctions WHERE id = ?", id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "auction not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s, err := h.summarize(r, &a)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var bidRows []struct {
		ID        int64     `db:"id"`
		UserID    int64     `db:"user_id"`
		Amount    int64     `db:"amount"`
		CreatedAt time.Time `db:"created_at"`
	}
	if err := h.db.SelectContext(r.Context(), &bidRows,
		"SELECT id, user_id, amount, created_at FROM bids WHERE auction_id = ? ORDER BY created_at DESC, id DESC", id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bids := make([]bidResponse, 0, len(bidRows))
	for _, b := range bidRows {
		var u userResponse
		// 意図的に遅い実装(N+1): 入札ごとにユーザーを引く
		if err := h.db.GetContext(r.Context(), &u,
			"SELECT id, name FROM users WHERE id = ?", b.UserID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		bids = append(bids, bidResponse{ID: b.ID, User: u, Amount: b.Amount, CreatedAt: b.CreatedAt})
	}
	writeJSON(w, http.StatusOK, auctionDetail{
		auctionSummary: *s,
		Description:    a.Description,
		StartingPrice:  a.StartingPrice,
		Bids:           bids,
	})
}
