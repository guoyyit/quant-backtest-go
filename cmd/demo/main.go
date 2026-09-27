// Command demo 用合成行情演示：单组回测 + 并行参数扫描。
// 全部数据在内存中生成，不访问任何外部服务。
package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/guoyyit/quant-backtest-go/backtest"
	"github.com/guoyyit/quant-backtest-go/market"
	"github.com/guoyyit/quant-backtest-go/scan"
	"github.com/guoyyit/quant-backtest-go/stats"
	"github.com/guoyyit/quant-backtest-go/strategy"
)

func syntheticFeed() *market.MemoryFeed {
	base := time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC)
	f := market.NewMemoryFeed()
	for i := 0; i < 120; i++ {
		// 带周期波动与轻微上升趋势的合成价格。
		c := 50 + 8*math.Sin(float64(i)*0.12) + 0.08*float64(i)
		f.Add(market.Bar{
			Symbol: "AAA", Time: base.AddDate(0, 0, i),
			Open: c, High: c + 0.4, Low: c - 0.4, Close: c, Volume: 1e6,
		})
	}
	return f
}

func main() {
	cfg := backtest.Config{
		InitialCash: 1_000_000,
		Broker: backtest.BrokerConfig{
			Commission:       backtest.RateCommission{Rate: 0.0003, MinFee: 5},
			Spread:           0.001,
			Slippage:         0.0005,
			MarketCashBuffer: 0.05,
		},
	}

	// 单次回测
	eng := backtest.NewEngine(syntheticFeed(), strategy.NewMACross("AAA", 5, 20, 100), cfg)
	res, err := eng.Run()
	if err != nil {
		panic(err)
	}
	m := stats.ComputeResult(res, stats.DefaultOptions())
	fmt.Printf("single run: final=%.2f return=%.2f%% maxDD=%.2f%% sharpe=%.2f fills=%d\n",
		m.FinalEquity, m.TotalReturn*100, m.MaxDrawdown*100, m.SharpeRatio, m.NumFills)

	// 参数扫描（并行，结果确定性可复现）
	sets := scan.Grid(map[string][]float64{
		"fast": {3, 5, 10},
		"slow": {20, 30, 60},
	})
	build := func(ps scan.ParamSet) (market.Feed, backtest.Strategy, backtest.Config) {
		return syntheticFeed(),
			strategy.NewMACross("AAA", int(ps["fast"]), int(ps["slow"]), 100),
			cfg
	}
	results := scan.Run(context.Background(), sets, build, 4)
	fmt.Println("\nparam scan (fast, slow): final, return%, maxDD%, sharpe")
	for _, r := range results {
		if r.Err != nil {
			fmt.Println("error:", r.Err)
			continue
		}
		mm := stats.ComputeResult(r.Result, stats.DefaultOptions())
		fmt.Printf("  fast=%3.0f slow=%3.0f: %12.2f  %7.2f  %6.2f  %6.2f\n",
			r.Params["fast"], r.Params["slow"],
			mm.FinalEquity, mm.TotalReturn*100, mm.MaxDrawdown*100, mm.SharpeRatio)
	}
}
