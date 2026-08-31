package main

import (
	"database/sql"
	"errors"
	"net/http"
)

// getMe はログイン中のユーザーを返す。
//
// SPA はリロード後に「自分が誰か」を知る必要があるが、セッションは httpOnly Cookie なので
// JS からは読めない。ログイン応答をクライアント側に保存する方式は Cookie と保存値が
// 乖離しうるため、サーバーに1本聞く形にしている。
//
// ここには意図的な遅さを入れていない。全画面の初期表示が必ずこの1本を通るため、
// 遅くしても改善路が増えず、他のボトルネックの計測を濁らせるだけである。
func (h *handler) getMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	var u struct {
		ID   int64  `db:"id"`
		Name string `db:"name"`
	}
	err := h.db.GetContext(r.Context(), &u,
		"SELECT id, name FROM users WHERE id = ?", userID)
	// initialize を挟んで古いセッションが残ると、存在しない user_id を指しうる。
	// これはログインしていないのと同じ扱いにする。
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, userResponse{ID: u.ID, Name: u.Name})
}
