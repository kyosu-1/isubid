package main

// SeedMax* は webapp/sql/90_seed_phase1.sql が占める id 範囲の上端。
// 生成データは必ずこの次から採番する。シードを置き換えないのは、
// webapp/go のテスト27本がシードの具体値に依存しているため(spec 論点4)。
const (
	SeedMaxUserID    = 20
	SeedMaxAuctionID = 12
	SeedMaxBidID     = 8
)

// DefaultSeed は乱数シードの既定値。同じ scale と seed なら生成物は完全に一致する。
const DefaultSeed = int64(20260830)

// GeneratedPasswordHash は生成ユーザー全員で共有する bcrypt ハッシュ(平文 "password", cost 12)。
// 5,000回の bcrypt cost 12 は生成に数十分かかるうえ、ハッシュが個別であることに
// 検証上の意味がない。webapp/sql/90_seed_phase1.sql のシードユーザーと同じ値。
const GeneratedPasswordHash = "$2a$12$3I2mdpUq0j9HnZ/290WE6uMDyjo247QZxz6NmRj9nOKMHDCKB7pzK"

// Config は生成する初期データの規模。
type Config struct {
	Name             string
	Users            int
	ClosedAuctions   int
	LiveAuctions     int
	UpcomingAuctions int
	Bids             int
	Seed             int64
}

// Scales は段階的サイジング用の名前付き規模。full が親設計ドキュメントの目標値。
// 採用規模は実測ゲート(計画末尾の 4-A-4)で決める。
var Scales = map[string]Config{
	"small":  {Name: "small", Users: 500, ClosedAuctions: 1000, LiveAuctions: 50, UpcomingAuctions: 25, Bids: 30000},
	"medium": {Name: "medium", Users: 2000, ClosedAuctions: 4000, LiveAuctions: 100, UpcomingAuctions: 50, Bids: 120000},
	"full":   {Name: "full", Users: 5000, ClosedAuctions: 10000, LiveAuctions: 200, UpcomingAuctions: 100, Bids: 300000},
}
