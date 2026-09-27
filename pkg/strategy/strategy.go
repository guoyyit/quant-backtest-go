package strategy

import (
	"time"

	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/order"
	"github.com/guoyyit/quant-backtest-go/pkg/portfolio"
)

// Params is the parameter set for one run (used in parameter scans).
type Params map[string]float64

// Context exposes to a strategy only information available at the current bar.
// A strategy cannot read bars after Now(); implementations serve closed history
// up to and including the current bar only.
type Context interface {
	Now() time.Time
	Params() Params
	// History returns up to n closed bars ending at the current bar (inclusive).
	History(symbol string, n int) []marketdata.Bar
	// CurrentBar returns the just-closed bar for a symbol, if available.
	CurrentBar(symbol string) (marketdata.Bar, bool)
	// Submit queues an order; it may only match on a later bar.
	Submit(o order.Order) (order.RejectReason, error)
	Cancel(orderID string) bool
	PendingOrders() []order.Order
	Cash() float64
	Positions() map[string]portfolio.Position
	// RNG returns a deterministic, per-run seeded random source.
	RNG() RNG
}

// RNG is a minimal deterministic random source for strategies.
type RNG interface {
	Float64() float64
	Intn(n int) int
}

// Strategy produces decisions as bars close.
type Strategy interface {
	// OnBar is called once per timeline point with the just-closed bars.
	OnBar(ctx Context, bars map[string]marketdata.Bar)
}

// FinishStrategy is optionally implemented to receive end-of-run notification.
type FinishStrategy interface {
	OnFinish(ctx Context)
}

// Factory builds a strategy for one parameter combination.
type Factory func(p Params) Strategy
