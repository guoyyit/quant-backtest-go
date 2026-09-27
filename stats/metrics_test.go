package stats

import (
	"testing"
	"time"

	"github.com/guoyyit/quant-backtest-go/backtest"
)

var base = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

func eq(t time.Time, cash, val, turn float64) backtest.EquityPoint {
	return backtest.EquityPoint{Time: t, Cash: cash, PositionValue: val, Equity: cash + val, Turnover: turn}
}

func TestComputeBasicMetrics(t *testing.T) {
	// 权益：100 -> 110 -> 99 -> 108.9
	equity := []backtest.EquityPoint{
		eq(base, 100, 0, 0),
		eq(base.AddDate(0, 0, 1), 110, 0, 100),
		eq(base.AddDate(0, 0, 2), 99, 0, 200),
		eq(base.AddDate(0, 0, 3), 108.9, 0, 300),
	}
	fills := []backtest.Fill{
		{Symbol: "AAA", Side: backtest.Sell, Quantity: 1, Price: 110, Commission: 0, RealizedPL: 10},
		{Symbol: "AAA", Side: backtest.Sell, Quantity: 1, Price: 99, Commission: 0, RealizedPL: -11},
		{Symbol: "AAA", Side: backtest.Sell, Quantity: 1, Price: 108.9, Commission: 0, RealizedPL: 9.9},
	}
	m := Compute(equity, fills, Options{PeriodsPerYear: 252})

	if d := m.TotalReturn - 0.089; d > 1e-12 || d < -1e-12 {
		t.Errorf("total return = %.6f, want 0.089", m.TotalReturn)
	}
	// 峰值 110 -> 谷值 99，回撤 10%
	if m.MaxDrawdown < 0.099999 || m.MaxDrawdown > 0.100001 {
		t.Errorf("max drawdown = %.6f, want 0.10", m.MaxDrawdown)
	}
	if m.WinRate < 0.66666 || m.WinRate > 0.66667 {
		t.Errorf("win rate = %.6f, want 2/3", m.WinRate)
	}
	avg := (100.0 + 110 + 99 + 108.9) / 4
	wantTurn := 300.0 / avg
	if m.TurnoverRate < wantTurn-1e-9 || m.TurnoverRate > wantTurn+1e-9 {
		t.Errorf("turnover = %.6f, want %.6f", m.TurnoverRate, wantTurn)
	}
	if m.NumFills != 3 || m.NumPeriods != 4 {
		t.Errorf("counts = %d/%d", m.NumFills, m.NumPeriods)
	}

	// 确定性：再算一次逐项相等。
	m2 := Compute(equity, fills, Options{PeriodsPerYear: 252})
	if m != m2 {
		t.Errorf("metrics not deterministic:\n%+v\n%+v", m, m2)
	}
}
