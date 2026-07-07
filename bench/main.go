package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/isucon/isucandar"
)

func main() {
	target := flag.String("target", "http://localhost:8080", "ベンチ対象のベースURL")
	flag.Parse()

	b, err := isucandar.NewBenchmark(isucandar.WithoutPanicRecover())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b.AddScenario(&Scenario{Target: *target})

	result := b.Start(context.Background())
	errs := result.Errors.All()
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "ERR: %v\n", e)
		}
		fmt.Println("PREPARE: FAIL")
		os.Exit(1)
	}
	fmt.Println("PREPARE: PASS")
}
