package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// truncateForNotification は通知メッセージに埋め込む出品タイトルを安全な長さへ
// 切り詰める純粋関数。マルチバイト文字を含めてrune単位で切り詰められること、
// 上限以下ならそのまま返すことを確認する。
func TestTruncateForNotification(t *testing.T) {
	for _, tt := range []struct {
		name  string
		title string
		want  string
	}{
		{"短いタイトルはそのまま", "テスト椅子", "テスト椅子"},
		{"空文字はそのまま", "", ""},
		{
			"ちょうど上限のタイトルはそのまま",
			strings.Repeat("あ", notificationTitleMaxRunes),
			strings.Repeat("あ", notificationTitleMaxRunes),
		},
		{
			"上限を超えるタイトルはrune単位で切り詰められる",
			strings.Repeat("あ", notificationTitleMaxRunes+50),
			strings.Repeat("あ", notificationTitleMaxRunes),
		},
		{
			"postAuctionが許す最大長(255rune)でも上限まで切り詰められる",
			strings.Repeat("椅", 255),
			strings.Repeat("椅", notificationTitleMaxRunes),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateForNotification(tt.title)
			if got != tt.want {
				t.Errorf("got %d runes, want %d runes (内容不一致)", len([]rune(got)), len([]rune(tt.want)))
			}
			if n := len([]rune(got)); n > notificationTitleMaxRunes {
				t.Errorf("truncateForNotification が %d rune を返した(上限 %d を超過)", n, notificationTitleMaxRunes)
			}
		})
	}
}

// postAuction が許す上限(255rune)いっぱいのタイトルで出品しても、outbid/won
// 通知の message INSERT が notifications.message の VARCHAR(255) を溢れさせて
// エラーにならないことを確認する(レビュー指摘: FIX2)。
//
// 修正前は、255runeのタイトル + outbidの定型句(17rune)や + wonの定型句(9rune)を
// 連結すると255を超え、MySQL 8のデフォルト(STRICT_TRANS_TABLES)下でINSERTがエラーに
// なっていた。closeAuction側はこのINSERTがトランザクション内にあるため、エラーになると
// UPDATE ... SET status = 'closed' ごとロールバックし、該当オークションが ends_at を
// 過ぎても永久に live のまま残ってしまう(closeDueAuctionsが毎秒再選出するだけで
// 前に進まない)。
func TestLongTitleAuctionDoesNotOverflowNotificationMessage(t *testing.T) {
	ts := newTestServer(t)
	initApp(t, ts)
	h := newTestHandler(t)
	ctx := context.Background()

	longTitle := strings.Repeat("椅", 255) // postAuction が許す最大長ちょうど
	seller := loginSeedUser(t, ts.URL, "seed_user_01")
	res, err := seller.Post(ts.URL+"/auctions", "application/json", strings.NewReader(
		fmt.Sprintf(`{"title":"%s","description":"d","category_id":1,"starting_price":1000,"duration_seconds":30}`, longTitle)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST /auctions status = %d, want 201", res.StatusCode)
	}
	var created auctionCreatedJSON
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	// 1件目の入札: 先行入札者がいないのでoutbidファンアウトは発生しない。
	bidder1 := loginSeedUser(t, ts.URL, "seed_user_02")
	res1 := postJSON(t, bidder1, fmt.Sprintf("%s/auctions/%d/bids", ts.URL, created.ID), `{"amount":1100}`)
	defer res1.Body.Close()
	if res1.StatusCode != http.StatusCreated {
		t.Fatalf("1件目の入札 status = %d, want 201", res1.StatusCode)
	}

	// 2件目の入札: seed_user_02へoutbid通知が飛ぶ。この経路が長いタイトルでmessageを
	// INSERTする(修正前はここで500になる)。
	bidder2 := loginSeedUser(t, ts.URL, "seed_user_03")
	res2 := postJSON(t, bidder2, fmt.Sprintf("%s/auctions/%d/bids", ts.URL, created.ID), `{"amount":1200}`)
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusCreated {
		t.Fatalf("2件目の入札(outbid通知を誘発) status = %d, want 201", res2.StatusCode)
	}

	var outbidMsg string
	if err := h.db.GetContext(ctx, &outbidMsg,
		"SELECT message FROM notifications WHERE auction_id = ? AND type = 'outbid' AND user_id = 2",
		created.ID); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(outbidMsg)); n > 255 {
		t.Errorf("outbid通知のmessageが%druneでVARCHAR(255)を超えている", n)
	}

	// 終了処理: won通知も同じ経路(タイトル埋め込み)を通す。
	if _, err := h.db.ExecContext(ctx,
		"UPDATE auctions SET ends_at = DATE_SUB(NOW(6), INTERVAL 1 SECOND) WHERE id = ?", created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.closeDueAuctions(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := h.db.GetContext(ctx, &status, "SELECT status FROM auctions WHERE id = ?", created.ID); err != nil {
		t.Fatal(err)
	}
	if status != "closed" {
		t.Fatalf("status = %q, want closed (修正前はwon通知のINSERTがVARCHAR(255)溢れでエラーになりUPDATEごとロールバックしてliveのまま残る)", status)
	}
	var wonMsg string
	if err := h.db.GetContext(ctx, &wonMsg,
		"SELECT message FROM notifications WHERE auction_id = ? AND type = 'won'", created.ID); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(wonMsg)); n > 255 {
		t.Errorf("won通知のmessageが%druneでVARCHAR(255)を超えている", n)
	}
}
