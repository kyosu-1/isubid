package main

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/jmoiron/sqlx"
)

var initSQLFiles = []string{"00_schema.sql", "90_seed_phase1.sql"}

func (h *handler) postInitialize(w http.ResponseWriter, r *http.Request) {
	sqlDir := getEnv("ISUBID_SQL_DIR", "../sql")
	db, err := sqlx.Open("mysql", dbDSN(true))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer db.Close()
	for _, f := range initSQLFiles {
		b, err := os.ReadFile(filepath.Join(sqlDir, f))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := db.ExecContext(r.Context(), string(b)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"lang": "go"})
}
