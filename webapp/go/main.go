package main

import (
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
	h := &handler{db: db}
	r := chi.NewRouter()
	r.Post("/initialize", h.postInitialize)
	return r
}

func main() {
	db, err := connectDB()
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	addr := ":" + getEnv("ISUBID_PORT", "8000")
	log.Printf("isubid listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, newRouter(db)))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
