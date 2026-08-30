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
	errorLimit = 100 // アプリエラーがこれを超えたらFail
	// errorPenalty はエラー1件あたりの減点。
	//
	// 成功1リクエストあたりの平均得点は 4-B ゲート3 の実測で約1.81点
	// (raw 19086点 / 成功10532回。10532 は
	// 2078+1566+3627+1136+1136+504+485 = スコア内訳7本の合計)なので、
	// エラー1件が成功約11リクエストぶんの損失になる。上限の100件で2000点、
	// raw 19086 に対して約10.5%。
	//
	// 4-E1 以前このコメントは「約1.85点(19086点/10331回)」としていたが、分母の
	// 10331 は 4-B Task 8 のレビュー実測表(docs/superpowers/plans/
	// 2026-08-30-isubid-phase4b-list-search.md)から転記する際に一覧の行だけ
	// 別走行の値(2125→2078)に差し替わったハイブリッドで、どの単一走行とも
	// 一致しなかった。上の数字は 4-B ゲート3 の実測ログ1本から再計算している
	// (docs/phase4-notes.md 持ち越し24)。
	//
	// 以前は 1 だった。4-A(raw 6177)では100件で1.6%の減点だったが、4-B の
	// ページネーション導入で raw が19086まで上がり、同じ100件が0.5%になって
	// 減点が事実上機能しなくなっていた。採用スケールを変えると相対重みはまた
	// 変わるので、4-E で採用スケールを再決定する際に再調整すること。
	errorPenalty = 20
)
