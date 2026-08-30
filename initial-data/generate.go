package main

import (
	"fmt"
	"math/rand"
	"time"
)

type User struct {
	ID   int64
	Name string
	// Icon はアイコン PNG のバイト列。nil ならアイコン未設定(DB では NULL)。
	Icon []byte
}

// generatedEpoch は live / upcoming の時刻を保持する固定基準。
// POST /initialize がこの起点からのオフセットを現在時刻へ付け替える(spec 論点2)。
// closed は過去データなので書き換えず、絶対時刻のまま置く。
//
// 意図的に未来日付にしてある(過去日付に「整地」してはいけない)。ダンプ投入直後、
// applyGeneratedSchedule が現在時刻基準へ書き換えるより前の一瞬、生成 live オークションの
// ends_at はこのエポック起点のオフセットそのままの値になる。エポックが過去日付だと、その
// 一瞬を runAuctionCloser (毎秒 status='live' AND ends_at<=NOW(6) を閉じるバッチ) が拾って
// 全件を期限切れとみなし、won 通知を auto-increment id で挿入してしまう。すると
// notifications の採番カウンタが1を超え、後続の 94_notifications.sql が id=1 から明示挿入
// する際に Duplicate entry で衝突する(確率的に発生する初期化失敗。webapp/go/initialize.go
// の generatedEpochLiteral と一致させること)。
var generatedEpoch = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

// closedBaseTime は closed オークションの ends_at を配置する基準。
// 走行時刻に依存しない過去の絶対時刻。
var closedBaseTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

type Auction struct {
	ID            int64
	SellerID      int64
	CategoryID    int64
	Title         string
	Description   string
	StartingPrice int64
	StartsAt      time.Time
	EndsAt        time.Time
	Status        string // "closed" | "live" | "upcoming"
	WinnerID      *int64 // closed かつ入札ありのときのみ。Task 3 で埋める
	WinningPrice  *int64
}

type Bid struct {
	ID        int64
	AuctionID int64
	UserID    int64
	Amount    int64
	CreatedAt time.Time
}

type Notification struct {
	ID        int64
	UserID    int64
	Type      string // "outbid" | "won"
	AuctionID int64
	Message   string
	CreatedAt time.Time
}

// Dataset は1回の生成で得られる全データ。
type Dataset struct {
	Config        Config
	Users         []User
	Auctions      []Auction
	Bids          []Bid
	Notifications []Notification
}

// chairNames / chairDescs は生成タイトルの素材。
// SQL リテラルへ素で埋め込むため、クォートやバックスラッシュを含めないこと
// (sqlout_test.go がこれを検証する)。
var chairNames = []string{
	"ヘリテージ・ラウンジ", "エルゴフロー", "ISUクラシック", "メッシュワークス",
	"アンティーク・ウィング", "ゲーミングエッジ", "スタンドアップ", "コンパクトシート",
	"ベルベット・オットマン", "スカンジ・ダイニング",
}

var chairDescs = []string{
	"長時間の作業に向いた一脚", "職人による手作業の仕上げ", "通気性に優れた背面メッシュ",
	"程よい硬さの座面", "折りたたみ可能な省スペース設計",
}

func pad5(n int64) string {
	return fmt.Sprintf("%05d", n)
}

// notificationTitleMaxRunes は通知文面に埋め込むタイトルの上限。
// notifications.message は VARCHAR(255) で、最も長い接尾辞
// 「」で他のユーザーに競り負けました は17文字。webapp/go 側の同名定数と揃えること。
const notificationTitleMaxRunes = 200

func truncateForNotification(title string) string {
	r := []rune(title)
	if len(r) <= notificationTitleMaxRunes {
		return title
	}
	return string(r[:notificationTitleMaxRunes])
}

// Generate は cfg に従って初期データを生成する。
// cfg.Seed で初期化した単一の *rand.Rand を全生成で共有するため、
// 同じ cfg なら結果は完全に一致する。
func Generate(cfg Config) *Dataset {
	rng := rand.New(rand.NewSource(cfg.Seed))
	ds := &Dataset{Config: cfg}
	ds.Users = generateUsers(cfg)
	ds.Auctions = generateAuctions(cfg, rng, ds.Users)
	ds.Bids = generateBids(cfg, rng, ds.Auctions, ds.Users)
	backfillWinners(ds.Auctions, ds.Bids)
	ds.Notifications = generateNotifications(ds.Auctions, ds.Bids)
	return ds
}

// iconlessRate はアイコンを持たない生成ユーザーの割合。
//
// 実サイトに即しているだけでなく、参照実装が「icon IS NULL のユーザーに
// 404 ではなく 200 を返す」改悪をしたときにベンチが検出できるようにするため、
// 未設定のユーザーを必ず作る。
const iconlessRate = 0.1

