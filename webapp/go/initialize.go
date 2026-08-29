package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jmoiron/sqlx"
)

var initSQLFiles = []string{"00_schema.sql", "90_seed_phase1.sql"}

// auctionEndOffsets は初期化時刻からの ends_at オフセット(秒)。
// ends_at 昇順が id 昇順と一致しないよう意図的に非相関に配置してある
// (一覧の ORDER BY ends_at ASC を ORDER BY id ASC に書き換えるとベンチが落ちる)。
// +12〜+44 の5件は60秒走行中に closed へ遷移し、+3600〜+3840 の5件は走行を通して live のまま残る。
// ベンチ側の期待値(bench/validate.go)はこの表が正。
var auctionEndOffsets = map[int64]int{
	4: 12, 2: 20, 8: 28, 6: 36, 10: 44,
	1: 3600, 3: 3660, 5: 3720, 7: 3780, 9: 3840,
}

const (
	// upcoming(auction 12)は走行中ずっと upcoming のまま維持する。
	upcomingStartOffset = 300
	upcomingEndOffset   = 600
)

// applyRelativeSchedule は seed 投入後のオークション時刻を base 基準の相対値へ書き換える。
// base を1つに固定することで、ベンチが「初期化時刻 + オフセット」で期待値を組み立てられる。
func applyRelativeSchedule(ctx context.Context, db *sqlx.DB, base time.Time) error {
	for id, off := range auctionEndOffsets {
		if _, err := db.ExecContext(ctx,
			"UPDATE auctions SET starts_at = DATE_SUB(?, INTERVAL 1 HOUR), ends_at = DATE_ADD(?, INTERVAL ? SECOND) WHERE id = ?",
			base, base, off, id); err != nil {
			return err
		}
	}
	if _, err := db.ExecContext(ctx,
		"UPDATE auctions SET starts_at = DATE_ADD(?, INTERVAL ? SECOND), ends_at = DATE_ADD(?, INTERVAL ? SECOND) WHERE id = 12",
		base, upcomingStartOffset, base, upcomingEndOffset); err != nil {
		return err
	}
	return nil
}

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
	if err := applyRelativeSchedule(r.Context(), db, time.Now().UTC()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"lang": "go"})
}
