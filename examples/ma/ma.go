// Package ma implements a simple moving-average crossover example strategy.
package ma

import (
	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/order"
	"github.com/guoyyit/quant-backtest-go/pkg/strategy"
)

// Strategy goes long when the fast moving average crosses above the slow one
// and exits (sells) on the opposite cross. All decisions use only closed bars.
type Strategy struct {
	Symbol string
	fast   int
	slow   int
	held   float64
}

// New builds the strategy for one parameter combination.
func New(p strategy.Params) strategy.Strategy {
	return &Strategy{
		Symbol: "AAA",
		fast:   int(p["fast"]),
		slow:   int(p["slow"]),
	}
}

func sma(bars []marketdata.Bar) float64 {
	if len(bars) == 0 {
		return 0
	}
	var s float64
	for _, b := range bars {
		s += b.Close
	}
	return s / float64(len(bars))
}

func (s *Strategy) OnBar(ctx strategy.Context, _ map[string]marketdata.Bar) {
	// Need enough history to compare the fast/slow means on the previous bar
	// versus the current bar: that requires slow+1 closed bars.
	hist := ctx.History(s.Symbol, s.slow+1)
	if len(hist) < s.slow+1 {
		return
	}
	// Indices: last = current bar, last-1 = previous bar.
	prevFast := sma(hist[len(hist)-s.fast-1 : len(hist)-1])
	prevSlow := sma(hist[len(hist)-s.slow-1 : len(hist)-1])
	curFast := sma(hist[len(hist)-s.fast : len(hist)])
	curSlow := sma(hist[len(hist)-s.slow : len(hist)])

	switch {
	case prevFast <= prevSlow && curFast > curSlow && s.held == 0:
		cash := ctx.Cash()
		cur, _ := ctx.CurrentBar(s.Symbol)
		px := cur.Close
		if px <= 0 {
			return
		}
		qty := cash / px * 0.90
		if qty < 1 {
			return
		}
		if reason, err := ctx.Submit(order.Order{
			Symbol: s.Symbol, Side: order.Buy, Type: order.Market, Qty: qty,
		}); err != nil {
			_ = reason
		} else {
			s.held = qty
		}
	case prevFast >= prevSlow && curFast < curSlow && s.held > 0:
		ctx.Submit(order.Order{
			Symbol: s.Symbol, Side: order.Sell, Type: order.Market, Qty: s.held,
		})
		s.held = 0
	}
}
