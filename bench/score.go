package main

import (
	"github.com/isucon/isucandar/failure"
	"github.com/isucon/isucandar/score"
)

const (
	ScoreGETList          score.ScoreTag = "GET /auctions"
	ScoreGETSearch        score.ScoreTag = "GET /auctions (検索)"
	ScoreGETDetail        score.ScoreTag = "GET /auctions/:id"
	ScorePOSTBid          score.ScoreTag = "POST /auctions/:id/bids"
	ScoreGETFeed          score.ScoreTag = "GET /auctions/:id/bids"
	ScoreGETNotifications score.ScoreTag = "GET /notifications"
	ScorePOSTAuction      score.ScoreTag = "POST /auctions"
)

// 配点(スペック準拠: 入札が主役)
var scoreTable = map[score.ScoreTag]int64{
	ScoreGETList:          1,
	ScoreGETSearch:        2, // 最も重い読み取り経路。配点で攻略線へ誘導する
	ScoreGETDetail:        1,
	ScorePOSTBid:          5,
	ScoreGETFeed:          1,
	ScoreGETNotifications: 2,
	ScorePOSTAuction:      5,
}

const (
	// ErrCritical は整合性違反。1件でもFail。
	ErrCritical failure.StringCode = "critical"
	// ErrApplication は5xx・予期しない応答など。減点対象。
	ErrApplication failure.StringCode = "application"
)

const (
	errorLimit   = 100 // アプリエラーがこれを超えたらFail
	errorPenalty = 1   // エラー1件あたりの減点
)
