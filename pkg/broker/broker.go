package broker

import (
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/order"
	"github.com/guoyyit/quant-backtest-go/pkg/portfolio"
)

// Config controls execution costs and constraints.
type Config struct {
	// CommissionRate is charged per unit of traded notional (0.0003 = 3bp).
	CommissionRate float64
	// MinCommission is the minimum commission per fill.
	MinCommission float64
	// HalfSpread is half the bid-ask spread as a fraction of price.
	// Buys pay raw*(1+HalfSpread); sells receive raw*(1-HalfSpread).
	HalfSpread float64
	// SlippageRate is additional adverse movement as a fraction of raw price.
	SlippageRate float64
	// AllowShort permits explicit short sales.
	AllowShort bool
	// MaxShortQty bounds absolute short quantity per symbol (0 = unlimited).
	MaxShortQty float64
}

// ErrRejected is returned with a rejection when an order cannot be accepted.
var ErrRejected = errors.New("order rejected")

// Broker matches pending orders against new bars and updates the portfolio.
type Broker struct {
	cfg       Config
	feed      marketdata.Feed
	portfolio *portfolio.Portfolio

	pending    map[string]order.Order
	orderSeq   int
	fills      []order.Fill
	rejections []order.Rejection
	now        time.Time
}

// New constructs a Broker.
func New(cfg Config, f marketdata.Feed, p *portfolio.Portfolio) *Broker {
	return &Broker{
		cfg:       cfg,
		feed:      f,
		portfolio: p,
		pending:   make(map[string]order.Order),
	}
}

func (b *Broker) nextID() string {
	b.orderSeq++
	return idFor(b.orderSeq)
}

// SetSeq lets deterministic ID generation be seeded by the engine.
func (b *Broker) SetSeq(n int) { b.orderSeq = n }

// SetNow bounds cash-check reference prices to information available at t;
// using a future close would leak look-ahead information into validation.
func (b *Broker) SetNow(t time.Time) { b.now = t }

func idFor(n int) string {
	return "ord-" + strconv.Itoa(n)
}

// knownSymbol reports whether the feed ever lists this symbol.
func (b *Broker) knownSymbol(symbol string) bool {
	for _, s := range b.feed.Symbols() {
		if s == symbol {
			return true
		}
	}
	return false
}

// validate checks an order without booking it.
func (b *Broker) validate(o order.Order) (order.RejectReason, string) {
	if o.Qty <= 0 {
		return order.RejectInvalidQty, "qty must be positive"
	}
	if !b.knownSymbol(o.Symbol) {
		return order.RejectUnknownSymbol, "symbol not in feed: " + o.Symbol
	}
	if o.Type == order.Limit && o.LimitPrice <= 0 {
		return order.RejectInvalidPrice, "limit price must be positive"
	}

	pos := b.portfolio.Position(o.Symbol)
	if o.Side == order.Sell {
		if pos.Qty < 0 {
			// Already short: adding requires short permission and quota.
			if !o.ShortSale {
				return order.RejectNoPosition, "selling beyond holding requires ShortSale"
			}
			if r := b.checkShortQuota(o, pos); r != order.RejectNone {
				return r, "short quota exceeded"
			}
		} else if o.Qty > pos.Qty {
			over := o.Qty - pos.Qty
			if !o.ShortSale {
				return order.RejectNoPosition, "sell qty exceeds long position"
			}
			if !b.cfg.AllowShort {
				return order.RejectShortNotAllowed, "short selling disabled"
			}
			if r := b.checkShortQuota(order.Order{
				Symbol: o.Symbol, Qty: over,
			}, pos); r != order.RejectNone {
				return r, "short quota exceeded"
			}
		}
	}

	// Cash sufficiency for buys: worst-case outlay at a reference price.
	if o.Side == order.Buy {
		ref := b.referencePrice(o)
		needed := b.buyCashNeeded(o, ref)
		if needed > b.portfolio.Cash() {
			return order.RejectInsufficientCash, "cash short"
		}
	}
	return order.RejectNone, ""
}

