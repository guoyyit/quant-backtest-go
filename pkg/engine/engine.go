package engine

import (
	"math/rand"
	"time"

	"github.com/guoyyit/quant-backtest-go/pkg/broker"
	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/metrics"
	"github.com/guoyyit/quant-backtest-go/pkg/order"
	"github.com/guoyyit/quant-backtest-go/pkg/portfolio"
	"github.com/guoyyit/quant-backtest-go/pkg/strategy"
)

// Config configures one backtest run.
type Config struct {
	StartingCash float64
	Broker       broker.Config
	StartTime    time.Time
	EndTime      time.Time
	// Seed fixes all strategy RNG draws for reproducibility.
	Seed int64
	// PeriodsPerYear annualizes metrics; defaults to 252 when zero.
	PeriodsPerYear float64
}

// Engine drives the chronological event loop.
type Engine struct {
	cfg       Config
	feed      marketdata.Feed
	portfolio *portfolio.Portfolio
	broker    *broker.Broker
	rng       *rand.Rand
}

// New constructs an Engine.
func New(cfg Config, f marketdata.Feed) *Engine {
	if cfg.StartingCash == 0 {
		cfg.StartingCash = 1_000_000
	}
	if cfg.PeriodsPerYear == 0 {
		cfg.PeriodsPerYear = 252
	}
	p := portfolio.New(cfg.StartingCash)
	bk := broker.New(cfg.Broker, f, p)
	return &Engine{
		cfg:       cfg,
		feed:      f,
		portfolio: p,
		broker:    bk,
		rng:       rand.New(rand.NewSource(cfg.Seed)),
	}
}

// Portfolio exposes the portfolio (mainly for inspection/tests).
func (e *Engine) Portfolio() *portfolio.Portfolio { return e.portfolio }

// Broker exposes the broker.
func (e *Engine) Broker() *broker.Broker { return e.broker }

// Run replays the feed through the strategy with no scan parameters.
func (e *Engine) Run(s strategy.Strategy) (*Result, error) {
	return e.RunWithParams(s, nil)
}

// RunWithParams replays the feed with an explicit parameter set.
//
// Loop invariant: at timeline point t the strategy sees bars with StartTime
// <= t only. Orders submitted at t are matched against t+1 or later, so no
// information after t can affect trading at t (no look-ahead bias).
func (e *Engine) RunWithParams(s strategy.Strategy, params strategy.Params) (*Result, error) {
	res := &Result{}
	ppYear := e.cfg.PeriodsPerYear
	if ppYear == 0 {
		ppYear = 252
	}

	ctx := newCtx(e)
	ctx.setParams(params)
	timeline := e.feed.Timeline()

	// Determine the first in-window timestamp so the opening equity point uses
	// a real, timestamped moment (keeps the curve clean for recomputation).
	var firstTime time.Time
	for _, t := range timeline {
		if !e.cfg.StartTime.IsZero() && t.Before(e.cfg.StartTime) {
			continue
		}
		if !e.cfg.EndTime.IsZero() && t.After(e.cfg.EndTime) {
			break
		}
		firstTime = t
		break
	}

	// Opening equity point so drawdown/return start from initial cash.
	firstSnap := e.portfolio.MarkToMarket(firstTime, nil)
	res.Equity = append(res.Equity, metrics.Point{
		Time: firstTime, Equity: firstSnap.Equity, Cash: firstSnap.Cash,
	})

	var traded []metrics.TradeStat
	for _, t := range timeline {
		if !e.cfg.StartTime.IsZero() && t.Before(e.cfg.StartTime) {
			continue
		}
		if !e.cfg.EndTime.IsZero() && t.After(e.cfg.EndTime) {
			break
		}

		ctx.bind(t)
		e.broker.SetNow(t)

		// Collect bars and skip reasons for this timestamp.
		bars := map[string]marketdata.Bar{}
		var skips []marketdata.SkipEvent
		for _, sym := range e.feed.Symbols() {
			bar, ok := e.feed.BarAt(sym, t)
			if !ok {
				skips = append(skips, marketdata.SkipEvent{
					Time: t, Symbol: sym, Reason: marketdata.SkipMissing,
					Detail: "no bar at timeline point",
				})
				continue
			}
			bars[sym] = bar
			switch {
			case bar.Halted:
				skips = append(skips, marketdata.SkipEvent{Time: t, Symbol: sym, Reason: marketdata.SkipHalted})
			case bar.LimitUp:
				skips = append(skips, marketdata.SkipEvent{Time: t, Symbol: sym, Reason: marketdata.SkipLimitUp})
			case bar.LimitDown:
				skips = append(skips, marketdata.SkipEvent{Time: t, Symbol: sym, Reason: marketdata.SkipLimitDown})
			}
		}
		res.Skips = append(res.Skips, skips...)

		// Match orders placed in earlier bars against the bar that just closed.
		fills, fillRejects := e.broker.ProcessBar(t, skips)
		res.Fills = append(res.Fills, fills...)
		res.Rejections = append(res.Rejections, fillRejects...)
		var notional float64
		for _, fl := range fills {
			notional += fl.Qty * fl.Price
		}
		if notional > 0 {
			traded = append(traded, metrics.TradeStat{Time: t, Notional: notional})
		}

		// Strategy decides after observing the closed bar and the fills.
		rejectBefore := len(e.broker.Rejections())
		s.OnBar(ctx, bars)
		if newR := e.broker.Rejections()[rejectBefore:]; len(newR) > 0 {
			res.Rejections = append(res.Rejections, newR...)
		}

		// Mark to market on current closes (including halted/limit bars:
		// last observed close still values an untradeable position).
		prices := make(map[string]float64, len(bars))
		for sym, bar := range bars {
			prices[sym] = bar.Close
		}
		snap := e.portfolio.MarkToMarket(t, prices)
		res.Equity = append(res.Equity, metrics.Point{Time: t, Equity: snap.Equity, Cash: snap.Cash})
	}

	if fs, ok := s.(strategy.FinishStrategy); ok {
		if last := len(timeline) - 1; last >= 0 {
			ctx.bind(timeline[last])
		}
		fs.OnFinish(ctx)
	}

	res.Fills = e.broker.Fills()
	res.Rejections = e.broker.Rejections()
	res.Final = e.portfolio.MarkToMarket(lastTime(timeline), e.lastPrices(timeline))
	res.Metrics = metrics.Compute(res.Equity, traded, ppYear)
	return res, nil
}

func (e *Engine) lastPrices(timeline []time.Time) map[string]float64 {
	out := map[string]float64{}
	for _, sym := range e.feed.Symbols() {
		for i := len(timeline) - 1; i >= 0; i-- {
			if bar, ok := e.feed.BarAt(sym, timeline[i]); ok {
				out[sym] = bar.Close
				break
			}
		}
	}
	return out
}

func lastTime(tl []time.Time) time.Time {
	if len(tl) == 0 {
		return time.Time{}
	}
	return tl[len(tl)-1]
}

// EquityPoint is one mark-to-market observation.
type EquityPoint = metrics.Point

// Result holds everything produced by one run.
type Result struct {
	Equity     []metrics.Point
	Fills      []order.Fill
	Rejections []order.Rejection
	Skips      []marketdata.SkipEvent
	Final      portfolio.Snapshot
	Metrics    metrics.Metrics
}
