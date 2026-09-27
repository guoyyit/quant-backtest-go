// Command backtest runs a local, reproducible backtest or parameter scan.
//
// Single run:
//
//	backtest -csv data/AAA.csv -symbol AAA -fast 5 -slow 20 -cash 1000000
//
// Parameter scan (comma-separated values, executed in parallel):
//
//	backtest -csv data/AAA.csv -scan-fast 5,10 -scan-slow 20,40 -workers 4
//
// All data is read from local files; no network access is required.
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/guoyyit/quant-backtest-go/examples/ma"
	"github.com/guoyyit/quant-backtest-go/pkg/broker"
	"github.com/guoyyit/quant-backtest-go/pkg/engine"
	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/scan"
	"github.com/guoyyit/quant-backtest-go/pkg/strategy"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "backtest:", err)
		os.Exit(1)
	}
}

func run() error {
	csvPath := flag.String("csv", "", "path to CSV bars: time,open,high,low,close,volume[,halted,limit_up,limit_down]")
	symbol := flag.String("symbol", "AAA", "symbol name")
	interval := flag.Duration("interval", 24*time.Hour, "bar interval")
	cash := flag.Float64("cash", 1_000_000, "starting cash")
	seed := flag.Int64("seed", 1, "fixed RNG seed")
	fast := flag.Float64("fast", 5, "fast MA window (single run)")
	slow := flag.Float64("slow", 20, "slow MA window (single run)")
	scanFast := flag.String("scan-fast", "", "comma-separated fast windows for a scan")
	scanSlow := flag.String("scan-slow", "", "comma-separated slow windows for a scan")
	workers := flag.Int("workers", 0, "parallel workers (default NumCPU)")
	equityOut := flag.String("equity-out", "", "optional path to write the equity curve as CSV (single run)")
	flag.Parse()

	if *csvPath == "" {
		return fmt.Errorf("-csv is required")
	}

	base := engine.Config{
		StartingCash: *cash,
		Broker: broker.Config{
			CommissionRate: 0.0003,
			HalfSpread:     0.001,
			SlippageRate:   0.001,
		},
		Seed:           *seed,
		PeriodsPerYear: 252,
	}
	loadFeed := func() (marketdata.Feed, error) {
		cf, err := marketdata.LoadCSVFile(*symbol, *csvPath, *interval)
		if err != nil {
			return nil, err
		}
		return marketdata.NewMultiFeed(cf)
	}

	if *scanFast != "" || *scanSlow != "" {
		return runScan(base, loadFeed, *scanFast, *scanSlow, *fast, *slow, *workers)
	}

	feed, err := loadFeed()
	if err != nil {
		return err
	}
	params := strategy.Params{"fast": *fast, "slow": *slow}
	res, err := engine.New(base, feed).RunWithParams(ma.New(params), params)
	if err != nil {
		return err
	}
	printResult(params, res)
	if *equityOut != "" {
		return writeEquity(*equityOut, res)
	}
	return nil
}

func runScan(base engine.Config, loadFeed scan.FeedFactory, sf, ss string, defFast, defSlow float64, workers int) error {
	fasts, err := parseList(sf, defFast)
	if err != nil {
		return fmt.Errorf("scan-fast: %w", err)
	}
	slows, err := parseList(ss, defSlow)
	if err != nil {
		return fmt.Errorf("scan-slow: %w", err)
	}
	grid := scan.Grid{"fast": fasts, "slow": slows}
	combos := scan.Expand(grid)
	rows := scan.Run(context.Background(), combos, ma.New, loadFeed, base,
		scan.Options{Workers: workers, Deterministic: true})

	fmt.Println("fast,slow,total_return,max_drawdown,sharpe,turnover,final_equity,fills")
	for _, r := range rows {
		if r.Err != nil {
			return fmt.Errorf("combo %+v: %w", r.Params, r.Err)
		}
		m := r.Result.Metrics
		fmt.Printf("%v,%v,%.6f,%.6f,%.4f,%.4f,%.2f,%d\n",
			r.Params["fast"], r.Params["slow"],
			m.TotalReturn, m.MaxDrawdown, m.Sharpe, m.Turnover,
			m.FinalEquity, len(r.Result.Fills))
	}
	return nil
}

func parseList(list string, def float64) ([]float64, error) {
	if strings.TrimSpace(list) == "" {
		return []float64{def}, nil
	}
	parts := strings.Split(list, ",")
	out := make([]float64, 0, len(parts))
	for _, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func printResult(p strategy.Params, r *engine.Result) {
	m := r.Metrics
	fmt.Printf("params: fast=%v slow=%v\n", p["fast"], p["slow"])
	fmt.Printf("fills:        %d\n", len(r.Fills))
	fmt.Printf("rejections:   %d\n", len(r.Rejections))
	fmt.Printf("skip events:  %d\n", len(r.Skips))
	fmt.Printf("start equity: %.2f\n", m.StartEquity)
	fmt.Printf("final equity: %.2f\n", m.FinalEquity)
	fmt.Printf("total return: %.6f (%.2f%%)\n", m.TotalReturn, 100*m.TotalReturn)
	fmt.Printf("CAGR:         %.6f\n", m.CAGR)
	fmt.Printf("ann. vol:     %.6f\n", m.AnnualVol)
	fmt.Printf("sharpe:       %.4f\n", m.Sharpe)
	fmt.Printf("max drawdown: %.6f (%.2f%%)\n", m.MaxDrawdown, 100*m.MaxDrawdown)
	fmt.Printf("turnover:     %.4f\n", m.Turnover)
}

func writeEquity(path string, r *engine.Result) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"time", "equity", "cash"}); err != nil {
		return err
	}
	for _, pt := range r.Equity {
		if err := w.Write([]string{
			pt.Time.Format(time.RFC3339),
			strconv.FormatFloat(pt.Equity, 'f', 6, 64),
			strconv.FormatFloat(pt.Cash, 'f', 6, 64),
		}); err != nil {
			return err
		}
	}
	return nil
}
