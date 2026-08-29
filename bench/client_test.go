package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// PostAuction は PostBid と同様、4xxをエラーではなくステータスコードで返すこと
// (FIX3: 呼び出し側が「結果不明(転送エラー/5xx)」と「確定的に未コミット(4xx)」を
// 区別できるようにするため)を確認する。
func TestPostAuctionStatusCodes(t *testing.T) {
	for _, tt := range []struct {
		name       string
		statusCode int
		body       string
		wantErr    bool
	}{
		{"201は成功としてAuctionCreatedを返す", http.StatusCreated,
			`{"id":1,"title":"t","starting_price":1000,"ends_at":"2024-01-01T00:00:00Z","status":"live"}`, false},
		{"400は確定的な未コミット、エラーにしない", http.StatusBadRequest, `{"error":"invalid title"}`, false},
		{"401は確定的な未コミット、エラーにしない", http.StatusUnauthorized, `{"error":"login required"}`, false},
		{"500は結果不明、エラーを返す", http.StatusInternalServerError, `{"error":"boom"}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer ts.Close()

			c, err := NewClient(ts.URL)
			if err != nil {
				t.Fatal(err)
			}
			created, code, err := c.PostAuction(context.Background(), "t", "d", 1, 1000, 30)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if code != tt.statusCode {
				t.Errorf("code = %d, want %d", code, tt.statusCode)
			}
			if tt.statusCode == http.StatusCreated {
				if created == nil {
					t.Fatal("201のときcreatedがnil")
				}
				if created.ID != 1 || created.Status != "live" {
					t.Errorf("created = %+v, 応答のパースが不正", created)
				}
			} else if created != nil {
				t.Errorf("非201でcreatedが%+vを返した、want nil", created)
			}
		})
	}
}

// 転送エラー(サーバーに到達できない)はステータスコード0でエラーを返す。
func TestPostAuctionTransportError(t *testing.T) {
	// 即座にリスンを閉じ、接続不能なアドレスへ向ける。
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	target := ts.URL
	ts.Close() // 直後に閉じることで接続不能にする

	c, err := NewClient(target)
	if err != nil {
		t.Fatal(err)
	}
	created, code, err := c.PostAuction(context.Background(), "t", "d", 1, 1000, 30)
	if err == nil {
		t.Fatal("転送エラーのはずがerrがnil")
	}
	if code != 0 {
		t.Errorf("code = %d, want 0 (結果不明)", code)
	}
	if created != nil {
		t.Errorf("created = %+v, want nil", created)
	}
}
