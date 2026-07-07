package main

import (
	"net/http"

	"github.com/gorilla/sessions"
)

const sessionName = "isubid_session"

var store = sessions.NewCookieStore([]byte(getEnv("ISUBID_SESSION_SECRET", "isubid-secret")))

func setLogin(w http.ResponseWriter, r *http.Request, userID int64) error {
	sess, _ := store.Get(r, sessionName)
	sess.Values["user_id"] = userID
	return sess.Save(r, w)
}

func currentUserID(r *http.Request) (int64, bool) {
	sess, err := store.Get(r, sessionName)
	if err != nil {
		return 0, false
	}
	v, ok := sess.Values["user_id"].(int64)
	return v, ok
}
