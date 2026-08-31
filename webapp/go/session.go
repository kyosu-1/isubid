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
