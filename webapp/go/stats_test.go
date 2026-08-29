package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

type statsJSON struct {
	ListedCount int64 `json:"listed_count"`
	SoldCount   int64 `json:"sold_count"`
	TotalSales  int64 `json:"total_sales"`
	LiveCount   int64 `json:"live_count"`
}

func TestGetStatsMe(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	h := newTestHandler(t)

	// seed: user 11 は auction 11 のみを出品しており、closed / winner 12 / 12000
	c11 := loginSeedUser(t, ts.URL, "seed_user_11")
	res, err := c11.Get(ts.URL + "/stats/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var s statsJSON
	if err := json.NewDecoder(res.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.ListedCount != 1 {
		t.Errorf("listed_count = %d, want 1", s.ListedCount)
	}
	if s.SoldCount != 1 {
		t.Errorf("sold_count = %d, want 1", s.SoldCount)
	}
	if s.TotalSales != 12000 {
		t.Errorf("total_sales = %d, want 12000", s.TotalSales)
	}
	if s.LiveCount != 0 {
		t.Errorf("live_count = %d, want 0", s.LiveCount)
	}

	// seed: user 1 は auction 1 のみ出品、live で未落札
	c1 := loginSeedUser(t, ts.URL, "seed_user_01")
	res2, err := c1.Get(ts.URL + "/stats/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	var s1 statsJSON
	if err := json.NewDecoder(res2.Body).Decode(&s1); err != nil {
		t.Fatal(err)
	}
	if s1.ListedCount != 1 || s1.SoldCount != 0 || s1.TotalSales != 0 || s1.LiveCount != 1 {
		t.Errorf("user 1 stats = %+v, want listed=1 sold=0 total=0 live=1", s1)
	}

	// auction 1 を閉じると sold と total_sales が動く(落札額 1500 / user 4)
	if _, err := h.db.ExecContext(context.Background(),
		"UPDATE auctions SET ends_at = DATE_SUB(NOW(6), INTERVAL 1 SECOND) WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.closeDueAuctions(context.Background()); err != nil {
		t.Fatal(err)
	}
	res3, err := c1.Get(ts.URL + "/stats/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res3.Body.Close()
	var s2 statsJSON
	if err := json.NewDecoder(res3.Body).Decode(&s2); err != nil {
		t.Fatal(err)
	}
	if s2.SoldCount != 1 || s2.TotalSales != 1500 || s2.LiveCount != 0 {
		t.Errorf("落札後 stats = %+v, want sold=1 total=1500 live=0", s2)
	}
}

func TestGetStatsMeRequiresLogin(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	res, err := http.Get(ts.URL + "/stats/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.StatusCode)
	}
}
