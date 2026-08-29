package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	scale := flag.String("scale", "small", "生成規模 (small|medium|full)")
	seed := flag.Int64("seed", DefaultSeed, "乱数シード")
	out := flag.String("out", "out", "出力ディレクトリ")
	flag.Parse()

	cfg, ok := Scales[*scale]
	if !ok {
		fmt.Fprintf(os.Stderr, "不明な scale: %q (small|medium|full)\n", *scale)
		os.Exit(1)
	}
	cfg.Seed = *seed

	ds := Generate(cfg)
	if err := WriteSQL(*out, ds); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := WriteSnapshot(*out, BuildSnapshot(ds)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("scale=%s seed=%d users=%d auctions=%d bids=%d notifications=%d -> %s\n",
		cfg.Name, cfg.Seed, len(ds.Users), len(ds.Auctions), len(ds.Bids), len(ds.Notifications), *out)
}
