package engine_test

import (
	"math"
	"testing"
	"time"

	"github.com/guoyyit/quant-backtest-go/pkg/broker"
	"github.com/guoyyit/quant-backtest-go/pkg/engine"
	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/order"
	"github.com/guoyyit/quant-backtest-go/pkg/strategy"
)

const sym = "AAA"

func makeBars(closes []float64) []marketdata.Bar {
	t0 := time.Date(2024, 1, 1, 9, 30, 0, 0, time.UTC)
	d := 24 * time.Hour
	bars := make([]marketdata.Bar, len(closes))
	for i, c := range closes {
		o := c
		if i > 0 {
			o = closes[i-1]
		}
		lo, hi := o, c
		if lo > hi {
			lo, hi = hi, lo
		}
		bars[i] = marketdata.Bar{
			Symbol: sym, StartTime: t0.Add(time.Duration(i) * d), Interval: d,
			Open: o, High: hi, Low: lo, Close: c, Volume: 1000,
		}
	}
	return bars
}

// roundTrip buys on the first bar and sells on the second-to-last bar so both
// orders have a later bar on which to execute.
type roundTrip struct {
	sellOn int
}

func (st *roundTrip) OnBar(ctx strategy.Context, bars map[string]marketdata.Bar) {
	hist := ctx.History(sym, 1000)
	switch len(hist) {
	case 1:
		ctx.Submit(order.Order{Symbol: sym, Side: order.Buy, Type: order.Market, Qty: 100})
	case st.sellOn:
		ctx.Submit(order.Order{Symbol: sym, Side: order.Sell, Type: order.Market, Qty: 100})
	}
}

func testCfg() engine.Config {
	return engine.Config{
		StartingCash: 100000,
		Broker: broker.Config{
			CommissionRate: 0.0003,
			HalfSpread:     0.001,
			SlippageRate:   0.001,
		},
		Seed: 1,
	}
}

func TestMarketRoundTripAccounting(t *testing.T) {
	closes := []float64{10, 10, 11, 12, 13, 13}
	feed := marketdata.NewMemoryFeed(24*time.Hour, makeBars(closes))
	eng := engine.New(testCfg(), feed)
	res, err := eng.Run(&roundTrip{sellOn: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fills) != 2 {
		t.Fatalf("want 2 fills, got %d: %+v", len(res.Fills), res.Fills)
	}
	buy, sell := res.Fills[0], res.Fills[1]
	if !closeTo(buy.Price, 10.02, 1e-9) {
		t.Fatalf("buy price=%v want 10.02", buy.Price)
	}
	if !closeTo(sell.Price, 12.974, 1e-9) {
		t.Fatalf("sell price=%v want 12.974", sell.Price)
	}
	// Commissions.
	wantBuyComm := 0.0003 * 1002.0
	wantSellComm := 0.0003 * 1297.4
	if !closeTo(buy.Commission, wantBuyComm, 1e-9) {
		t.Fatalf("buy comm=%v want %v", buy.Commission, wantBuyComm)
	}
	if !closeTo(sell.Commission, wantSellComm, 1e-9) {
		t.Fatalf("sell comm=%v want %v", sell.Commission, wantSellComm)
	}
	// Final cash & realized PnL.
	wantCash := 100000 - 1002 - wantBuyComm + 1297.4 - wantSellComm
	if !closeTo(res.Final.Cash, wantCash, 1e-7) {
		t.Fatalf("cash=%v want %v", res.Final.Cash, wantCash)
	}
	wantPnL := (12.974-10.02)*100 - wantBuyComm - wantSellComm
	if !closeTo(res.Final.RealizedPnL, wantPnL, 1e-7) {
		t.Fatalf("realized=%v want %v", res.Final.RealizedPnL, wantPnL)
	}
	// Flat after round trip.
	if q := res.Final.Positions[sym].Qty; math.Abs(q) > 1e-12 {
		t.Fatalf("position qty=%v want 0", q)
	}
	if !closeTo(res.Metrics.TotalReturn, wantCash/100000-1, 1e-9) {
		t.Fatalf("total return=%v want %v", res.Metrics.TotalReturn, wantCash/100000-1)
	}
}

func closeTo(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}
