package stats

import (
	"math"
	"time"

	"github.com/guoyyit/quant-backtest-go/backtest"
)

// Metrics 为回测绩效指标，全部可由净值序列与逐笔成交复算。
type Metrics struct {
	StartTime     time.Time
	EndTime       time.Time
	InitialEquity float64
	FinalEquity   float64
	TotalReturn   float64 // 累计收益率
	AnnualReturn  float64
	MaxDrawdown   float64 // 最大回撤幅度（正数）
	SharpeRatio   float64
	WinRate       float64 // 产生已实现盈亏的成交中，盈利笔数占比
	TurnoverRate  float64 // 总成交额 / 平均权益
	NumFills      int
	NumPeriods    int
}

type Options struct {
	RiskFreeRate   float64 // 年化无风险利率
	PeriodsPerYear float64 // Bar 年化周期数（日线=252）
}

func DefaultOptions() Options {
	return Options{PeriodsPerYear: 252}
}

// Compute 依据净值序列与成交记录计算指标。纯函数：相同输入逐项相同。
func Compute(equity []backtest.EquityPoint, fills []backtest.Fill, opts Options) Metrics {
	if opts.PeriodsPerYear <= 0 {
		opts.PeriodsPerYear = 252
	}
	m := Metrics{NumFills: len(fills), NumPeriods: len(equity)}
	if len(equity) == 0 {
		return m
	}

	m.StartTime = equity[0].Time
	m.EndTime = equity[len(equity)-1].Time
	m.InitialEquity = equity[0].Equity
	m.FinalEquity = equity[len(equity)-1].Equity

	if m.InitialEquity > 0 {
		m.TotalReturn = m.FinalEquity/m.InitialEquity - 1
	}

	// 逐期收益率与最大回撤。
	periodRets := make([]float64, 0, len(equity)-1)
	peak := equity[0].Equity
	for i := 1; i < len(equity); i++ {
		prev, cur := equity[i-1].Equity, equity[i].Equity
		if prev != 0 {
			periodRets = append(periodRets, cur/prev-1)
		}
		if cur > peak {
			peak = cur
		}
		if peak > 0 {
			dd := 1 - cur/peak
			if dd > m.MaxDrawdown {
				m.MaxDrawdown = dd
			}
		}
	}

	// 年化收益（复利）。
	n := len(equity) - 1
	if n > 0 && m.InitialEquity > 0 && m.FinalEquity > 0 {
		years := float64(n) / opts.PeriodsPerYear
		if years > 0 {
			m.AnnualReturn = math.Pow(m.FinalEquity/m.InitialEquity, 1/years) - 1
		}
	}

	// 夏普比率：逐期超额收益的均值/标准差 × sqrt(周期数/年)。
	if len(periodRets) > 1 {
		rfPer := opts.RiskFreeRate / opts.PeriodsPerYear
		sum := 0.0
		for _, r := range periodRets {
			sum += r - rfPer
		}
		mean := sum / float64(len(periodRets))
		ss := 0.0
		for _, r := range periodRets {
			d := r - rfPer - mean
			ss += d * d
		}
		sd := math.Sqrt(ss / float64(len(periodRets)-1))
		if sd > 0 {
			m.SharpeRatio = mean / sd * math.Sqrt(opts.PeriodsPerYear)
		}
	}

	// 换手率：总成交额（取最后快照的累计值）/ 平均权益。
	avgEquity := 0.0
	for _, pt := range equity {
		avgEquity += pt.Equity
	}
	avgEquity /= float64(len(equity))
	turnover := equity[len(equity)-1].Turnover
	if avgEquity > 0 {
		m.TurnoverRate = turnover / avgEquity
	}

	// 胜率：仅统计实际产生已实现盈亏（平仓）的成交。
	wins, closed := 0, 0
	for _, f := range fills {
		if f.RealizedPL != 0 {
			closed++
			if f.RealizedPL > 0 {
				wins++
			}
		}
	}
	if closed > 0 {
		m.WinRate = float64(wins) / float64(closed)
	}
	return m
}

// ComputeResult 是 Compute 的便捷包装，直接读取回测结果。
func ComputeResult(r *backtest.Result, opts Options) Metrics {
	return Compute(r.Equity, r.Fills, opts)
}
