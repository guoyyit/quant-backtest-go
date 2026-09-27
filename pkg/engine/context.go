package engine

import (
	"math/rand"
	"sort"
	"time"

	"github.com/guoyyit/quant-backtest-go/pkg/broker"
	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/order"
	"github.com/guoyyit/quant-backtest-go/pkg/portfolio"
	"github.com/guoyyit/quant-backtest-go/pkg/strategy"
)

// ctx is the engine-backed strategy.Context. Every data accessor is bounded by
// now so a strategy can never observe a bar that has not yet closed.
type ctx struct {
	engine *Engine
	now    time.Time
	params strategy.Params
	rng    *rand.Rand
}

func newCtx(e *Engine) *ctx {
	return &ctx{engine: e, rng: e.rng}
}

// bind resets the context for one timeline point.
func (c *ctx) bind(t time.Time) { c.now = t }

// setParams attaches the active scan parameters (nil for a plain run).
func (c *ctx) setParams(p strategy.Params) { c.params = p }

func (c *ctx) Now() time.Time          { return c.now }
func (c *ctx) Params() strategy.Params { return c.params }
func (c *ctx) RNG() strategy.RNG       { return c.rng }

func (c *ctx) brokerCtx() *broker.Broker          { return c.engine.broker }
func (c *ctx) portfolioCtx() *portfolio.Portfolio { return c.engine.portfolio }

// CurrentBar returns the closed bar at now for a symbol.
func (c *ctx) CurrentBar(symbol string) (marketdata.Bar, bool) {
	return c.engine.feed.BarAt(symbol, c.now)
}

// History returns the n most recent closed bars ending at now, chronological.
// It scans only timeline points <= now.
func (c *ctx) History(symbol string, n int) []marketdata.Bar {
	if n <= 0 {
		return nil
	}
	tl := c.engine.feed.Timeline()
	var out []marketdata.Bar
	for i := len(tl) - 1; i >= 0 && len(out) < n; i-- {
		t := tl[i]
		if t.After(c.now) {
			continue
		}
		if bar, ok := c.engine.feed.BarAt(symbol, t); ok {
			out = append(out, bar)
		}
	}
	// reverse to chronological order
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartTime.Before(out[j].StartTime)
	})
	return out
}

// Submit stamps the order with the current bar time before queuing it. The
// broker will not match it until a later bar.
func (c *ctx) Submit(o order.Order) (order.RejectReason, error) {
	if o.Submitted.IsZero() {
		o.Submitted = c.now
	}
	return c.engine.broker.Submit(o)
}

func (c *ctx) Cancel(id string) bool        { return c.engine.broker.Cancel(id) }
func (c *ctx) PendingOrders() []order.Order { return c.engine.broker.PendingOrders() }
func (c *ctx) Cash() float64                { return c.engine.portfolio.Cash() }
func (c *ctx) Positions() map[string]portfolio.Position {
	return c.engine.portfolio.Positions()
}
