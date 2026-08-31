package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// queryer は *sqlx.DB と *sqlx.Tx の両方が満たす、読み取りに必要な最小インターフェース。
// summarize をトランザクション内(一貫したスナップショット)でも単独でも使えるようにする。
type queryer interface {
	GetContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
	SelectContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
}

type auctionRow struct {
	ID            int64         `db:"id"`
	SellerID      int64         `db:"seller_id"`
	CategoryID    int64         `db:"category_id"`
	Title         string        `db:"title"`
	Description   string        `db:"description"`
	StartingPrice int64         `db:"starting_price"`
	StartsAt      time.Time     `db:"starts_at"`
	EndsAt        time.Time     `db:"ends_at"`
	Status        string        `db:"status"`
	WinnerID      sql.NullInt64 `db:"winner_id"`
	WinningPrice  sql.NullInt64 `db:"winning_price"`
}

const auctionColumns = "id, seller_id, category_id, title, description, starting_price, starts_at, ends_at, status, winner_id, winning_price"

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
	WinnerID      *int64        `json:"winner_id"`
	WinningPrice  *int64        `json:"winning_price"`
	Bids          []bidResponse `json:"bids"`
}

// summarize は1オークションあたり3クエリを発行する。意図的に遅い実装(N+1)。
// q に *sqlx.Tx を渡すと、呼び出し元が同一トランザクション(MySQLデフォルトの
// REPEATABLE READ)内でスナップショットを共有でき、他クエリとの読み取り一貫性を保てる。
func (h *handler) summarize(ctx context.Context, q queryer, a *auctionRow) (*auctionSummary, error) {
	var maxAmount sql.NullInt64
	if err := q.GetContext(ctx, &maxAmount,
		"SELECT MAX(amount) FROM bids WHERE auction_id = ?", a.ID); err != nil {
		return nil, err
	}
	price := a.StartingPrice
	if maxAmount.Valid {
		price = maxAmount.Int64
	}
	var bidCount int64
	if err := q.GetContext(ctx, &bidCount,
		"SELECT COUNT(*) FROM bids WHERE auction_id = ?", a.ID); err != nil {
		return nil, err
	}
	var seller userResponse
	if err := q.GetContext(ctx, &seller,
		"SELECT id, name FROM users WHERE id = ?", a.SellerID); err != nil {
		return nil, err
	}
	return &auctionSummary{
		ID: a.ID, Title: a.Title, CategoryID: a.CategoryID, Seller: seller,
		CurrentPrice: price, BidCount: bidCount,
		StartsAt: a.StartsAt, EndsAt: a.EndsAt, Status: a.Status,
	}, nil
}

// nullInt64Ptr は sql.NullInt64 を JSON の null 可能な *int64 に変換する。
func nullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

// auctionsPerPage は一覧1ページあたりの件数。
// bench/validate.go の同名定数と手で揃えること(モジュールが別なので
// コンパイル時に照合する手段が無い)。
const auctionsPerPage = 20

type auctionListResponse struct {
	Auctions   []auctionSummary `json:"auctions"`
	TotalCount int64            `json:"total_count"`
	HasNext    bool             `json:"has_next"`
}

// auctionListQuery は GET /auctions のクエリパラメータ。
type auctionListQuery struct {
	Page     int64
	Q        string
	Category sql.NullInt64
}

// parseAuctionListQuery はクエリ文字列を解釈する。エラーを返した場合は 400 にする。
// 値が空文字のパラメータは「未指定」として扱う(?page= と page 省略を同一視する)。
func parseAuctionListQuery(v url.Values) (auctionListQuery, error) {
	q := auctionListQuery{Page: 1}
	if s := v.Get("page"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		// 上限を math.MaxInt64/auctionsPerPage で切る: これを超えると
		// (q.Page-1)*auctionsPerPage や q.Page*auctionsPerPage が int64 を
		// 溢れ、OFFSET が負値になって MySQL エラー(500)を引き起こす。
		if err != nil || n < 1 || n > math.MaxInt64/auctionsPerPage {
			return q, errors.New("invalid page")
		}
		q.Page = n
	}
	q.Q = v.Get("q")
	if len([]rune(q.Q)) > 255 {
		return q, errors.New("invalid q")
	}
	if s := v.Get("category"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return q, errors.New("invalid category")
		}
		q.Category = sql.NullInt64{Int64: n, Valid: true}
	}
	return q, nil
}

// where は WHERE 句とバインド引数を組み立てる。COUNT と SELECT の両方が同じものを使う。
func (q auctionListQuery) where() (string, []any) {
	cond := "status = 'live'"
	args := []any{}
	if q.Q != "" {
		// 意図的に遅い実装: 先頭ワイルドカードの LIKE は B-tree インデックスが
		// 原理的に使えず、必ず全行スキャンになる。title と description の両方を
		// 対象にすることで1行あたりの比較コストも上げている。
		like := "%" + escapeLike(q.Q) + "%"
		cond += " AND (title LIKE ? OR description LIKE ?)"
		args = append(args, like, like)
	}
	if q.Category.Valid {
		// 意図的に遅い実装: category_id にインデックスが無い。
		cond += " AND category_id = ?"
		args = append(args, q.Category.Int64)
	}
	return cond, args
}

