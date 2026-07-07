package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/isucon/isucandar"
	"github.com/isucon/isucandar/failure"
	"github.com/isucon/isucandar/score"
)

func main() {
	target := flag.String("target", "http://localhost:8080", "ベンチ対象のベースURL")
	duration := flag.Duration("duration", 60*time.Second, "負荷走行時間")
	prepareOnly := flag.Bool("prepare-only", false, "Prepare(整合性チェック)のみ実行")
	bidders := flag.Int("bidders", 8, "入札者worker数")
	watchers := flag.Int("watchers", 4, "ウォッチャーworker数")
	flag.Parse()

	s := &Scenario{
		Target:      *target,
		PrepareOnly: *prepareOnly,
		Bidders:     *bidders,
		Watchers:    *watchers,
		Ledger:      NewLedger(),
	}

	b, err := isucandar.NewBenchmark(
		isucandar.WithoutPanicRecover(),
		isucandar.WithLoadTimeout(*duration),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b.AddScenario(s)

	result := b.Start(context.Background())

	for tag, mag := range scoreTable {
		result.Score.Set(tag, mag)
	}

	errs := result.Errors.All()
	criticalCount := 0
	appCount := 0
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "ERR: %v\n", e)
		if failure.IsCode(e, ErrCritical) {
			criticalCount++
		} else {
			appCount++
		}
	}

	if *prepareOnly {
		if len(errs) > 0 {
			fmt.Println("PREPARE: FAIL")
			os.Exit(1)
		}
		fmt.Println("PREPARE: PASS")
		return
	}

	raw := result.Score.Sum()
	penalty := int64(len(errs) * errorPenalty)
	total := raw - penalty
	if total < 0 {
		total = 0
	}

	fmt.Printf("SCORE: %d  (raw %d, penalty %d)\n", total, raw, penalty)
	breakdown := result.Score.Breakdown()
	for _, st := range []struct {
		tag  score.ScoreTag
		name string
	}{
		{ScoreGETList, "GET /auctions"},
		{ScoreGETDetail, "GET /auctions/:id"},
		{ScorePOSTBid, "POST /auctions/:id/bids"},
	} {
		count := breakdown[st.tag]
		pt := count * scoreTable[st.tag]
		fmt.Printf("  %-25s: %d回 (%d点)\n", st.name, count, pt)
	}
	fmt.Printf("ERRORS: %d件 (critical: %d件)\n", len(errs), criticalCount)

	pass := criticalCount == 0 && appCount <= errorLimit && total > 0
	if pass {
		fmt.Println("RESULT: PASS")
		return
	}
	fmt.Println("RESULT: FAIL")
	os.Exit(1)
}
