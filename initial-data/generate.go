package main

import (
	"fmt"
	"math/rand"
)

type User struct {
	ID   int64
	Name string
}

// Dataset は1回の生成で得られる全データ。
type Dataset struct {
	Config Config
	Users  []User
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
	_ = rng // 後続タスクで auctions / bids の生成に使う
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
