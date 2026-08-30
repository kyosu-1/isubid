package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/isucon/isucandar/agent"
)

// initializeTimeout は POST /initialize 専用のHTTPクライアントタイムアウト。
// レギュレーションは /initialize に30秒を許しており、このブランチのゲート1も
// 15秒までを合格としている。一方 ag (既定10秒)は全リクエスト共通のタイムアウトで、
// これを流用すると10〜15秒で終わる(ゲート1的には合格の)初期化がクライアント側で
// 中断され、Prepareがtransportエラーでfailする。http.Client.Timeoutはリクエストごとの
// contextでは上書きできないハードキャップなので、/initialize専用に別エージェントを
// 用意する。30秒+余裕を見て60秒とし、それ以外のリクエストは既定の10秒のままにする。
const initializeTimeout = 60 * time.Second

type Client struct {
	ag *agent.Agent
	// initAg は POST /initialize 専用。initializeTimeout を参照。
	initAg *agent.Agent
}

func NewClient(target string) (*Client, error) {
	ag, err := agent.NewAgent(
		agent.WithBaseURL(target),
		agent.WithTimeout(10*time.Second),
		agent.WithDefaultTransport(),
	)
	if err != nil {
		return nil, err
	}
	initAg, err := agent.NewAgent(
		agent.WithBaseURL(target),
		agent.WithTimeout(initializeTimeout),
		agent.WithDefaultTransport(),
	)
	if err != nil {
		return nil, err
	}
	return &Client{ag: ag, initAg: initAg}, nil
}

// doJSONWith はJSONリクエストを指定のエージェントで送り、ステータスとボディを返す。
func (c *Client) doJSONWith(ctx context.Context, ag *agent.Agent, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := ag.NewRequest(method, path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := ag.Do(ctx, req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, nil, err
	}
	return res.StatusCode, b, nil
}

// doJSON はJSONリクエストを既定(10秒)のエージェントで送り、ステータスとボディを返す。
func (c *Client) doJSON(ctx context.Context, method, path string, body any) (int, []byte, error) {
	return c.doJSONWith(ctx, c.ag, method, path, body)
}

// doRaw は生のGETを送り、ステータス・Content-Type・本文を返す。
// 画像のようにJSONでない応答を扱うために使う。
func (c *Client) doRaw(ctx context.Context, path string) (int, string, []byte, error) {
	req, err := c.ag.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		return 0, "", nil, err
	}
	res, err := c.ag.Do(ctx, req)
	if err != nil {
		return 0, "", nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, "", nil, err
	}
	return res.StatusCode, res.Header.Get("Content-Type"), b, nil
}

// GetUserIcon はアイコンを取得し、ステータス・Content-Type・本文を返す。
// 404 はアイコン未設定・ユーザー不在の正常な応答なので、エラーにはしない。
func (c *Client) GetUserIcon(ctx context.Context, id int64) (int, string, []byte, error) {
	return c.doRaw(ctx, fmt.Sprintf("/users/%d/icon", id))
}

