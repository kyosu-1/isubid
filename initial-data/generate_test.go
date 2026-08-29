package main

import "testing"

func TestGenerateUsers(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	ds := Generate(cfg)

	if len(ds.Users) != cfg.Users {
		t.Fatalf("users = %d, want %d", len(ds.Users), cfg.Users)
	}
	// 生成ユーザーは必ずシードの次から採番する
	if ds.Users[0].ID != SeedMaxUserID+1 {
		t.Errorf("最初の user id = %d, want %d", ds.Users[0].ID, SeedMaxUserID+1)
	}
	// id は連番で、name は id から決まる
	for i, u := range ds.Users {
		wantID := int64(SeedMaxUserID + 1 + i)
		if u.ID != wantID {
			t.Fatalf("users[%d].ID = %d, want %d", i, u.ID, wantID)
		}
		if u.Name != "gen_user_"+pad5(u.ID) {
			t.Fatalf("users[%d].Name = %q, want %q", i, u.Name, "gen_user_"+pad5(u.ID))
		}
	}
}

// 同じ scale と seed なら生成結果は完全に一致する。
func TestGenerateIsDeterministic(t *testing.T) {
	cfg := Scales["small"]
	cfg.Seed = DefaultSeed
	a := Generate(cfg)
	b := Generate(cfg)

	if len(a.Users) != len(b.Users) {
		t.Fatalf("users 件数が不一致: %d vs %d", len(a.Users), len(b.Users))
	}
	for i := range a.Users {
		if a.Users[i] != b.Users[i] {
			t.Fatalf("users[%d] が不一致: %+v vs %+v", i, a.Users[i], b.Users[i])
		}
	}
}
