package main

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

func postJSON(t *testing.T, client *http.Client, url, body string) *http.Response {
	t.Helper()
	res, err := client.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func newClientWithJar(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func TestRegisterAndLogin(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := newClientWithJar(t)

	res := postJSON(t, client, ts.URL+"/register", `{"name":"alice","password":"secretpw"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want 201", res.StatusCode)
	}
	var u struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(res.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.Name != "alice" || u.ID == 0 {
		t.Errorf("unexpected user: %+v", u)
	}

	res2 := postJSON(t, client, ts.URL+"/login", `{"name":"alice","password":"secretpw"}`)
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", res2.StatusCode)
	}
}

func TestRegisterDuplicateName(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := newClientWithJar(t)

	res := postJSON(t, client, ts.URL+"/register", `{"name":"bob","password":"secretpw"}`)
	res.Body.Close()
	res2 := postJSON(t, client, ts.URL+"/register", `{"name":"bob","password":"secretpw"}`)
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate register status = %d, want 409", res2.StatusCode)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := newClientWithJar(t)

	res := postJSON(t, client, ts.URL+"/login", `{"name":"seed_user_01","password":"wrong"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want 401", res.StatusCode)
	}
}

func TestLoginSeedUser(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	client := newClientWithJar(t)

	// シードユーザーのパスワードは全員 'password'(90_seed_phase1.sql)
	res := postJSON(t, client, ts.URL+"/login", `{"name":"seed_user_01","password":"password"}`)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body := make([]byte, 256)
		n, _ := res.Body.Read(body)
		t.Fatalf("seed login status = %d, want 200 (body: %s)", res.StatusCode, string(body[:n]))
	}
}
