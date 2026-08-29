package main

import (
	"net/http"
	"time"
)

type notificationResponse struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	AuctionID int64     `json:"auction_id"`
	Message   string    `json:"message"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
}

// getNotifications は自分宛の通知を id 降順で返す。
// 既読化APIは用意していない(is_read は常に false)。
//
// 意図的に遅い実装: notifications に user_id のインデックスが無くフルスキャンになる。
func (h *handler) getNotifications(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	var rows []struct {
		ID        int64     `db:"id"`
		Type      string    `db:"type"`
		AuctionID int64     `db:"auction_id"`
		Message   string    `db:"message"`
		IsRead    bool      `db:"is_read"`
		CreatedAt time.Time `db:"created_at"`
	}
	if err := h.db.SelectContext(r.Context(), &rows,
		"SELECT id, type, auction_id, message, is_read, created_at FROM notifications WHERE user_id = ? ORDER BY id DESC",
		userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]notificationResponse, 0, len(rows))
	for _, n := range rows {
		out = append(out, notificationResponse{
			ID: n.ID, Type: n.Type, AuctionID: n.AuctionID,
			Message: n.Message, IsRead: n.IsRead, CreatedAt: n.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out})
}
