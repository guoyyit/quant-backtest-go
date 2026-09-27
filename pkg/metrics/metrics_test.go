package metrics_test

import (
	"math"
	"testing"
	"time"

	"github.com/guoyyit/quant-backtest-go/pkg/metrics"
)

func pts(vals ...float64) []metrics.Point {
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]metrics.Point, len(vals))
	for i, v := range vals {
		out[i] = metrics.Point{Time: t0.Add(time.Duration(i) * 24 * time.Hour), Equity: v, Cash: v}
	}
	return out
}

func TestTotalReturnAndDrawdown(t *testing.T) {
	eq := pts(100, 120, 90, 99)
	m := metrics.Compute(eq, nil, 252)
	if math.Abs(m.TotalReturn-(-0.01)) > 1e-12 {
		t.Fatalf("total return=%v want -0.01", m.TotalReturn)
	}
	// Peak 120 -> trough 90 => 25% drawdown.
	if math.Abs(m.MaxDrawdown-0.25) > 1e-12 {
		t.Fatalf("max drawdown=%v want 0.25", m.MaxDrawdown)
	}
}

func TestMaxDrawdownMonotonicUpIsZero(t *testing.T) {
	if d := metrics.MaxDrawdown(pts(1, 2, 3)); d != 0 {
		t.Fatalf("drawdown=%v want 0", d)
	}
}

func TestTurnover(t *testing.T) {
	eq := pts(100, 100, 100, 100)
	traded := []metrics.TradeStat{
		{Time: eq[1].Time, Notional: 50},
		{Time: eq[2].Time, Notional: 150},
	}
	got := metrics.Turnover(eq, traded)
	if math.Abs(got-2.0) > 1e-12 {
		t.Fatalf("turnover=%v want 2.0", got)
	}
}

func TestSharpeDeterministicFlat(t *testing.T) {
	// Constant equity -> zero vol -> Sharpe 0 (no NaN).
	m := metrics.Compute(pts(100, 100, 100, 100), nil, 252)
	if m.Sharpe != 0 || math.IsNaN(m.Sharpe) {
		t.Fatalf("sharpe=%v want 0", m.Sharpe)
	}
	if m.AnnualVol != 0 {
		t.Fatalf("vol=%v want 0", m.AnnualVol)
	}
}

func TestCAGR(t *testing.T) {
	// Equity doubles over exactly one year (365.25 days) => CAGR 100%.
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	eq := []metrics.Point{
		{Time: t0, Equity: 100},
		{Time: t0.AddDate(1, 0, 0), Equity: 200},
	}
	m := metrics.Compute(eq, nil, 252)
	// Allow tolerance for leap-year calendar length vs 365.25 convention.
	if math.Abs(m.CAGR-1.0) > 5e-3 {
		t.Fatalf("cagr=%v want ~1.0", m.CAGR)
	}
}
