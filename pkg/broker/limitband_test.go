package broker_test

import (
	"testing"

	"github.com/guoyyit/quant-backtest-go/pkg/broker"
	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/order"
	"github.com/guoyyit/quant-backtest-go/pkg/portfolio"
)

func limitBar(sym string, i int, up, down bool, halted bool) marketdata.Bar {
	b := bar(sym, i, 100, 100, 100, 100)
	b.LimitUp, b.LimitDown, b.Halted = up, down, halted
	return b
}

// limitFeed builds: bar0 normal (order reference), bar1 in the given state.
func limitFeed(sym string, b1 marketdata.Bar) marketdata.Feed {
	return feedOf(bar(sym, 0, 100, 101, 99, 100), b1)
}

func TestLimitUpBlocksBuyAllowsSell(t *testing.T) {
	s := "UP"
	f := limitFeed(s, limitBar(s, 1, true, false, false))
	p := portfolio.New(1e6)
	// Existing long so selling is permitted.
	p.Apply(order.Fill{Symbol: s, Side: order.Buy, Qty: 10, Price: 100, Time: f0()})
	bk := broker.New(broker.Config{}, f, p)
	bk.SetNow(f.Timeline()[0])
	bk.Submit(order.Order{ID: "buy", Symbol: s, Side: order.Buy, Type: order.Market, Qty: 10, Submitted: f.Timeline()[0]})
	bk.Submit(order.Order{ID: "sell", Symbol: s, Side: order.Sell, Type: order.Market, Qty: 10, Submitted: f.Timeline()[0]})
	skips := []marketdata.SkipEvent{{Time: f.Timeline()[1], Symbol: s, Reason: marketdata.SkipLimitUp}}
	fills, _ := bk.ProcessBar(f.Timeline()[1], skips)
	if len(fills) != 1 || fills[0].Side != order.Sell {
		t.Fatalf("want only sell to fill, got %+v", fills)
	}
}

func TestLimitDownBlocksSellAllowsBuy(t *testing.T) {
	s := "DN"
	f := limitFeed(s, limitBar(s, 1, false, true, false))
	p := portfolio.New(1e6)
	bk := broker.New(broker.Config{}, f, p)
	bk.SetNow(f.Timeline()[0])
	bk.Submit(order.Order{ID: "buy", Symbol: s, Side: order.Buy, Type: order.Market, Qty: 10, Submitted: f.Timeline()[0]})
	// Naked sell would be rejected at submit; instead open a long on bar0 then sell.
	p.Apply(order.Fill{Symbol: s, Side: order.Buy, Qty: 10, Price: 100, Time: f0()})
	bk.Submit(order.Order{ID: "sell", Symbol: s, Side: order.Sell, Type: order.Market, Qty: 10, Submitted: f.Timeline()[0]})
	skips := []marketdata.SkipEvent{{Time: f.Timeline()[1], Symbol: s, Reason: marketdata.SkipLimitDown}}
	fills, _ := bk.ProcessBar(f.Timeline()[1], skips)
	if len(fills) != 1 || fills[0].Side != order.Buy {
		t.Fatalf("want only buy to fill, got %+v", fills)
	}
}

func TestHaltedBlocksBothSides(t *testing.T) {
	s := "HL"
	f := limitFeed(s, limitBar(s, 1, false, false, true))
	p := portfolio.New(1e6)
	p.Apply(order.Fill{Symbol: s, Side: order.Buy, Qty: 10, Price: 100, Time: f0()})
	bk := broker.New(broker.Config{}, f, p)
	bk.SetNow(f.Timeline()[0])
	bk.Submit(order.Order{ID: "buy", Symbol: s, Side: order.Buy, Type: order.Market, Qty: 10, Submitted: f.Timeline()[0]})
	bk.Submit(order.Order{ID: "sell", Symbol: s, Side: order.Sell, Type: order.Market, Qty: 10, Submitted: f.Timeline()[0]})
	skips := []marketdata.SkipEvent{{Time: f.Timeline()[1], Symbol: s, Reason: marketdata.SkipHalted}}
	fills, _ := bk.ProcessBar(f.Timeline()[1], skips)
	if len(fills) != 0 {
		t.Fatalf("halted must block both sides, got %+v", fills)
	}
}
