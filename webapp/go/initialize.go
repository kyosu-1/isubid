package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
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

// seedMaxAuctionID は webapp/sql/90_seed_phase1.sql が占める auction id の上端。
// 生成データは id 13 から採番される(initial-data/config.go の SeedMaxAuctionID と揃えること)。
const seedMaxAuctionID = 12

// generatedEpochLiteral は生成データが live/upcoming の時刻を保持する固定基準。
// initial-data/generate.go の generatedEpoch と一致させること。
//
// 意図的に未来日付にしてある(過去日付に「整地」してはいけない)。ダンプ投入直後、
// このUPDATEが走るより前の一瞬、生成 live オークションの ends_at はこのエポック起点の
// オフセットそのままの値になる。エポックが過去日付だと、その一瞬を runAuctionCloser
// (毎秒 status='live' AND ends_at<=NOW(6) を閉じるバッチ) が拾って全件を期限切れとみなし、
// won 通知を auto-increment id で挿入してしまう。すると notifications の採番カウンタが
// 1を超え、後続の 94_notifications.sql が id=1 から明示挿入する際に Duplicate entry で
// 衝突する(確率的に発生する初期化失敗)。
const generatedEpochLiteral = "2100-01-01 00:00:00"

// applyGeneratedSchedule は生成データの live/upcoming を base 基準の時刻へ付け替える。
//
// WHERE id > seedMaxAuctionID が必須である。シード(id 1〜12)は applyRelativeSchedule で
// 既に「現在時刻＋オフセット」の絶対時刻になっており、ここで固定エポック起点の変換を
// 当てると TIMESTAMPDIFF が約8億3600万秒となり ends_at が2052年へ飛ぶ。
//
// closed は過去データなので書き換えない(走行時刻に依存しない)。
func applyGeneratedSchedule(ctx context.Context, db *sqlx.DB, base time.Time) error {
	_, err := db.ExecContext(ctx,
		"UPDATE auctions SET "+
			"starts_at = DATE_ADD(?, INTERVAL TIMESTAMPDIFF(SECOND, ?, starts_at) SECOND), "+
			"ends_at   = DATE_ADD(?, INTERVAL TIMESTAMPDIFF(SECOND, ?, ends_at)   SECOND) "+
			"WHERE id > ? AND status IN ('live','upcoming')",
		base, generatedEpochLiteral, base, generatedEpochLiteral, seedMaxAuctionID)
	return err
}

// initScriptTimeout は loadViaInitScript が init.sh に許す上限時間。
// リクエストのキャンセルとは無関係な、この処理専用の打ち切りである。
const initScriptTimeout = 10 * time.Minute

// loadViaInitScript は init.sh に投入を委譲する(mysql クライアントでのバルクロード)。
//
// リクエストの context は受け取らない。exec.CommandContext が Kill するのは
// 直接の子(sh)だけで、init.sh の run() がダンプファイルごとに起動する mysql の
// 孫プロセスには届かない。dev/nginx.conf は proxy_read_timeout を設定しておらず
// 既定の60秒でnginxが上流接続を切るため、大きいスケールのロードはそれを
// 超えうる。そこで r.Context() をここに渡すと、切断でキャンセルされた瞬間に
// sh だけが死んで mysql が生き残り、デタッチされたまま書き込みを続ける。
// POST /initialize は 00_schema.sql の DROP TABLE から始まる破壊的な全入れ替えで、
// 完走すれば一貫した状態になるが、志半ばで打ち切られたロードはオーファン化した
// mysql が次の初期化の新テーブルに書き込みを続け、競合を起こす。そのため
// 打ち切りはリクエストから独立させ、十分に長い時間(initScriptTimeout)を許す。
func loadViaInitScript(sqlDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), initScriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", filepath.Join(sqlDir, "init.sh"))
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("init.sh: %w: %s", err, out)
	}
	return nil
}

// loadViaGo は Go でスキーマとシードだけを流す(生成データ非搭載時)。
// ホストに mysql クライアントが無い環境でも webapp/go のテストが動くよう、この経路を残す。
func loadViaGo(ctx context.Context, db *sqlx.DB, sqlDir string) error {
	for _, f := range initSQLFiles {
		b, err := os.ReadFile(filepath.Join(sqlDir, f))
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, string(b)); err != nil {
			return err
		}
	}
	return nil
}

func (h *handler) postInitialize(w http.ResponseWriter, r *http.Request) {
	sqlDir := getEnv("ISUBID_SQL_DIR", "../sql")
	generatedDir := os.Getenv("ISUBID_INITIAL_DATA_DIR")

	db, err := sqlx.Open("mysql", dbDSN(true))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer db.Close()

	if generatedDir != "" {
		if err := loadViaInitScript(sqlDir); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		if err := loadViaGo(r.Context(), db, sqlDir); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	base := time.Now().UTC()
	if err := applyRelativeSchedule(r.Context(), db, base); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if generatedDir != "" {
		if err := applyGeneratedSchedule(r.Context(), db, base); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"lang": "go"})
}
