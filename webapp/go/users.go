package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// getUserIcon はユーザーアイコンを返す。
//
// アイコンあり → 200 + image/png、icon IS NULL → 404、ユーザー不在 → 404、
// 非数値 id → 400。
func (h *handler) getUserIcon(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	// 意図的に遅い実装: リクエストのたびに LONGBLOB を丸ごと DB から読む。
	// 画像を配るのに毎回 MySQL へ往復している。一覧1ページ(20件)を開くと
	// 最大20回この経路を通るため、静的配信へ外出しするのが想定の攻略線。
	var icon []byte
	err = h.db.GetContext(r.Context(), &icon, "SELECT icon FROM users WHERE id = ?", id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if len(icon) == 0 {
		// icon IS NULL。アイコン未設定のユーザーは 404 を返す。
		writeError(w, http.StatusNotFound, "icon not found")
		return
	}

	// 意図的に遅い実装: Cache-Control / ETag / Last-Modified を一切付けない。
	// クライアントは毎回取り直すことになる。
	w.Header().Set("Content-Type", "image/png")
	w.WriteHeader(http.StatusOK)
	w.Write(icon)
}
