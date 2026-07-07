package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/isucon/isucandar/agent"
)

type Client struct {
	ag *agent.Agent
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
	return &Client{ag: ag}, nil
}

// doJSON はJSONリクエストを送り、ステータスとボディを返す。
func (c *Client) doJSON(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := c.ag.NewRequest(method, path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.ag.Do(ctx, req)
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

func (c *Client) Initialize(ctx context.Context) (string, error) {
	code, b, err := c.doJSON(ctx, http.MethodPost, "/initialize", map[string]string{})
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

func (c *Client) GetAuctions(ctx context.Context) ([]AuctionSummary, error) {
	code, b, err := c.doJSON(ctx, http.MethodGet, "/auctions", nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("GET /auctions: status %d (body: %s)", code, b)
	}
	var list []AuctionSummary
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("GET /auctions: 不正なJSON: %w", err)
	}
	return list, nil
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
