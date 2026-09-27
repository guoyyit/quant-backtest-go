package ma_test

import (
	"fmt"
	"time"

	"github.com/guoyyit/quant-backtest-go/examples/ma"
	"github.com/guoyyit/quant-backtest-go/pkg/broker"
	"github.com/guoyyit/quant-backtest-go/pkg/engine"
	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/strategy"
)

func Example() {
	d := 24 * time.Hour
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	closes := []float64{
		10, 10, 10, 10, 10, // flat -> slow history warms up
		11, 12, 13, // rally triggers a golden cross (buy)
		13, 12, 11, 11, // decline triggers death cross (sell) + bar to fill
	}
	bars := make([]marketdata.Bar, len(closes))
	for i, c := range closes {
		bars[i] = marketdata.Bar{
			Symbol: "AAA", StartTime: t0.Add(time.Duration(i) * d), Interval: d,
			Open: c, High: c + 0.1, Low: c - 0.1, Close: c, Volume: 100,
		}
	}
	feed := marketdata.NewMemoryFeed(d, bars)
	cfg := engine.Config{
		StartingCash:   100000,
		Broker:         broker.Config{CommissionRate: 0.0003},
		Seed:           7,
		PeriodsPerYear: 252,
	}
	params := strategy.Params{"fast": 2, "slow": 4}
	res, err := engine.New(cfg, feed).RunWithParams(ma.New(params), params)
	if err != nil {
		panic(err)
	}
	fmt.Printf("fills=%d rejections=%d finalEquity=%.2f\n",
		len(res.Fills), len(res.Rejections), res.Metrics.FinalEquity)
	// Output: fills=2 rejections=0 finalEquity=91761.73
}
