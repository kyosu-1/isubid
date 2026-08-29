package main

import (
	"fmt"
	"math/rand"
	"time"
)

type User struct {
	ID   int64
	Name string
}

// generatedEpoch は live / upcoming の時刻を保持する固定基準。
// POST /initialize がこの起点からのオフセットを現在時刻へ付け替える(spec 論点2)。
// closed は過去データなので書き換えず、絶対時刻のまま置く。
var generatedEpoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

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

// Dataset は1回の生成で得られる全データ。
type Dataset struct {
	Config   Config
	Users    []User
	Auctions []Auction
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

// Generate は cfg に従って初期データを生成する。
// cfg.Seed で初期化した単一の *rand.Rand を全生成で共有するため、
// 同じ cfg なら結果は完全に一致する。
func Generate(cfg Config) *Dataset {
	rng := rand.New(rand.NewSource(cfg.Seed))
	ds := &Dataset{Config: cfg}
	ds.Users = generateUsers(cfg)
	ds.Auctions = generateAuctions(cfg, rng, ds.Users)
	return ds
}

func generateUsers(cfg Config) []User {
	users := make([]User, 0, cfg.Users)
	for i := 0; i < cfg.Users; i++ {
		id := int64(SeedMaxUserID + 1 + i)
		users = append(users, User{ID: id, Name: "gen_user_" + pad5(id)})
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
