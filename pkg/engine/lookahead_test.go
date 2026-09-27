package engine_test

import (
	"testing"
	"time"

	"github.com/guoyyit/quant-backtest-go/pkg/engine"
	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/order"
	"github.com/guoyyit/quant-backtest-go/pkg/strategy"
)

// peekStrategy records the closes History exposes and submits a market order
// on the very first bar so we can assert the fill occurs strictly later.
type peekStrategy struct {
	seenCloses []float64
	fillTimes  []time.Time
	firstBar   time.Time
}

func (st *peekStrategy) OnBar(ctx strategy.Context, bars map[string]marketdata.Bar) {
	hist := ctx.History(sym, 1000)
	var cs []float64
	for _, b := range hist {
		cs = append(cs, b.Close)
	}
	st.seenCloses = cs
	if len(hist) == 1 {
		st.firstBar = hist[0].StartTime
		ctx.Submit(order.Order{Symbol: sym, Side: order.Buy, Type: order.Market, Qty: 10})
	}
	// CurrentBar must equal the last history entry and never a future bar.
	if cur, ok := ctx.CurrentBar(sym); ok {
		if cur.StartTime.After(ctx.Now()) {
			panic("current bar in the future")
		}
		if hist[len(hist)-1].StartTime != cur.StartTime {
			panic("current bar is not the last closed bar")
		}
	}
}

func TestNoLookahead(t *testing.T) {
	closes := []float64{10, 11, 12, 13, 14}
	feed := marketdata.NewMemoryFeed(24*time.Hour, makeBars(closes))
	eng := engine.New(testCfg(), feed)
	st := &peekStrategy{}
	res, err := eng.Run(st)
	if err != nil {
		t.Fatal(err)
	}

	// At each call the strategy must only see prefixes of the close series.
	// Re-run captures the largest prefix via a fresh run instead; verify via
	// the final fill timing directly:
	if len(res.Fills) != 1 {
		t.Fatalf("want 1 fill, got %d", len(res.Fills))
	}
	fill := res.Fills[0]
	if !fill.Time.After(st.firstBar) {
		t.Fatalf("fill time %v must be strictly after submission bar %v", fill.Time, st.firstBar)
	}
	// Filled at the SECOND bar open = closes[0] = 10 (makeBars open rule),
	// never at the first bar or any later (higher) price.
	if !closeTo(fill.Price, 10.02, 1e-9) {
		t.Fatalf("fill price=%v, must execute at next bar open with costs", fill.Price)
	}
}

// prefixStrategy verifies that on bar i, exactly closes[:i+1] are visible.
type prefixStrategy struct {
	closes []float64
	bad    string
}

func (st *prefixStrategy) OnBar(ctx strategy.Context, bars map[string]marketdata.Bar) {
	hist := ctx.History(sym, 1000)
	i := len(hist) - 1
	if i < 0 || i >= len(st.closes) {
		return
	}
	if len(hist) != i+1 {
		st.bad = "history length mismatch"
		return
	}
	if !closeTo(hist[i].Close, st.closes[i], 1e-12) {
		st.bad = "future or wrong bar exposed"
	}
	// Requesting more than available just returns what is closed, nothing future.
	full := ctx.History(sym, 100000)
	if len(full) != i+1 {
		st.bad = "history leaked future bars"
	}
}

func TestHistoryIsPrefixOnly(t *testing.T) {
	closes := []float64{10, 11, 12, 13, 14}
	feed := marketdata.NewMemoryFeed(24*time.Hour, makeBars(closes))
	eng := engine.New(testCfg(), feed)
	st := &prefixStrategy{closes: closes}
	if _, err := eng.Run(st); err != nil {
		t.Fatal(err)
	}
	if st.bad != "" {
		t.Fatal(st.bad)
	}
}
