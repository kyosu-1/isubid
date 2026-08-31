package main

import (
	"crypto/rand"
	"log"
	"net/http"

	"github.com/gorilla/sessions"
)

const sessionName = "isubid_session"

// sessionSecret はセッション署名鍵を返す。
//
// ISUBID_SESSION_SECRET が設定されていればそれを使う。コンテスト環境のように
// 複数インスタンスでセッションを共有したい場合や、プロセス再起動をまたいで
// ログイン状態を保ちたい場合は、これを設定すること(dev/compose.yaml も参照)。
//
// 未設定の場合は起動のたびに crypto/rand でランダムな鍵を生成する。この鍵は
// プロセス間で共有されないため、再起動やマルチインスタンス構成ではセッションが
// 継続しなくなるが、これは機能上の制約に過ぎず安全側に倒れる。
// かつてはここに固定文字列("isubid-secret")のデフォルトが入っていたが、
// これは公開リポジトリのどのデプロイにも共通する既知の鍵になってしまい、
// 誰でも任意の user_id を持つセッションCookieを偽造できてしまうため廃止した。
func sessionSecret() []byte {
	if v := getEnv("ISUBID_SESSION_SECRET", ""); v != "" {
		return []byte(v)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		// crypto/rand の失敗はOS側の異常事態でしかありえない。フォールバックせず
		// 起動時に気づけるようにする。
		log.Fatalf("セッション鍵の生成に失敗しました: %v", err)
	}
	log.Println("警告: ISUBID_SESSION_SECRET が未設定のため、起動ごとにランダムな鍵を生成しました。" +
		"複数インスタンス構成やプロセス再起動をまたいでセッションを共有するには ISUBID_SESSION_SECRET を設定してください。")
	return key
}

var store = sessions.NewCookieStore(sessionSecret())

func init() {
	// gorilla/sessions v1.4.0 の NewCookieStore は既定で Secure: true / SameSite: None を
	// 設定する(store.go 参照)。このアプリはコンテスト環境でHTTPのまま動く前提のため、
	// Secure: true のままだと Go 標準の net/http/cookiejar が localhost 以外への
	// HTTPリクエストで Secure Cookie を送らなくなり、ベンチのセッション認証が全滅する。
	// そのため Secure は明示的に false のままにする。
	//
	// HttpOnly は gorilla/sessions の Options 構造体では明示されておらず、
	// ゼロ値(false)のままだった(v1.4.0 の options.go を確認)。4-D で SPA が入り
	// XSSがあった場合の増幅面が現実味を帯びたため、明示的に true にする。
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 30, // 既定(NewCookieStoreの初期値)を維持
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	}
}

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