// escapeLike は LIKE パターン中で特別な意味を持つ文字をエスケープする。
// strings.NewReplacer は入力を1パスで走査し置換結果を再走査しないため、
// この3文字(\ % _)の置換順序には依存しない。
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (h *handler) getAuctions(w http.ResponseWriter, r *http.Request) {
	q, err := parseAuctionListQuery(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cond, args := q.where()

	// total_count と auctions を単一トランザクション(MySQLデフォルトの REPEATABLE READ)の
	// スナップショットから読む。別々に読むと、COUNT と SELECT のあいだに入札や終了処理が
	// commit された場合に「total_count は 137 なのに全ページ合計は 138 件」が
	// 正しい実装でも起きてしまう。意図的なN+1構成はそのまま維持し、読み取り一貫性のみ確保する。
	tx, err := h.db.BeginTxx(r.Context(), nil)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	defer tx.Rollback()

	var total int64
	// 意図的に遅い実装: 検索条件つきの COUNT が SELECT と同じ WHERE をもう一度走る
	// (LIKE 検索時はフルスキャンが2回になる)。
	if err := tx.GetContext(r.Context(), &total,
		"SELECT COUNT(*) FROM auctions WHERE "+cond, args...); err != nil {
		writeInternalError(w, r, err)
		return
	}

	// 意図的に遅い実装: status / ends_at にインデックスが無いため全スキャン + filesort。
	// OFFSET が深いほど読み捨てる行が増える。
	pageArgs := append(append([]any{}, args...), auctionsPerPage, (q.Page-1)*auctionsPerPage)
	var rows []auctionRow
	if err := tx.SelectContext(r.Context(), &rows,
		"SELECT "+auctionColumns+" FROM auctions WHERE "+cond+
			" ORDER BY ends_at ASC, id ASC LIMIT ? OFFSET ?", pageArgs...); err != nil {
		writeInternalError(w, r, err)
		return
	}

	summaries := make([]auctionSummary, 0, len(rows))
	for i := range rows {
		s, err := h.summarize(r.Context(), tx, &rows[i]) // 意図的に遅い実装(N+1)
		if err != nil {
			writeInternalError(w, r, err)
			return
		}
		summaries = append(summaries, *s)
	}
	writeJSON(w, http.StatusOK, auctionListResponse{
		Auctions:   summaries,
		TotalCount: total,
		HasNext:    q.Page*auctionsPerPage < total,
	})
}

func (h *handler) getAuction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid auction id")
		return
	}
	// 応答全体(current_price/bid_count/bids)を単一トランザクションのスナップショットから
	// 組み立てる。個別クエリのままだと、組み立て中に他リクエストの入札がcommitされた場合
	// bid_count と bids件数が食い違う(意図的なN+1構成はそのまま維持しつつ読み取り一貫性のみ確保)。
	tx, err := h.db.BeginTxx(r.Context(), nil)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	defer tx.Rollback()

	var a auctionRow
	err = tx.GetContext(r.Context(), &a,
		"SELECT "+auctionColumns+" FROM auctions WHERE id = ?", id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "auction not found")
		return
	}
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	s, err := h.summarize(r.Context(), tx, &a)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	var bidRows []struct {
		ID        int64     `db:"id"`
		UserID    int64     `db:"user_id"`
		Amount    int64     `db:"amount"`
		CreatedAt time.Time `db:"created_at"`
	}
	if err := tx.SelectContext(r.Context(), &bidRows,
		"SELECT id, user_id, amount, created_at FROM bids WHERE auction_id = ? ORDER BY created_at DESC, id DESC", id); err != nil {
		writeInternalError(w, r, err)
		return
	}
	bids := make([]bidResponse, 0, len(bidRows))
	for _, b := range bidRows {
		var u userResponse
		// 意図的に遅い実装(N+1): 入札ごとにユーザーを引く
		if err := tx.GetContext(r.Context(), &u,
			"SELECT id, name FROM users WHERE id = ?", b.UserID); err != nil {
			writeInternalError(w, r, err)
			return
		}
		bids = append(bids, bidResponse{ID: b.ID, User: u, Amount: b.Amount, CreatedAt: b.CreatedAt})
	}
	writeJSON(w, http.StatusOK, auctionDetail{
		auctionSummary: *s,
		Description:    a.Description,
		StartingPrice:  a.StartingPrice,
		WinnerID:       nullInt64Ptr(a.WinnerID),
		WinningPrice:   nullInt64Ptr(a.WinningPrice),
		Bids:           bids,
	})
}

// postAuction は新規出品。出品と同時に live になる(upcoming を経由しない)。
func (h *handler) postAuction(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	var req struct {
		Title           string `json:"title"`
		Description     string `json:"description"`
		CategoryID      int64  `json:"category_id"`
		StartingPrice   int64  `json:"starting_price"`
		DurationSeconds int64  `json:"duration_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" || len([]rune(req.Title)) > 255 {
		writeError(w, http.StatusBadRequest, "invalid title")
		return
	}
	if req.StartingPrice < 1 {
		writeError(w, http.StatusBadRequest, "invalid starting_price")
		return
	}
	if req.DurationSeconds < 10 || req.DurationSeconds > 300 {
		writeError(w, http.StatusBadRequest, "invalid duration_seconds")
		return
	}
	var exists int
	err := h.db.GetContext(r.Context(), &exists, "SELECT 1 FROM categories WHERE id = ?", req.CategoryID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusBadRequest, "invalid category_id")
		return
	}
	if err != nil {
		writeInternalError(w, r, err)
		return
	}

	now := time.Now().UTC()
	endsAt := now.Add(time.Duration(req.DurationSeconds) * time.Second)
	res, err := h.db.ExecContext(r.Context(),
		"INSERT INTO auctions (seller_id, category_id, title, description, starting_price, starts_at, ends_at, status) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, 'live')",
		userID, req.CategoryID, req.Title, req.Description, req.StartingPrice, now, endsAt)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":             id,
		"title":          req.Title,
		"starting_price": req.StartingPrice,
		"ends_at":        endsAt,
		"status":         "live",
	})
}
