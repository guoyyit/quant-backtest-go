package scan_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/guoyyit/quant-backtest-go/pkg/engine"
	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/order"
	"github.com/guoyyit/quant-backtest-go/pkg/scan"
	"github.com/guoyyit/quant-backtest-go/pkg/strategy"
)

const ssym = "RNG"

func makeFeed() (marketdata.Feed, error) {
	d := 24 * time.Hour
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	var bars []marketdata.Bar
	// 30 flat bars at price 100 give room for random decisions.
	for i := 0; i < 30; i++ {
		bars = append(bars, marketdata.Bar{
			Symbol: ssym, StartTime: t0.Add(time.Duration(i) * d), Interval: d,
			Open: 100, High: 100.5, Low: 99.5, Close: 100, Volume: 10,
		})
	}
	return marketdata.NewMemoryFeed(d, bars), nil
}

// randStrategy uses its deterministic RNG and a "threshold" param to decide
// whether to buy on the first bar, so different seeds/params can differ but a
// fixed seed+param is identical run-to-run.
type randStrategy struct {
	p     strategy.Params
	fired bool
}

func (st *randStrategy) OnBar(ctx strategy.Context, bars map[string]marketdata.Bar) {
	if st.fired {
		return
	}
	if len(ctx.History(ssym, 2)) == 1 {
		thr := st.p["threshold"]
		if ctx.RNG().Float64() < thr {
			ctx.Submit(order.Order{Symbol: ssym, Side: order.Buy, Type: order.Market, Qty: 1})
		}
		st.fired = true
	}
}

func factory(p strategy.Params) strategy.Strategy {
	return &randStrategy{p: p}
}

func TestExpandGrid(t *testing.T) {
	g := scan.Grid{"a": {1, 2}, "b": {10, 20}}
	combos := scan.Expand(g)
	if len(combos) != 4 {
		t.Fatalf("want 4 combos, got %d", len(combos))
	}
	// Deterministic key order: a varies slowest? Keys sorted a,b; a outer.
	if combos[0]["a"] != 1 || combos[0]["b"] != 10 {
		t.Fatalf("unexpected first combo: %+v", combos[0])
	}
	if combos[3]["a"] != 2 || combos[3]["b"] != 20 {
		t.Fatalf("unexpected last combo: %+v", combos[3])
	}
}

func sig(rows []scan.Row) string {
	var b strings.Builder
	for _, r := range rows {
		if r.Err != nil {
			b.WriteString("E")
			continue
		}
		n := 0
		if r.Result != nil {
			n = len(r.Result.Fills)
		}
		eq := 0.0
		if r.Result != nil && len(r.Result.Equity) > 0 {
			eq = r.Result.Equity[len(r.Result.Equity)-1].Equity
		}
		b.WriteString(itoa(n))
		b.WriteString(":")
		b.WriteString(ftoa(eq))
		b.WriteString(";")
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func ftoa(f float64) string {
	// rounded integer string is enough to distinguish equity outcomes
	return itoa(int(f + 0.5))
}

func runScan(workers int) []scan.Row {
	combos := scan.Expand(scan.Grid{"threshold": {0.1, 0.5, 0.9, 0.001, 0.999}})
	base := engine.Config{StartingCash: 1e6, Seed: 42}
	return scan.Run(context.Background(), combos, factory, makeFeed, base,
		scan.Options{Workers: workers, Deterministic: true})
}

func TestScanReproducibleAcrossWorkerCounts(t *testing.T) {
	s1 := sig(runScan(1))
	s4 := sig(runScan(4))
	s8 := sig(runScan(8))
	if s1 != s4 || s4 != s8 {
		t.Fatalf("non-reproducible scans:\n1=%v\n4=%v\n8=%v", s1, s4, s8)
	}
	// Run twice more sequentially; identical inputs -> identical result.
	if sig(runScan(4)) != s1 {
		t.Fatal("repeat scan differed")
	}
}

func TestScanResultCountAndNoErrors(t *testing.T) {
	rows := runScan(4)
	if len(rows) != 5 {
		t.Fatalf("want 5 rows, got %d", len(rows))
	}
	for i, r := range rows {
		if r.Err != nil {
			t.Fatalf("row %d error: %v", i, r.Err)
		}
		if r.Result == nil {
			t.Fatalf("row %d nil result", i)
		}
	}
}
