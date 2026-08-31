package main

import (
	"testing"
	"time"

	"github.com/isucon/isucandar/score"
)

// TestScoredTagsCoversScoreTable は採点対象の並び(内訳出力と liveness 判定が使う)が
// 配点表と過不足なく一致することを固定する。片方にタグを足し忘れると、内訳の合計が
// raw と一致しなくなるか、新しいエンドポイントが liveness 判定をすり抜ける。
func TestScoredTagsCoversScoreTable(t *testing.T) {
	if len(scoredTags) != len(scoreTable) {
		t.Fatalf("scoredTags %d件 と scoreTable %d件 が不一致", len(scoredTags), len(scoreTable))
	}
	seen := map[score.ScoreTag]bool{}
	for _, st := range scoredTags {
		if _, ok := scoreTable[st.Tag]; !ok {
			t.Errorf("scoredTags の %q が scoreTable に無い", st.Tag)
		}
		if seen[st.Tag] {
			t.Errorf("scoredTags に %q が重複している", st.Tag)
		}
		seen[st.Tag] = true
		if st.Name == "" {
			t.Errorf("%q の表示名が空", st.Tag)
		}
	}
}

// TestLivenessRequiredCoversAllTags は、すべてのワーカーが有効なら採点対象すべてに
// floor が課されることを固定する。新しい採点タグを livenessRequired へ足し忘れると、
// そのエンドポイントは黙って判定をすり抜ける。
func TestLivenessRequiredCoversAllTags(t *testing.T) {
	all := workerCounts{Bidders: 1, Watchers: 1, Notifiers: 1, Sellers: 1, Visitors: 1}
	for _, st := range scoredTags {
		if !livenessRequired(st.Tag, all) {
			t.Errorf("%q に floor が課されていない", st.Tag)
		}
	}
}

func TestLivenessFloor(t *testing.T) {
	for _, tt := range []struct {
		d    time.Duration
		want int64
	}{
		{60 * time.Second, 6},
		{10 * time.Second, 1},
		{9 * time.Second, 1}, // 端数は切り捨てだが最低 1
		{1 * time.Second, 1},
		{0, 1},
		{120 * time.Second, 12},
	} {
		if got := livenessFloor(tt.d); got != tt.want {
			t.Errorf("livenessFloor(%v) = %d, want %d", tt.d, got, tt.want)
		}
	}
}

// TestLivenessRequiredRespectsWorkerCounts は、ワーカーを止めた走行で
// そのワーカーしか叩かないエンドポイントに floor を課さないことを固定する。
// ここが効いていないと -sellers 0 のデバッグ走行が誤って FAIL する。
func TestLivenessRequiredRespectsWorkerCounts(t *testing.T) {
	noSellers := workerCounts{Bidders: 1, Watchers: 1, Notifiers: 1, Sellers: 0}
	if livenessRequired(ScorePOSTAuction, noSellers) {
		t.Error("sellers=0 なのに POST /auctions に floor が課されている")
	}
	if !livenessRequired(ScorePOSTBid, noSellers) {
		t.Error("bidders=1 なのに POST /auctions/:id/bids に floor が課されていない")
	}

	noBidders := workerCounts{Bidders: 0, Watchers: 1, Notifiers: 1, Sellers: 1}
	if livenessRequired(ScorePOSTBid, noBidders) {
		t.Error("bidders=0 なのに POST /auctions/:id/bids に floor が課されている")
	}
	if livenessRequired(ScoreGETFeed, noBidders) {
		t.Error("bidders=0 なのに GET /auctions/:id/bids に floor が課されている")
	}
	// 一覧と詳細は bidder と watcher の両方が叩くので、片方が生きていれば課す
	if !livenessRequired(ScoreGETList, noBidders) {
		t.Error("watchers=1 なのに GET /auctions に floor が課されていない")
	}
	if !livenessRequired(ScoreGETDetail, noBidders) {
		t.Error("watchers=1 なのに GET /auctions/:id に floor が課されていない")
	}

	noWatchers := workerCounts{Bidders: 1, Watchers: 0, Notifiers: 1, Sellers: 1}
	if livenessRequired(ScoreGETSearch, noWatchers) {
		t.Error("watchers=0 なのに GET /auctions (検索) に floor が課されている")
	}
	if !livenessRequired(ScoreGETList, noWatchers) {
		t.Error("bidders=1 なのに GET /auctions に floor が課されていない")
	}

	noVisitors := workerCounts{Bidders: 1, Watchers: 1, Notifiers: 1, Sellers: 1, Visitors: 0}
	if livenessRequired(ScoreGETPage, noVisitors) {
		t.Error("visitors=0 なのに GET / (ページロード) に floor が課されている")
	}
	// 一覧と詳細は bidder / watcher も叩くので、visitor を止めても課され続ける
	if !livenessRequired(ScoreGETList, noVisitors) {
		t.Error("bidders=1 なのに GET /auctions に floor が課されていない")
	}

	onlyVisitors := workerCounts{Visitors: 1}
	if !livenessRequired(ScoreGETPage, onlyVisitors) {
		t.Error("visitors=1 なのに GET / (ページロード) に floor が課されていない")
	}
	if !livenessRequired(ScoreGETList, onlyVisitors) {
		t.Error("visitors=1 なのに GET /auctions に floor が課されていない")
	}
	if livenessRequired(ScorePOSTBid, onlyVisitors) {
		t.Error("bidders=0 なのに POST /auctions/:id/bids に floor が課されている")
	}

	none := workerCounts{}
	for _, st := range scoredTags {
		if livenessRequired(st.Tag, none) {
			t.Errorf("ワーカーが全て0なのに %q に floor が課されている", st.Tag)
		}
	}
}

