package portfolio_test

import (
	"math"
	"testing"
	"time"

	"github.com/guoyyit/quant-backtest-go/pkg/order"
	"github.com/guoyyit/quant-backtest-go/pkg/portfolio"
)

func TestLongRoundTripRealizedPnL(t *testing.T) {
	p := portfolio.New(10000)
	p.Apply(order.Fill{Symbol: "A", Side: order.Buy, Qty: 10, Price: 100, Commission: 1, Time: t0()})
	p.Apply(order.Fill{Symbol: "A", Side: order.Sell, Qty: 10, Price: 110, Commission: 1, Time: t0()})
	// Realized = (110-100)*10 - 2 commissions = 98.
	if got := p.RealizedPnL(); math.Abs(got-98) > 1e-9 {
		t.Fatalf("realized=%v want 98", got)
	}
	if q := p.Position("A").Qty; q != 0 {
		t.Fatalf("qty=%v want 0", q)
	}
	// Cash = 10000 - 1000 -1 + 1100 -1 = 10098.
	if got := p.Cash(); math.Abs(got-10098) > 1e-9 {
		t.Fatalf("cash=%v want 10098", got)
	}
}

func TestPartialSellRealizedAndRemainder(t *testing.T) {
	p := portfolio.New(10000)
	p.Apply(order.Fill{Symbol: "A", Side: order.Buy, Qty: 10, Price: 100, Time: t0()})
	p.Apply(order.Fill{Symbol: "A", Side: order.Sell, Qty: 4, Price: 125, Time: t0()})
	if got := p.RealizedPnL(); math.Abs(got-100) > 1e-9 {
		t.Fatalf("realized=%v want 100", got)
	}
	if q := p.Position("A").Qty; q != 6 {
		t.Fatalf("qty=%v want 6", q)
	}
}

func TestShortRoundTripRealizedPnL(t *testing.T) {
	p := portfolio.New(10000)
	// Sell short 10 @100 then cover 10 @90 -> +100.
	p.Apply(order.Fill{Symbol: "B", Side: order.Sell, Qty: 10, Price: 100, Commission: 0, Time: t0()})
	if q := p.Position("B").Qty; q != -10 {
		t.Fatalf("short qty=%v want -10", q)
	}
	p.Apply(order.Fill{Symbol: "B", Side: order.Buy, Qty: 10, Price: 90, Commission: 0, Time: t0()})
	if got := p.RealizedPnL(); math.Abs(got-100) > 1e-9 {
		t.Fatalf("short realized=%v want 100", got)
	}
	if q := p.Position("B").Qty; q != 0 {
		t.Fatalf("qty=%v want 0", q)
	}
}

func TestMarkToMarketUnrealized(t *testing.T) {
	p := portfolio.New(10000)
	p.Apply(order.Fill{Symbol: "A", Side: order.Buy, Qty: 10, Price: 100, Time: t0()})
	snap := p.MarkToMarket(t0(), map[string]float64{"A": 108})
	if math.Abs(snap.UnrealizedPnL-80) > 1e-9 {
		t.Fatalf("unrealized=%v want 80", snap.UnrealizedPnL)
	}
	// Equity = cash (10000-1000=9000) + position value 1080 = 10080.
	if math.Abs(snap.Equity-10080) > 1e-9 {
		t.Fatalf("equity=%v want 10080", snap.Equity)
	}
}

func t0() time.Time { return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) }