// newIconRNG はアイコン専用の乱数源を作る。
//
// **共有の rng から引いてはならない。** generateUsers は Generate の中で
// generateAuctions より先に呼ばれるので、共有 rng を消費すると乱数の順序が
// ずれ、auctions / bids / notifications がすべて別物になる(コミット済み
// ダンプの全ファイルが変わり、4-A/4-B の実測値の前提も崩れる)。
// 独立した源から引くことで 92〜94 のダンプはバイト単位で不変に保たれる。
func newIconRNG(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed ^ 0x1c04))
}

func generateUsers(cfg Config) []User {
	iconRNG := newIconRNG(cfg.Seed)
	users := make([]User, 0, cfg.Users)
	for i := 0; i < cfg.Users; i++ {
		id := int64(SeedMaxUserID + 1 + i)
		u := User{ID: id, Name: "gen_user_" + pad5(id)}
		if iconRNG.Float64() >= iconlessRate {
			u.Icon = GenerateIcon(id)
		}
		users = append(users, u)
	}
	return users
}

// generateAuctions は closed / live / upcoming を id 昇順で連続採番して返す。
//
// live の ends_at は generatedEpoch からのオフセットで表す。オフセットは
// 「走行中に閉じる短いもの」と「走行後も残る長いもの」を混ぜたうえでシャッフルし、
// id 順と相関しないようにする。相関していると一覧の ORDER BY ends_at ASC を
// ORDER BY id ASC に書き換える改変を検出できなくなる。
func generateAuctions(cfg Config, rng *rand.Rand, users []User) []Auction {
	total := cfg.ClosedAuctions + cfg.LiveAuctions + cfg.UpcomingAuctions
	auctions := make([]Auction, 0, total)

	// live の ends_at オフセット。重複しないよう等差で作ってからシャッフルする。
	liveOffsets := make([]int, cfg.LiveAuctions)
	for i := range liveOffsets {
		if i%2 == 0 {
			liveOffsets[i] = 15 + i*3 // 走行中(60秒)に一部が閉じる
		} else {
			liveOffsets[i] = 3600 + i*7 // 走行を通して live のまま
		}
	}
	rng.Shuffle(len(liveOffsets), func(i, j int) {
		liveOffsets[i], liveOffsets[j] = liveOffsets[j], liveOffsets[i]
	})

	statuses := make([]string, 0, total)
	for i := 0; i < cfg.ClosedAuctions; i++ {
		statuses = append(statuses, "closed")
	}
	for i := 0; i < cfg.LiveAuctions; i++ {
		statuses = append(statuses, "live")
	}
	for i := 0; i < cfg.UpcomingAuctions; i++ {
		statuses = append(statuses, "upcoming")
	}

	liveIdx := 0
	for i, status := range statuses {
		id := int64(SeedMaxAuctionID + 1 + i)
		a := Auction{
			ID:            id,
			SellerID:      users[rng.Intn(len(users))].ID,
			CategoryID:    int64(1 + rng.Intn(3)),
			Title:         chairNames[rng.Intn(len(chairNames))] + " " + pad5(id),
			Description:   chairDescs[rng.Intn(len(chairDescs))],
			StartingPrice: int64(1000 + rng.Intn(19)*500),
			Status:        status,
		}
		switch status {
		case "closed":
			// 過去に分散配置。走行時刻に依存しない絶対時刻。
			end := closedBaseTime.Add(time.Duration(rng.Intn(180*24)) * time.Hour)
			a.StartsAt = end.Add(-72 * time.Hour)
			a.EndsAt = end
		case "live":
			off := liveOffsets[liveIdx]
			liveIdx++
			a.StartsAt = generatedEpoch.Add(-1 * time.Hour)
			a.EndsAt = generatedEpoch.Add(time.Duration(off) * time.Second)
		case "upcoming":
			// 走行中ずっと upcoming のまま
			a.StartsAt = generatedEpoch.Add(time.Duration(3600+i) * time.Second)
			a.EndsAt = generatedEpoch.Add(time.Duration(7200+i) * time.Second)
		}
		auctions = append(auctions, a)
	}
	return auctions
}

