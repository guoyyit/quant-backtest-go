package broker_test

import (
	"testing"
	"time"

	"github.com/guoyyit/quant-backtest-go/pkg/broker"
	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/order"
	"github.com/guoyyit/quant-backtest-go/pkg/portfolio"
)

var day = 24 * time.Hour

func bar(sym string, i int, o, h, l, c float64) marketdata.Bar {
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	return marketdata.Bar{
		Symbol: sym, StartTime: t0.Add(time.Duration(i) * day), Interval: day,
		Open: o, High: h, Low: l, Close: c, Volume: 1,
	}
}

func feedOf(bars ...marketdata.Bar) marketdata.Feed {
	return marketdata.NewMemoryFeed(day, bars)
}

func TestLimitBuyFillsWhenPriceTouchesLimit(t *testing.T) {
	s := "LMT"
	f := feedOf(
		bar(s, 0, 100, 101, 99, 100),
		bar(s, 1, 100, 100, 94, 95), // low 94 <= limit 95
	)
	p := portfolio.New(1e6)
	bk := broker.New(broker.Config{}, f, p)
	bk.SetNow(f.Timeline()[0])
	_, err := bk.Submit(order.Order{
		ID: "x", Symbol: s, Side: order.Buy, Type: order.Limit,
		Qty: 10, LimitPrice: 95, TIF: order.GTC, Submitted: f.Timeline()[0],
	})
	if err != nil {
		t.Fatal(err)
	}
	fills, _ := bk.ProcessBar(f.Timeline()[1], nil)
	if len(fills) != 1 {
		t.Fatalf("want 1 fill, got %d", len(fills))
	}
	// Raw fill is min(open=100, limit=95)=95; zero cost config keeps price 95.
	if fills[0].Price != 95 {
		t.Fatalf("price=%v want 95", fills[0].Price)
	}
}

func TestLimitBuyStaysPendingWhenNotTouched(t *testing.T) {
	s := "LMT"
	f := feedOf(
		bar(s, 0, 100, 101, 99, 100),
		bar(s, 1, 98, 99, 96, 97), // low 96 > limit 95, no fill
		bar(s, 2, 98, 97, 94, 95), // low 94 <= limit 95 -> fill
	)
	p := portfolio.New(1e6)
	bk := broker.New(broker.Config{}, f, p)
	bk.SetNow(f.Timeline()[0])
	bk.Submit(order.Order{
		ID: "x", Symbol: s, Side: order.Buy, Type: order.Limit,
		Qty: 10, LimitPrice: 95, TIF: order.GTC, Submitted: f.Timeline()[0],
	})
	if fills, _ := bk.ProcessBar(f.Timeline()[1], nil); len(fills) != 0 {
		t.Fatalf("should not fill on bar 1, got %d", len(fills))
	}
	if len(bk.PendingOrders()) != 1 {
		t.Fatal("limit order must remain pending")
	}
	if fills, _ := bk.ProcessBar(f.Timeline()[2], nil); len(fills) != 1 {
		t.Fatalf("should fill on bar 2, got %d", len(fills))
	}
}

func TestCancelRemovesPendingOrder(t *testing.T) {
	s := "LMT"
	f := feedOf(
		bar(s, 0, 100, 101, 99, 100),
		bar(s, 1, 90, 91, 80, 85),
	)
	p := portfolio.New(1e6)
	bk := broker.New(broker.Config{}, f, p)
	bk.SetNow(f.Timeline()[0])
	bk.Submit(order.Order{
		ID: "c1", Symbol: s, Side: order.Buy, Type: order.Limit,
		Qty: 10, LimitPrice: 95, Submitted: f.Timeline()[0],
	})
	if !bk.Cancel("c1") {
		t.Fatal("cancel should succeed")
	}
	if bk.Cancel("c1") {
		t.Fatal("second cancel should fail")
	}
	if fills, _ := bk.ProcessBar(f.Timeline()[1], nil); len(fills) != 0 {
		t.Fatalf("cancelled order must not fill, got %d", len(fills))
	}
}

func TestLimitSellFillsAtOrAboveLimit(t *testing.T) {
	s := "LMT"
	p0 := portfolio.New(1e6)
	// establish a long via a direct portfolio fill so selling is allowed
	p0.Apply(order.Fill{Symbol: s, Side: order.Buy, Qty: 10, Price: 90, Time: f0()})
	f := feedOf(
		bar(s, 0, 90, 91, 89, 90),
		bar(s, 1, 90, 106, 90, 105), // high 106 >= limit 105
	)
	bk := broker.New(broker.Config{}, f, p0)
	bk.SetNow(f.Timeline()[0])
	bk.Submit(order.Order{
		ID: "s1", Symbol: s, Side: order.Sell, Type: order.Limit,
		Qty: 10, LimitPrice: 105, Submitted: f.Timeline()[0],
	})
	fills, _ := bk.ProcessBar(f.Timeline()[1], nil)
	if len(fills) != 1 || fills[0].Price != 105 {
		t.Fatalf("want sell fill at 105, got %+v", fills)
	}
}

func TestGTDExpiry(t *testing.T) {
	s := "LMT"
	f := feedOf(
		bar(s, 0, 100, 101, 99, 100),
		bar(s, 1, 100, 100, 99, 100), // not touched
		bar(s, 2, 100, 100, 90, 92),  // would touch, but expired
	)
	p := portfolio.New(1e6)
	bk := broker.New(broker.Config{}, f, p)
	bk.SetNow(f.Timeline()[0])
	expiry := f.Timeline()[1]
	bk.Submit(order.Order{
		ID: "g", Symbol: s, Side: order.Buy, Type: order.Limit, Qty: 10,
		LimitPrice: 95, TIF: order.GTD, Expiry: expiry, Submitted: f.Timeline()[0],
	})
	bk.ProcessBar(f.Timeline()[1], nil) // t==expiry: still alive but not touched
	if len(bk.PendingOrders()) != 1 {
		t.Fatal("order should be alive at t==expiry")
	}
	fills, _ := bk.ProcessBar(f.Timeline()[2], nil) // t>expiry
	if len(fills) != 0 || len(bk.PendingOrders()) != 0 {
		t.Fatalf("expired order must not remain/fill, fills=%d pending=%d", len(fills), len(bk.PendingOrders()))
	}
}

func TestDAYExpiresAfterOneActiveBar(t *testing.T) {
	s := "LMT"
	f := feedOf(
		bar(s, 0, 100, 101, 99, 100),
		bar(s, 1, 100, 100, 99, 100), // not touched
		bar(s, 2, 100, 100, 90, 92),
	)
	p := portfolio.New(1e6)
	bk := broker.New(broker.Config{}, f, p)
	bk.SetNow(f.Timeline()[0])
	bk.Submit(order.Order{
		ID: "d", Symbol: s, Side: order.Buy, Type: order.Limit, Qty: 10,
		LimitPrice: 95, TIF: order.DAY, Submitted: f.Timeline()[0],
	})
	bk.ProcessBar(f.Timeline()[1], nil)
	if len(bk.PendingOrders()) != 0 {
		t.Fatal("DAY order must expire after its first active bar")
	}
	fills, _ := bk.ProcessBar(f.Timeline()[2], nil)
	if len(fills) != 0 {
		t.Fatal("expired DAY order must never fill")
	}
}

func f0() time.Time { return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) }
