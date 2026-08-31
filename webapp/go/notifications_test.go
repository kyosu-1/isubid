package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

type notificationJSON struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	AuctionID int64     `json:"auction_id"`
	Message   string    `json:"message"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
}

type notificationsResponseJSON struct {
	Notifications []notificationJSON `json:"notifications"`
}

func getNotificationsAs(t *testing.T, c *http.Client, url string) notificationsResponseJSON {
	t.Helper()
	res, err := c.Get(url + "/api/notifications")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /notifications status = %d, want 200", res.StatusCode)
	}
	var body notificationsResponseJSON
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestGetNotifications(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)

	// user 5 が auction 1 に2回入札 → user 2,3,4 に各2件の outbid 通知
	bidder := loginSeedUser(t, ts.URL, "seed_user_05")
	res := postJSON(t, bidder, ts.URL+"/api/auctions/1/bids", `{"amount":1600}`)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("post bid 1 status = %d, want 201", res.StatusCode)
	}

	res = postJSON(t, bidder, ts.URL+"/api/auctions/1/bids", `{"amount":1700}`)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("post bid 2 status = %d, want 201", res.StatusCode)
	}

	// user 2 は自分宛の2件だけを id DESC で受け取る
	c2 := loginSeedUser(t, ts.URL, "seed_user_02")
	body := getNotificationsAs(t, c2, ts.URL)
	if len(body.Notifications) != 2 {
		t.Fatalf("user 2 の通知が %d件, want 2", len(body.Notifications))
	}
	for i := 1; i < len(body.Notifications); i++ {
		if body.Notifications[i].ID >= body.Notifications[i-1].ID {
			t.Errorf("id DESC でない: [%d].ID=%d, [%d].ID=%d",
				i-1, body.Notifications[i-1].ID, i, body.Notifications[i].ID)
		}
	}
	for _, n := range body.Notifications {
		if n.Type != "outbid" {
			t.Errorf("type = %q, want outbid", n.Type)
		}
		if n.AuctionID != 1 {
			t.Errorf("auction_id = %d, want 1", n.AuctionID)
		}
		if n.Message == "" {
			t.Error("message が空")
		}
		if n.IsRead {
			t.Error("is_read = true, want false")
		}
	}

	// 入札していない user 10 には通知が来ない(空配列、null ではない)
	c10 := loginSeedUser(t, ts.URL, "seed_user_10")
	empty := getNotificationsAs(t, c10, ts.URL)
	if empty.Notifications == nil {
		t.Error("notifications が null (期待: 空配列)")
	}
	if len(empty.Notifications) != 0 {
		t.Errorf("user 10 の通知が %d件, want 0", len(empty.Notifications))
	}
}

func TestGetNotificationsRequiresLogin(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	res, err := http.Get(ts.URL + "/api/notifications")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.StatusCode)
	}
}
