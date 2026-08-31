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
		writeInternalError(w, r, err)
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

// notificationTitleMaxRunes は通知メッセージへ埋め込む出品タイトルの安全な最大長(rune数)。
//
// postAuction は title を255 runeまで許可する(webapp/go/auctions.go)が、
// notifications.message は VARCHAR(255)(webapp/sql/00_schema.sql)。通知文は
// タイトルを「」で囲んだ上に定型句を足す(outbidで+17rune、wonで+9rune。bids.go/
// closer.go参照)ため、255rune ぴったりのタイトルをそのまま使うと合計が255を超え、
// MySQL 8のデフォルト(STRICT_TRANS_TABLES)ではINSERTがエラーになる。特に
// closeAuctionではこのINSERTがトランザクション内にあるため、エラーになると
// `UPDATE ... SET status = 'closed'` ごとロールバックし、そのオークションが
// ends_at を過ぎても永久にliveのまま取り残される(closeDueAuctionsが毎秒再選出し
// 続けるだけで前に進まない)。それを避けるため、通知文に使うタイトルはここで
// 安全な長さへ切り詰める。最長サフィックス(17rune)+ 括弧2runeを足しても255を
// 十分下回る値として200を選んでいる。
const notificationTitleMaxRunes = 200

// truncateForNotification は通知メッセージに埋め込む出品タイトルを
// notificationTitleMaxRunes 以内に切り詰める(上記コメント参照)。
// 呼び出し側(bids.go の outbid通知、closer.go の won通知)では、括弧や定型句を
// 足す前の生タイトルに対して使うこと。
func truncateForNotification(title string) string {
	r := []rune(title)
	if len(r) <= notificationTitleMaxRunes {
		return title
	}
	return string(r[:notificationTitleMaxRunes])
}