func (b *Broker) checkShortQuota(o order.Order, pos portfolio.Position) order.RejectReason {
	if !b.cfg.AllowShort {
		return order.RejectShortNotAllowed
	}
	if b.cfg.MaxShortQty <= 0 {
		return order.RejectNone
	}
	projected := -pos.Qty + o.Qty
	if projected > b.cfg.MaxShortQty {
		return order.RejectShortQuota
	}
	return order.RejectNone
}

// referencePrice estimates cost for cash checks: last tradable close at or
// before now, else the limit price. It never reads a future bar.
func (b *Broker) referencePrice(o order.Order) float64 {
	tl := b.feed.Timeline()
	for i := len(tl) - 1; i >= 0; i-- {
		if tl[i].After(b.now) {
			continue
		}
		if bar, ok := b.feed.BarAt(o.Symbol, tl[i]); ok && bar.Tradable() && bar.Close > 0 {
			return bar.Close
		}
	}
	if o.LimitPrice > 0 {
		return o.LimitPrice
	}
	return 0
}

func (b *Broker) buyCashNeeded(o order.Order, ref float64) float64 {
	raw := ref
	if o.Type == order.Limit && o.LimitPrice > 0 {
		// Worst case is the limit (no slippage above limit needed to fill).
		raw = o.LimitPrice
	}
	if raw <= 0 {
		return 0
	}
	px := raw * (1 + b.cfg.HalfSpread + b.cfg.SlippageRate)
	notional := px * o.Qty
	comm := b.cfg.CommissionRate*notional + b.cfg.MinCommission
	return notional + comm + 1e-9
}

// Submit validates and queues an order. It never fills during the current bar:
// matching happens on the next ProcessBar call.
func (b *Broker) Submit(o order.Order) (order.RejectReason, error) {
	if reason, detail := b.validate(o); reason != order.RejectNone {
		b.rejections = append(b.rejections, order.Rejection{
			Order: o, Reason: reason, Detail: detail, Time: o.Submitted,
		})
		return reason, ErrRejected
	}
	if o.ID == "" {
		o.ID = b.nextID()
	}
	if _, exists := b.pending[o.ID]; exists {
		b.rejections = append(b.rejections, order.Rejection{
			Order: o, Reason: order.RejectInvalidQty, Detail: "duplicate order id", Time: o.Submitted,
		})
		return order.RejectInvalidQty, ErrRejected
	}
	b.pending[o.ID] = o
	return order.RejectNone, nil
}

// Cancel removes a pending order; false if it is not working.
func (b *Broker) Cancel(orderID string) bool {
	if _, ok := b.pending[orderID]; ok {
		delete(b.pending, orderID)
		return true
	}
	return false
}