func TestCheckLiveness(t *testing.T) {
	all := workerCounts{Bidders: 8, Watchers: 4, Notifiers: 2, Sellers: 2, Visitors: 2}

	// 全て floor 以上なら空
	healthy := map[score.ScoreTag]int64{}
	for _, st := range scoredTags {
		healthy[st.Tag] = 100
	}
	if dead := checkLiveness(healthy, 6, all); len(dead) != 0 {
		t.Errorf("健全な走行で %d件が下回った: %+v", len(dead), dead)
	}

	// ちょうど floor は通る(境界)
	atFloor := map[score.ScoreTag]int64{}
	for _, st := range scoredTags {
		atFloor[st.Tag] = 6
	}
	if dead := checkLiveness(atFloor, 6, all); len(dead) != 0 {
		t.Errorf("ちょうど floor の走行が落ちた: %+v", dead)
	}

	// floor - 1 は落ちる(境界)
	belowFloor := map[score.ScoreTag]int64{}
	for _, st := range scoredTags {
		belowFloor[st.Tag] = 5
	}
	if dead := checkLiveness(belowFloor, 6, all); len(dead) != len(scoredTags) {
		t.Errorf("floor-1 で落ちたのが %d件, want %d件", len(dead), len(scoredTags))
	}

	// 4-A で見逃された形: 一覧経路が全滅し、通知と出品だけ生きている
	partial := map[score.ScoreTag]int64{
		ScoreGETList:          0,
		ScoreGETSearch:        0,
		ScoreGETDetail:        0,
		ScorePOSTBid:          0,
		ScoreGETFeed:          0,
		ScoreGETNotifications: 513,
		ScorePOSTAuction:      493,
		ScoreGETPage:          0,
	}
	dead := checkLiveness(partial, 6, all)
	if len(dead) != 6 {
		t.Fatalf("下回ったのが %d件, want 6件: %+v", len(dead), dead)
	}
	// 返る順序は scoredTags の順であること(出力の安定性のため)
	wantOrder := []score.ScoreTag{
		ScoreGETList, ScoreGETSearch, ScoreGETDetail, ScorePOSTBid, ScoreGETFeed, ScoreGETPage,
	}
	for i, want := range wantOrder {
		if dead[i].Tag != want {
			t.Errorf("dead[%d] = %q, want %q", i, dead[i].Tag, want)
		}
	}

	// ワーカーを止めたぶんは 0回でも下回り扱いにしない
	noSellers := workerCounts{Bidders: 8, Watchers: 4, Notifiers: 2, Sellers: 0}
	zeroSeller := map[score.ScoreTag]int64{}
	for _, st := range scoredTags {
		zeroSeller[st.Tag] = 100
	}
	zeroSeller[ScorePOSTAuction] = 0
	if dead := checkLiveness(zeroSeller, 6, noSellers); len(dead) != 0 {
		t.Errorf("sellers=0 の走行で %+v が下回り扱いになった", dead)
	}
}
