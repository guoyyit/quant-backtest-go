package metrics

import (
	"math"
	"time"
)

// Point is one equity/NAV observation.
type Point struct {
	Time   time.Time
	Equity float64
	Cash   float64
}

// TradeStat records traded notional per timestamp for turnover.
type TradeStat struct {
	Time     time.Time
	Notional float64
}

// Metrics are summary risk/return statistics.
type Metrics struct {
	TotalReturn    float64
	CAGR           float64
	AnnualVol      float64
	Sharpe         float64
	MaxDrawdown    float64
	Turnover       float64
	FinalEquity    float64
	StartEquity    float64
	PeriodsPerYear float64
}

// Compute derives metrics from an equity curve and per-bar traded notional.
// periodsPerYear annualizes Sharpe/volatility (e.g. 252 daily bars).
//
// The curve may contain a leading synthetic point at a zero time (opening
// book); CAGR is based on the first and last timestamped observations, while
// returns/volatility/Sharpe/drawdown use the full series in order.
func Compute(equity []Point, traded []TradeStat, periodsPerYear float64) Metrics {
	m := Metrics{PeriodsPerYear: periodsPerYear}
	if len(equity) == 0 {
		return m
	}
	m.StartEquity = equity[0].Equity
	m.FinalEquity = equity[len(equity)-1].Equity
	if m.StartEquity != 0 {
		m.TotalReturn = m.FinalEquity/m.StartEquity - 1
	}
	m.MaxDrawdown = MaxDrawdown(equity)
	m.Sharpe = Sharpe(equity, periodsPerYear)
	m.AnnualVol = annualVol(equity, periodsPerYear)
	m.CAGR = cagr(equity)
	m.Turnover = Turnover(equity, traded)
	return m
}

// returns builds simple per-step returns from the equity curve.
func returns(equity []Point) []float64 {
	var r []float64
	for i := 1; i < len(equity); i++ {
		prev := equity[i-1].Equity
		if prev != 0 {
			r = append(r, equity[i].Equity/prev-1)
		}
	}
	return r
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func std(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	mu := mean(xs)
	var ss float64
	for _, x := range xs {
		d := x - mu
		ss += d * d
	}
	// Population standard deviation is used for annualized volatility.
	return math.Sqrt(ss / float64(len(xs)))
}

// MaxDrawdown returns the largest peak-to-trough relative decline in [0,1].
func MaxDrawdown(equity []Point) float64 {
	var peak, mdd float64
	for i, p := range equity {
		if i == 0 || p.Equity > peak {
			peak = p.Equity
		}
		if peak > 0 {
			dd := (peak - p.Equity) / peak
			if dd > mdd {
				mdd = dd
			}
		}
	}
	return mdd
}

// Sharpe computes the annualized Sharpe ratio (risk-free rate = 0) from
// per-step equity returns.
func Sharpe(equity []Point, periodsPerYear float64) float64 {
	r := returns(equity)
	if len(r) < 2 {
		return 0
	}
	sd := sampleStd(r)
	if sd == 0 {
		return 0
	}
	return mean(r) / sd * math.Sqrt(periodsPerYear)
}

func sampleStd(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	mu := mean(xs)
	var ss float64
	for _, x := range xs {
		d := x - mu
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(xs)-1))
}

func annualVol(equity []Point, periodsPerYear float64) float64 {
	r := returns(equity)
	if len(r) < 2 {
		return 0
	}
	return std(r) * math.Sqrt(periodsPerYear)
}

// cagr annualizes total return over the span between first and last
// timestamped observations (the leading synthetic point is skipped).
func cagr(equity []Point) float64 {
	var start, end Point
	var haveStart bool
	for _, p := range equity {
		if !p.Time.IsZero() {
			start = p
			haveStart = true
			break
		}
	}
	if !haveStart {
		return 0
	}
	end = equity[len(equity)-1]
	years := end.Time.Sub(start.Time).Hours() / (24 * 365.25)
	if years <= 0 || start.Equity <= 0 {
		return 0
	}
	return math.Pow(end.Equity/start.Equity, 1/years) - 1
}

// Turnover is total traded notional divided by the time-weighted average equity.
// It is a round-trip-independent activity measure; dividing by 2 gives the
// one-sided convention if preferred.
func Turnover(equity []Point, traded []TradeStat) float64 {
	var notional float64
	for _, tr := range traded {
		notional += tr.Notional
	}
	avg := averageEquity(equity)
	if avg == 0 {
		return 0
	}
	return notional / avg
}

// averageEquity computes the time-weighted mean equity using point timestamps.
// Consecutive zero-time or duplicate-time steps fall back to a simple mean.
func averageEquity(equity []Point) float64 {
	if len(equity) == 0 {
		return 0
	}
	var area, dur float64
	for i := 1; i < len(equity); i++ {
		dt := equity[i].Time.Sub(equity[i-1].Time).Seconds()
		if dt <= 0 {
			// Fallback: simple arithmetic mean over all observations.
			var s float64
			for _, p := range equity {
				s += p.Equity
			}
			return s / float64(len(equity))
		}
		area += (equity[i-1].Equity + equity[i].Equity) / 2 * dt
		dur += dt
	}
	if dur == 0 {
		var s float64
		for _, p := range equity {
			s += p.Equity
		}
		return s / float64(len(equity))
	}
	return area / dur
}
