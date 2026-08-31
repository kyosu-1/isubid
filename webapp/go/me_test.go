package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGetMeLoggedIn(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := loginSeedUser(t, ts.URL, "seed_user_05")

	res, err := client.Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/me status = %d, want 200", res.StatusCode)
	}
	var u struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(res.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.ID != 5 || u.Name != "seed_user_05" {
		t.Errorf("GET /api/me = %+v, want id=5 name=seed_user_05", u)
	}
}

func TestGetMeGuest(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := newClientWithJar(t)

	res, err := client.Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/me status = %d, want 401", res.StatusCode)
	}
}
