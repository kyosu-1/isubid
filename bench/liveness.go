package main

import (
	"time"

	"github.com/isucon/isucandar/score"
)

// scoredTag は採点対象タグと、スコア内訳に出す表示名の対。
//
// 内訳の出力(bench/main.go)と liveness 判定の両方がこの並びを使う。以前は
// main.go に直書きされており、scoreTable と二重管理になっていた ——
// 片方に足し忘れると内訳の合計が raw と一致しなくなる。単一の定義にまとめ、
// TestScoredTagsCoversScoreTable が scoreTable との一致を固定している。
type scoredTag struct {
	Tag  score.ScoreTag
	Name string
}

var scoredTags = []scoredTag{
	{ScoreGETList, "GET /auctions"},
	{ScoreGETSearch, "GET /auctions (検索)"},
	{ScoreGETDetail, "GET /auctions/:id"},
	{ScorePOSTBid, "POST /auctions/:id/bids"},
	{ScoreGETFeed, "GET /auctions/:id/bids"},
	{ScoreGETNotifications, "GET /notifications"},
	{ScorePOSTAuction, "POST /auctions"},
	{ScoreGETPage, "GET / (ページロード)"},
}

// workerCounts は Load を駆動するワーカー数。liveness floor の条件付けに使う。
type workerCounts struct {
	Bidders   int
	Watchers  int
	Notifiers int
	Sellers   int
	Visitors  int
}

// livenessFloor は走行時間から、採点対象1本あたりの最低成功回数を返す。
//
// 採点対象で最小になるのは常に POST /auctions で、4-B の small 実測は
// 493回(Task 8 レビュー実測。docs/superpowers/plans/2026-08-30-isubid-phase4b-list-search.md)、
// 4-B ゲート3 で485回、4-E1 ゲート1 で496回(いずれも60秒走行)。
// 走行秒数/10(60秒なら6回)はその 1/80 前後であり、正しいアプリを
// 誤って落とす余地はほぼ無い。一方で「0回」だけでなく「ほぼ死んでいる」状態も
// 捕まえられる。走行時間に比例させているのは、-duration を短くしたデバッグ走行で
// 壊れないようにするため。
//
// 逆に言うと floor が捕まえるのは「全滅・ほぼ全滅」だけで、「60秒で10回しか
// 成功しない」程度の劣化は通す(docs/phase4-notes.md 持ち越し25)。
func livenessFloor(d time.Duration) int64 {
	if f := int64(d / (10 * time.Second)); f > 1 {
		return f
	}
	return 1
}

// livenessRequired は tag に floor を課すべきかを返す。
//
// -sellers 0 のようにワーカーを止めたデバッグ走行で、そのワーカーしか叩かない
// エンドポイントを誤って FAIL させないための条件付け。対応は bench/load.go の
// 各 *Iteration が実際に呼ぶエンドポイントから導いている。
//
// 新しい採点タグをここへ足し忘れると、そのエンドポイントは黙って判定をすり抜ける。
// TestLivenessRequiredCoversAllTags がそれを検出する。
func livenessRequired(tag score.ScoreTag, w workerCounts) bool {
	switch tag {
	case ScoreGETList, ScoreGETDetail:
		// bidder も watcher も visitor も、一覧を引いてから詳細を開く
		return w.Bidders > 0 || w.Watchers > 0 || w.Visitors > 0
	case ScoreGETSearch:
		// 検索付きの一覧を叩くのは watcher だけ
		return w.Watchers > 0
	case ScorePOSTBid, ScoreGETFeed:
		// 入札とフィード追従は bidder だけ
		return w.Bidders > 0
	case ScoreGETNotifications:
		return w.Notifiers > 0
	case ScorePOSTAuction:
		return w.Sellers > 0
	case ScoreGETPage:
		// ページロードを行うのは visitor だけ
		return w.Visitors > 0
	}
	return false
}

// checkLiveness は floor を下回った採点対象を scoredTags の順で返す。
// 空スライスなら、課すべき全エンドポイントが floor に到達している。
//
// これは 4-A で見逃された欠陥への対処である。エラー上限が絶対件数(100)であるため、
// 10秒でタイムアウトする「遅い全滅」は60秒間で高々数十件のエラーしか生まず、
// 採点対象が軒並み0回のまま RESULT: PASS が出ていた。
func checkLiveness(breakdown map[score.ScoreTag]int64, floor int64, w workerCounts) []scoredTag {
	var dead []scoredTag
	for _, st := range scoredTags {
		if !livenessRequired(st.Tag, w) {
			continue
		}
		if breakdown[st.Tag] < floor {
			dead = append(dead, st)
		}
	}
	return dead
}
