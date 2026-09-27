package engine_test

import (
	"testing"
	"time"

	"github.com/guoyyit/quant-backtest-go/pkg/broker"
	"github.com/guoyyit/quant-backtest-go/pkg/engine"
	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/order"
	"github.com/guoyyit/quant-backtest-go/pkg/strategy"
)

func haltedFeed(t *testing.T) marketdata.Feed {
	d := 24 * time.Hour
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(i int, halted, up, down bool) marketdata.Bar {
		return marketdata.Bar{
			Symbol: "TGT", StartTime: t0.Add(time.Duration(i) * d), Interval: d,
			Open: 100, High: 101, Low: 99, Close: 100, Volume: 1,
			Halted: halted, LimitUp: up, LimitDown: down,
		}
	}
	return marketdata.NewMemoryFeed(d, []marketdata.Bar{mk(0, false, false, false), mk(1, true, false, false), mk(2, false, true, false), mk(3, false, false, true), mk(4, false, false, false)})
}

// submitOnce buys on bar 0; the market order targets bar 1 (halted).
type submitOnce struct{ symbol string }

func (st *submitOnce) OnBar(ctx strategy.Context, bars map[string]marketdata.Bar) {
	if len(ctx.History(st.symbol, 1000)) == 1 {
		ctx.Submit(order.Order{Symbol: st.symbol, Side: order.Buy, Type: order.Market, Qty: 10})
	}
}

func TestHaltedAndLimitBarsSkippedThenMarketFillsLater(t *testing.T) {
	feed := haltedFeed(t)
	cfg := engine.Config{StartingCash: 1e6, Broker: broker.Config{}}
	eng := engine.New(cfg, feed)
	res, err := eng.Run(&submitOnce{symbol: "TGT"})
	if err != nil {
		t.Fatal(err)
	}
	// Distinct skip reasons recorded.
	got := map[marketdata.SkipReason]bool{}
	for _, sk := range res.Skips {
		got[sk.Reason] = true
	}
	for _, want := range []marketdata.SkipReason{marketdata.SkipHalted, marketdata.SkipLimitUp, marketdata.SkipLimitDown} {
		if !got[want] {
			t.Fatalf("missing skip reason %q; got %+v", want, got)
		}
	}
	// A buy order: blocked on bar1 (halted) and bar2 (limit-up), then allowed
	// on bar3 (limit-down blocks sells only) and fills exactly once.
	if len(res.Fills) != 1 {
		t.Fatalf("want exactly 1 fill, got %d (%+v)", len(res.Fills), res.Fills)
	}
	fill := res.Fills[0]
	allowed := time.Date(2024, time.January, 4, 0, 0, 0, 0, time.UTC)
	if !fill.Time.Equal(allowed) {
		t.Fatalf("fill at %v, want %v (limit-down permits buying)", fill.Time, allowed)
	}
}

// oneShot submits a single market buy the first time it sees the symbol bar,
// and never again (robust to later missing bars).
type oneShot struct {
	symbol string
	done   bool
}

func (st *oneShot) OnBar(ctx strategy.Context, bars map[string]marketdata.Bar) {
	if st.done {
		return
	}
	if _, ok := bars[st.symbol]; ok {
		ctx.Submit(order.Order{Symbol: st.symbol, Side: order.Buy, Type: order.Market, Qty: 10})
		st.done = true
	}
}

func TestMissingBarProducesSkipAndNoTrade(t *testing.T) {
	d := 24 * time.Hour
	t0 := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	// OTHER has a bar at day1; TGT only at day0 and day2 -> day1 is missing.
	bars := []marketdata.Bar{
		{Symbol: "TGT", StartTime: t0, Interval: d, Open: 100, High: 101, Low: 99, Close: 100},
		{Symbol: "OTHER", StartTime: t0.Add(d), Interval: d, Open: 50, High: 51, Low: 49, Close: 50},
		{Symbol: "TGT", StartTime: t0.Add(2 * d), Interval: d, Open: 100, High: 101, Low: 99, Close: 100},
	}
	feed := marketdata.NewMemoryFeed(d, bars)
	cfg := engine.Config{StartingCash: 1e6, Broker: broker.Config{}}
	eng := engine.New(cfg, feed)
	res, err := eng.Run(&oneShot{symbol: "TGT"})
	if err != nil {
		t.Fatal(err)
	}
	var sawMissing bool
	for _, sk := range res.Skips {
		if sk.Symbol == "TGT" && sk.Reason == marketdata.SkipMissing && sk.Time.Equal(t0.Add(d)) {
			sawMissing = true
		}
	}
	if !sawMissing {
		t.Fatalf("expected missing skip for TGT at day1; got %+v", res.Skips)
	}
	// Order placed day0: day1 missing -> no trade; fills day2 (first later bar).
	if len(res.Fills) != 1 || !res.Fills[0].Time.Equal(t0.Add(2*d)) {
		t.Fatalf("want single fill on day2, got %+v", res.Fills)
	}
}
