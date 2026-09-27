package portfolio

import (
	"time"

	"github.com/guoyyit/quant-backtest-go/pkg/order"
)

// Position is the signed holding of one symbol (negative when short).
type Position struct {
	Symbol      string
	Qty         float64
	AvgPrice    float64
	RealizedPnL float64
}

// Snapshot captures portfolio state at a point in time.
type Snapshot struct {
	Time          time.Time
	Cash          float64
	Equity        float64
	PositionValue float64
	UnrealizedPnL float64
	RealizedPnL   float64
	Positions     map[string]Position
}

// Portfolio tracks cash, positions and realized/unrealized PnL.
type Portfolio struct {
	cash         float64
	positions    map[string]*Position
	realizedPnL  float64
	lastSnapshot Snapshot
}

// New constructs a Portfolio with starting cash.
func New(startingCash float64) *Portfolio {
	return &Portfolio{
		cash:      startingCash,
		positions: make(map[string]*Position),
	}
}

// Cash returns current cash.
func (p *Portfolio) Cash() float64 { return p.cash }

// Position returns a copy of the position for a symbol.
func (p *Portfolio) Position(symbol string) Position {
	if pos, ok := p.positions[symbol]; ok {
		return *pos
	}
	return Position{Symbol: symbol}
}

// Positions returns copies of all positions, including flat ones omitted.
func (p *Portfolio) Positions() map[string]Position {
	out := make(map[string]Position, len(p.positions))
	for s, pos := range p.positions {
		out[s] = *pos
	}
	return out
}

// RealizedPnL returns cumulative realized profit and loss (commissions included).
func (p *Portfolio) RealizedPnL() float64 { return p.realizedPnL }

// applyBuy increases a long (or covers a short).
func (p *Portfolio) applyBuy(f order.Fill) {
	cost := f.Qty * f.Price
	// Commission always reduces cash and counts as a realized loss.
	p.cash -= cost + f.Commission
	p.realizedPnL -= f.Commission

	pos := p.getOrCreate(f.Symbol)
	if pos.Qty >= 0 {
		// Increase long: weighted average cost ignores commission.
		newQty := pos.Qty + f.Qty
		pos.AvgPrice = (pos.AvgPrice*pos.Qty + f.Price*f.Qty) / newQty
		pos.Qty = newQty
		return
	}
	// Covering a short.
	qtyShort := -pos.Qty
	if f.Qty <= qtyShort {
		// Profit = (old short price - cover price) * qty.
		pnl := (pos.AvgPrice - f.Price) * f.Qty
		pos.RealizedPnL += pnl
		p.realizedPnL += pnl
		pos.Qty += f.Qty // moves toward zero
		if pos.Qty == 0 {
			pos.AvgPrice = 0
		}
		return
	}
	// Cover whole short and go long with the remainder.
	pnl := (pos.AvgPrice - f.Price) * qtyShort
	pos.RealizedPnL += pnl
	p.realizedPnL += pnl
	remain := f.Qty - qtyShort
	pos.Qty = remain
	pos.AvgPrice = f.Price
}

// applySell decreases a long (or opens/adds to a short).
func (p *Portfolio) applySell(f order.Fill) {
	proceeds := f.Qty * f.Price
	p.cash += proceeds - f.Commission
	p.realizedPnL -= f.Commission

	pos := p.getOrCreate(f.Symbol)
	if pos.Qty <= 0 {
		// Increase short: weighted average entry price.
		newQty := pos.Qty - f.Qty // both negative magnitude grows
		pos.AvgPrice = (pos.AvgPrice*(-pos.Qty) + f.Price*f.Qty) / (-newQty)
		pos.Qty = newQty
		return
	}
	// Closing a long.
	if f.Qty <= pos.Qty {
		pnl := (f.Price - pos.AvgPrice) * f.Qty
		pos.RealizedPnL += pnl
		p.realizedPnL += pnl
		pos.Qty -= f.Qty
		if pos.Qty == 0 {
			pos.AvgPrice = 0
		}
		return
	}
	// Close whole long and go short with the remainder.
	qtyLong := pos.Qty
	pnl := (f.Price - pos.AvgPrice) * qtyLong
	pos.RealizedPnL += pnl
	p.realizedPnL += pnl
	remain := f.Qty - qtyLong
	pos.Qty = -remain
	pos.AvgPrice = f.Price
}

// Apply books one fill, updating cash, holdings and realized PnL.
func (p *Portfolio) Apply(f order.Fill) {
	switch f.Side {
	case order.Buy:
		p.applyBuy(f)
	case order.Sell:
		p.applySell(f)
	}
}

// MarkToMarket computes unrealized PnL and equity at the given close prices.
// Symbols without a price are valued at their average cost (unrealized = 0).
func (p *Portfolio) MarkToMarket(t time.Time, prices map[string]float64) Snapshot {
	var posValue, unrealized float64
	out := make(map[string]Position, len(p.positions))
	for s, pos := range p.positions {
		qty := pos.Qty
		var value, upnl float64
		if price, ok := prices[s]; ok && qty != 0 {
			value = qty * price
			upnl = (price - pos.AvgPrice) * qty
		}
		posValue += value
		unrealized += upnl
		cp := *pos
		out[s] = cp
	}
	snap := Snapshot{
		Time:          t,
		Cash:          p.cash,
		Equity:        p.cash + posValue,
		PositionValue: posValue,
		UnrealizedPnL: unrealized,
		RealizedPnL:   p.realizedPnL,
		Positions:     out,
	}
	p.lastSnapshot = snap
	return snap
}

func (p *Portfolio) getOrCreate(symbol string) *Position {
	pos, ok := p.positions[symbol]
	if !ok {
		pos = &Position{Symbol: symbol}
		p.positions[symbol] = pos
	}
	return pos
}