// PendingOrders returns working orders in stable ID order.
func (b *Broker) PendingOrders() []order.Order {
	out := make([]order.Order, 0, len(b.pending))
	for _, o := range b.pending {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Fills returns every fill produced so far.
func (b *Broker) Fills() []order.Fill { return append([]order.Fill(nil), b.fills...) }

// Rejections returns every rejection so far.
func (b *Broker) Rejections() []order.Rejection {
	return append([]order.Rejection(nil), b.rejections...)
}

// ProcessBar evaluates all pending orders against the bar that just closed.
// Orders submitted in earlier bars only; the engine enforces submission lag.
// skips lists bars that are not tradable this timestamp; no orders match then.
func (b *Broker) ProcessBar(t time.Time, skips []marketdata.SkipEvent) ([]order.Fill, []order.Rejection) {
	var fills []order.Fill
	var rejects []order.Rejection

	// Build the tradable bars at t, keyed by symbol.
	bars := map[string]marketdata.Bar{}
	for _, s := range b.feed.Symbols() {
		bar, ok := b.feed.BarAt(s, t)
		if !ok {
			continue
		}
		bars[s] = bar
	}
	skipSyms := map[string]marketdata.SkipReason{}
	for _, sk := range skips {
		skipSyms[sk.Symbol] = sk.Reason
	}

	// Stable processing order by order ID.
	orders := b.PendingOrders()
	for _, o := range orders {
		// Expiry checks use the bar start time t.
		if expired(o, t) {
			delete(b.pending, o.ID)
			continue
		}

		bar, hasBar := bars[o.Symbol]
		if !hasBar {
			// Missing data: no trade. A DAY order used its single active bar;
			// longer-lived orders remain working. GTD expiry is still honored.
			if o.TIF == order.DAY || expired(o, t) {
				delete(b.pending, o.ID)
			}
			continue
		}
		if reason := skipSyms[o.Symbol]; reason != marketdata.SkipNone {
			// Halted blocks both sides. A one-sided price limit blocks only the
			// direction that cannot transact: a limit-up bar has no sellers at a
			// lower price (buys blocked), while sells may still exit; a
			// limit-down bar blocks sells but lets buys in.
			blocked := reason == marketdata.SkipHalted ||
				(reason == marketdata.SkipLimitUp && o.Side == order.Buy) ||
				(reason == marketdata.SkipLimitDown && o.Side == order.Sell)
			if blocked {
				if o.TIF == order.DAY {
					delete(b.pending, o.ID)
				}
				continue
			}
		}

		var raw float64
		var matched bool
		switch o.Type {
		case order.Market:
			raw, matched = bar.Open, true
		case order.Limit:
			raw, matched = limitMatch(o, bar)
		}
		if !matched {
			if o.TIF == order.DAY {
				delete(b.pending, o.ID)
			}
			continue
		}

		// Re-check buying power immediately before filling (earlier fills count).
		if o.Side == order.Buy {
			px := b.buyFillPrice(raw)
			notional := px * o.Qty
			comm := b.commission(notional)
			if notional+comm > b.portfolio.Cash()+1e-9 {
				rejects = append(rejects, order.Rejection{
					Order: o, Reason: order.RejectInsufficientCash,
					Detail: "cash short at fill time", Time: t,
				})
				delete(b.pending, o.ID)
				continue
			}
		}

		fill := b.makeFill(o, raw, t)
		b.portfolio.Apply(fill)
		b.fills = append(b.fills, fill)
		fills = append(fills, fill)
		delete(b.pending, o.ID)
	}
	return fills, rejects
}

// limitMatch returns the raw execution price before costs if the limit is met.
func limitMatch(o order.Order, bar marketdata.Bar) (float64, bool) {
	switch o.Side {
	case order.Buy:
		// Fills when the market is offered at or below the limit.
		if bar.Low <= o.LimitPrice {
			px := bar.Open
			if px > o.LimitPrice {
				px = o.LimitPrice
			}
			return px, true
		}
	case order.Sell:
		// Fills when the market bids at or above the limit.
		if bar.High >= o.LimitPrice {
			px := bar.Open
			if px < o.LimitPrice {
				px = o.LimitPrice
			}
			return px, true
		}
	}
	return 0, false
}

func (b *Broker) buyFillPrice(raw float64) float64 {
	return raw * (1 + b.cfg.HalfSpread + b.cfg.SlippageRate)
}

func (b *Broker) sellFillPrice(raw float64) float64 {
	return raw * (1 - b.cfg.HalfSpread - b.cfg.SlippageRate)
}

func (b *Broker) commission(notional float64) float64 {
	c := b.cfg.CommissionRate * notional
	if c < b.cfg.MinCommission {
		c = b.cfg.MinCommission
	}
	return c
}

func (b *Broker) makeFill(o order.Order, raw float64, t time.Time) order.Fill {
	var px float64
	if o.Side == order.Buy {
		px = b.buyFillPrice(raw)
	} else {
		px = b.sellFillPrice(raw)
	}
	notional := px * o.Qty
	return order.Fill{
		OrderID:    o.ID,
		Symbol:     o.Symbol,
		Side:       o.Side,
		Qty:        o.Qty,
		Price:      px,
		Commission: b.commission(notional),
		Time:       t,
	}
}

// expired reports whether the order is no longer allowed to fill at time t.
func expired(o order.Order, t time.Time) bool {
	switch o.TIF {
	case order.GTD:
		return !o.Expiry.IsZero() && t.After(o.Expiry)
	default:
		return false
	}
}
