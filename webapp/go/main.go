package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"
)

type handler struct {
	db *sqlx.DB
}

func newRouter(db *sqlx.DB) http.Handler {
	return routerFor(&handler{db: db})
}

// routerFor は handler からルーターを組み立てる。
// main はバッチ用に handler を先に作る必要があるため分離している。
//
// API は /api 配下に置く。SPA のクライアントルート(/auctions/123 など)と
// API のパスが同一になると、chi が API ハンドラを先にマッチさせてしまい
// ディープリンクが JSON を返す。ISUCON11/12/13 が同じ理由で同じ形を採っている。
//
// POST /initialize も例外にしない。ここだけ /api の外に出すと nginx の分担
// (location /api/ → app / それ以外は静的)が割れなくなる。
func routerFor(h *handler) http.Handler {
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Post("/initialize", h.postInitialize)
		r.Post("/register", h.postRegister)
		r.Post("/login", h.postLogin)
		r.Get("/me", h.getMe)
		r.Get("/auctions", h.getAuctions)
		r.Post("/auctions", h.postAuction)
		r.Get("/auctions/{id}", h.getAuction)
		r.Get("/auctions/{id}/bids", h.getAuctionBids)
		r.Post("/auctions/{id}/bids", h.postBid)
		r.Get("/notifications", h.getNotifications)
		r.Get("/users/{id}/icon", h.getUserIcon)
		r.Get("/stats/me", h.getStatsMe)
	})
	// /api 以外はすべて静的配信(SPA)へ回す。
	r.NotFound(h.serveStatic)
	return r
}

func main() {
	db, err := connectDB()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	h := &handler{db: db}
	// 終了処理バッチ。テスト(newRouter経由)では起動しない。
	go h.runAuctionCloser(context.Background())
	addr := ":" + getEnv("ISUBID_PORT", "8000")
	log.Printf("isubid listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, routerFor(h)))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// writeInternalError は500応答を返す。
//
// err にはSQL文やドライバのメッセージなど内部の詳細が含まれうるため、クライアントへは
// 一般化したメッセージだけを返し、詳細はサーバーログにのみ出す。参加者はログでデバッグ
// できる一方、応答本文からテーブル名やクエリ内容などの内部情報が漏れないようにする。
func writeInternalError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("%s %s: internal error: %v", r.Method, r.URL.Path, err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}