func (c *Client) Initialize(ctx context.Context) (string, error) {
	code, b, err := c.doJSONWith(ctx, c.initAg, http.MethodPost, "/initialize", map[string]string{})
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("POST /initialize: status %d (body: %s)", code, b)
	}
	var body struct {
		Lang string `json:"lang"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return "", fmt.Errorf("POST /initialize: 不正なJSON: %w", err)
	}
	return body.Lang, nil
}

func (c *Client) auth(ctx context.Context, path, name, password string, wantCode int) (*User, error) {
	code, b, err := c.doJSON(ctx, http.MethodPost, path, map[string]string{
		"name": name, "password": password,
	})
	if err != nil {
		return nil, err
	}
	if code != wantCode {
		return nil, fmt.Errorf("POST %s: status %d (期待: %d, body: %s)", path, code, wantCode, b)
	}
	var u User
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, fmt.Errorf("POST %s: 不正なJSON: %w", path, err)
	}
	return &u, nil
}

func (c *Client) Register(ctx context.Context, name, password string) (*User, error) {
	return c.auth(ctx, "/register", name, password, http.StatusCreated)
}

func (c *Client) Login(ctx context.Context, name, password string) (*User, error) {
	return c.auth(ctx, "/login", name, password, http.StatusOK)
}

// AuctionListParams は GET /auctions のクエリ。ゼロ値は「page 未指定・絞り込み無し」。
type AuctionListParams struct {
	Page     int    // 0 なら page を送らない
	Q        string // 空なら q を送らない
	Category int64  // 0 なら category を送らない
}

// query はクエリ文字列を組み立てる(値はキー名の辞書順に並ぶ)。
func (p AuctionListParams) query() string {
	v := url.Values{}
	if p.Page != 0 {
		v.Set("page", strconv.Itoa(p.Page))
	}
	if p.Q != "" {
		v.Set("q", p.Q)
	}
	if p.Category != 0 {
		v.Set("category", strconv.FormatInt(p.Category, 10))
	}
	return v.Encode()
}

func (c *Client) GetAuctions(ctx context.Context, p AuctionListParams) (*AuctionList, error) {
	path := "/auctions"
	if q := p.query(); q != "" {
		path += "?" + q
	}
	code, b, err := c.doJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d (body: %s)", path, code, b)
	}
	var l AuctionList
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("GET %s: 不正なJSON: %w", path, err)
	}
	if l.Auctions == nil {
		return nil, fmt.Errorf("GET %s: auctions が null (期待: 空配列でも [])", path)
	}
	return &l, nil
}

// GetAuctionsRaw は生のクエリ文字列を送り、ステータスコードだけを返す。
// 不正値が 400 になることの検証に使う(ボディの形は問わない)。
func (c *Client) GetAuctionsRaw(ctx context.Context, rawQuery string) (int, error) {
	code, _, err := c.doJSON(ctx, http.MethodGet, "/auctions?"+rawQuery, nil)
	return code, err
}

func (c *Client) GetAuction(ctx context.Context, id int64) (*AuctionDetail, error) {
	path := fmt.Sprintf("/auctions/%d", id)
	code, b, err := c.doJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d (body: %s)", path, code, b)
	}
	var d AuctionDetail
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("GET %s: 不正なJSON: %w", path, err)
	}
	return &d, nil
}

// GetAuctionRetry は GetAuction を最大 attempts 回、brief backoff を挟んで再試行する。
// Validationフェーズでの単発の一過性エラー(GC/瞬断等)がそのままcritical化するのを避けるため
// (M1: 安価な追加耐性)。
func (c *Client) GetAuctionRetry(ctx context.Context, id int64, attempts int, backoff time.Duration) (*AuctionDetail, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		d, err := c.GetAuction(ctx, id)
		if err == nil {
			return d, nil
		}
		lastErr = err
		if i < attempts-1 {
			select {
			case <-ctx.Done():
				return nil, lastErr
			case <-time.After(backoff):
			}
		}
	}
	return nil, lastErr
}

// PostBid は入札する。4xxはエラーではなくステータスコードで返す(検証側で判断)。
func (c *Client) PostBid(ctx context.Context, auctionID, amount int64) (*BidCreated, int, error) {
	path := fmt.Sprintf("/auctions/%d/bids", auctionID)
	code, b, err := c.doJSON(ctx, http.MethodPost, path, map[string]int64{"amount": amount})
	if err != nil {
		return nil, 0, err
	}
	if code >= http.StatusInternalServerError {
		return nil, code, fmt.Errorf("POST %s: status %d (body: %s)", path, code, b)
	}
	if code != http.StatusCreated {
		return nil, code, nil
	}
	var bid BidCreated
	if err := json.Unmarshal(b, &bid); err != nil {
		return nil, code, fmt.Errorf("POST %s: 不正なJSON: %w", path, err)
	}
	return &bid, code, nil
}

// GetNotifications は自分宛の通知一覧を取得する。
func (c *Client) GetNotifications(ctx context.Context) ([]Notification, error) {
	code, b, err := c.doJSON(ctx, http.MethodGet, "/notifications", nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET /notifications: status %d (body: %s)", code, b)
	}
	var body struct {
		Notifications []Notification `json:"notifications"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, fmt.Errorf("GET /notifications: 不正なJSON: %w", err)
	}
	return body.Notifications, nil
}

// GetBidFeed は入札フィードを取得する。since より大きい id の入札が id 昇順で返る。
func (c *Client) GetBidFeed(ctx context.Context, auctionID, since int64) ([]Bid, error) {
	path := fmt.Sprintf("/auctions/%d/bids?since=%d", auctionID, since)
	code, b, err := c.doJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d (body: %s)", path, code, b)
	}
	var body struct {
		Bids []Bid `json:"bids"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, fmt.Errorf("GET %s: 不正なJSON: %w", path, err)
	}
	return body.Bids, nil
}

// PostAuction は出品する。PostBid と同様、4xxはエラーではなくステータスコードで返す
// (呼び出し側が「結果不明(転送エラー/5xx)」と「確定的に未コミット(4xx)」を区別できるようにする)。
func (c *Client) PostAuction(ctx context.Context, title, description string,
	categoryID, startingPrice, durationSeconds int64) (*AuctionCreated, int, error) {
	code, b, err := c.doJSON(ctx, http.MethodPost, "/auctions", map[string]any{
		"title": title, "description": description, "category_id": categoryID,
		"starting_price": startingPrice, "duration_seconds": durationSeconds,
	})
	if err != nil {
		return nil, 0, err
	}
	if code >= http.StatusInternalServerError {
		return nil, code, fmt.Errorf("POST /auctions: status %d (body: %s)", code, b)
	}
	if code != http.StatusCreated {
		return nil, code, nil
	}
	var a AuctionCreated
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, code, fmt.Errorf("POST /auctions: 不正なJSON: %w", err)
	}
	return &a, code, nil
}

// GetStatsMe は出品者の売上サマリを取得する。
func (c *Client) GetStatsMe(ctx context.Context) (*Stats, error) {
	code, b, err := c.doJSON(ctx, http.MethodGet, "/stats/me", nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET /stats/me: status %d (body: %s)", code, b)
	}
	var s Stats
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("GET /stats/me: 不正なJSON: %w", err)
	}
	return &s, nil
}