// generateBids は closed / live オークションへ入札を分配する。
//
// 各オークション内では id 昇順に amount が厳密単調増加し、created_at も同順になる。
// これはベンチの ValidateBidsInvariant / ValidateFeedPage が検証する不変条件であり、
// 破ると Prepare とウォッチャーが即 critical を出す。
// 連続する入札は必ず別ユーザーにする(現実的であり、通知ファンアウトの重みも生む)。
func generateBids(cfg Config, rng *rand.Rand, auctions []Auction, users []User) []Bid {
	// 入札対象は closed と live のみ
	targets := make([]int, 0, len(auctions))
	for i, a := range auctions {
		if a.Status == "closed" || a.Status == "live" {
			targets = append(targets, i)
		}
	}
	if len(targets) == 0 {
		return nil
	}

	// 各対象への入札数を決める。均等割りしたうえで剰余を先頭から配ることで、
	// 合計が必ず cfg.Bids に一致する。
	counts := make([]int, len(targets))
	base := cfg.Bids / len(targets)
	rest := cfg.Bids % len(targets)
	for i := range counts {
		counts[i] = base
		if i < rest {
			counts[i]++
		}
	}
	// 偏りを作る: 隣接するペアで入札数をやり取りする(合計は保存される)
	for i := 0; i+1 < len(counts); i += 2 {
		move := rng.Intn(counts[i]/2 + 1)
		counts[i] -= move
		counts[i+1] += move
	}

	bids := make([]Bid, 0, cfg.Bids)
	nextID := int64(SeedMaxBidID + 1)
	for k, idx := range targets {
		a := &auctions[idx]
		amount := a.StartingPrice
		created := a.StartsAt
		var prevUser int64
		for j := 0; j < counts[k]; j++ {
			amount += int64(100 + rng.Intn(400))
			created = created.Add(time.Duration(1+rng.Intn(60)) * time.Second)

			u := users[rng.Intn(len(users))].ID
			for u == prevUser {
				u = users[rng.Intn(len(users))].ID
			}
			prevUser = u

			bids = append(bids, Bid{
				ID: nextID, AuctionID: a.ID, UserID: u,
				Amount: amount, CreatedAt: created,
			})
			nextID++
		}
	}
	return bids
}

// generateNotifications は closed オークションの落札結果から過去の通知を作る。
//
// 目的は notifications テーブルの user_id フルスキャン(インデックス無し)に
// 走査対象を与えることであり、宛先は生成ユーザーに限る。シードユーザー宛を作ると
// GET /notifications の応答が初手から巨大になり、通知確認シナリオのコストが
// 実データではなく生成データに支配される。
//
// 1オークションにつき、落札者へ won を1件、他の入札者(最大3人)へ outbid を1件ずつ。
func generateNotifications(auctions []Auction, bids []Bid) []Notification {
	bidders := map[int64][]int64{} // auctionID -> 入札した user_id(重複除去済み、初出順)
	seen := map[[2]int64]bool{}
	for _, b := range bids {
		key := [2]int64{b.AuctionID, b.UserID}
		if seen[key] {
			continue
		}
		seen[key] = true
		bidders[b.AuctionID] = append(bidders[b.AuctionID], b.UserID)
	}

	const maxOutbidPerAuction = 3
	var out []Notification
	nextID := int64(1) // シードは notifications を持たない
	for i := range auctions {
		a := &auctions[i]
		if a.Status != "closed" || a.WinnerID == nil {
			continue
		}
		title := truncateForNotification(a.Title)

		out = append(out, Notification{
			ID: nextID, UserID: *a.WinnerID, Type: "won", AuctionID: a.ID,
			Message: "「" + title + "」を落札しました", CreatedAt: a.EndsAt,
		})
		nextID++

		n := 0
		for _, uid := range bidders[a.ID] {
			if uid == *a.WinnerID {
				continue
			}
			if n >= maxOutbidPerAuction {
				break
			}
			out = append(out, Notification{
				ID: nextID, UserID: uid, Type: "outbid", AuctionID: a.ID,
				Message: "「" + title + "」で他のユーザーに競り負けました", CreatedAt: a.EndsAt,
			})
			nextID++
			n++
		}
	}
	return out
}

// backfillWinners は closed オークションの winner_id / winning_price を、
// そのオークションの最大 amount の入札から埋める。
// ベンチの reconcileClosedAuction が winner = argmax(bids) を厳密に検証するため、
// 生成データもこの不変条件を満たす必要がある。
func backfillWinners(auctions []Auction, bids []Bid) {
	top := map[int64]Bid{}
	for _, b := range bids {
		if cur, ok := top[b.AuctionID]; !ok || b.Amount > cur.Amount {
			top[b.AuctionID] = b
		}
	}
	for i := range auctions {
		a := &auctions[i]
		if a.Status != "closed" {
			continue
		}
		t, ok := top[a.ID]
		if !ok {
			continue // 入札0件のまま closed
		}
		winner, price := t.UserID, t.Amount
		a.WinnerID = &winner
		a.WinningPrice = &price
	}
}
