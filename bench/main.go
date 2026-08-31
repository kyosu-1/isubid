package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/isucon/isucandar"
	"github.com/isucon/isucandar/failure"
)

func main() {
	target := flag.String("target", "http://localhost:8080", "ベンチ対象のベースURL")
	duration := flag.Duration("duration", 60*time.Second, "負荷走行時間")
	prepareOnly := flag.Bool("prepare-only", false, "Prepare(整合性チェック)のみ実行")
	bidders := flag.Int("bidders", 8, "入札者worker数")
	watchers := flag.Int("watchers", 4, "ウォッチャーworker数")
	notifiers := flag.Int("notifiers", 2, "通知確認worker数")
	sellers := flag.Int("sellers", 2, "出品者worker数")
	visitors := flag.Int("visitors", 2, "ページロードを行う閲覧者worker数")
	snapshotPath := flag.String("snapshot", "", "初期データの正解スナップショット(空なら生成データ非搭載モード)")
	flag.Parse()

	var snap *Snapshot
	if *snapshotPath != "" {
		var err error
		snap, err = LoadSnapshot(*snapshotPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	s := &Scenario{
		Target:      *target,
		PrepareOnly: *prepareOnly,
		Bidders:     *bidders,
		Watchers:    *watchers,
		Notifiers:   *notifiers,
		Sellers:     *sellers,
		Visitors:    *visitors,
		Listings:    newListingPubSub(),
		Ledger:      NewLedger(),
		Snapshot:    snap,
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
	for _, st := range scoredTags {
		count := breakdown[st.Tag]
		pt := count * scoreTable[st.Tag]
		fmt.Printf("  %-25s: %d回 (%d点)\n", st.Name, count, pt)
	}
	fmt.Printf("ERRORS: %d件 (critical: %d件)\n", len(errs), criticalCount)

	// 採点対象が「ほぼ成功していない」走行を落とす。エラー上限が絶対件数である
	// ため、10秒でタイムアウトする「遅い全滅」は数十件のエラーしか生まず、
	// 採点対象が軒並み0回のまま PASS が出ていた(docs/phase4-notes.md 持ち越し2)。
	floor := livenessFloor(*duration)
	workers := workerCounts{
		Bidders: *bidders, Watchers: *watchers, Notifiers: *notifiers, Sellers: *sellers,
		Visitors: *visitors,
	}
	dead := checkLiveness(breakdown, floor, workers)
	if len(dead) == 0 {
		checked := 0
		for _, st := range scoredTags {
			if livenessRequired(st.Tag, workers) {
				checked++
			}
		}
		fmt.Printf("LIVENESS: PASS (floor %d回、判定対象%d/%d本すべて到達)\n", floor, checked, len(scoredTags))
	} else {
		fmt.Printf("LIVENESS: FAIL (floor %d回)\n", floor)
		for _, st := range dead {
			fmt.Printf("  %-25s: %d回\n", st.Name, breakdown[st.Tag])
		}
	}

	pass := criticalCount == 0 && appCount <= errorLimit && total > 0 && len(dead) == 0
	if pass {
		fmt.Println("RESULT: PASS")
		return
	}
	fmt.Println("RESULT: FAIL")
	os.Exit(1)
}
