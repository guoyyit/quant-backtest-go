package broker_test

import (
	"testing"

	"github.com/guoyyit/quant-backtest-go/pkg/broker"
	"github.com/guoyyit/quant-backtest-go/pkg/order"
	"github.com/guoyyit/quant-backtest-go/pkg/portfolio"
)

func TestInsufficientCashRejected(t *testing.T) {
	s := "CASH"
	f := feedOf(
		bar(s, 0, 100, 101, 99, 100),
		bar(s, 1, 100, 101, 99, 100),
	)
	p := portfolio.New(500) // can't afford 100 shares @ ~100
	bk := broker.New(broker.Config{}, f, p)
	bk.SetNow(f.Timeline()[0])
	reason, err := bk.Submit(order.Order{
		ID: "big", Symbol: s, Side: order.Buy, Type: order.Market,
		Qty: 100, Submitted: f.Timeline()[0],
	})
	if err == nil {
		t.Fatal("expected rejection")
	}
	if reason != order.RejectInsufficientCash {
		t.Fatalf("reason=%v want insufficient_cash", reason)
	}
	fills, _ := bk.ProcessBar(f.Timeline()[1], nil)
	if len(fills) != 0 {
		t.Fatal("rejected order must not fill")
	}
	if p.Cash() != 500 {
		t.Fatalf("cash changed: %v", p.Cash())
	}
}

func TestSellWithoutPositionRejected(t *testing.T) {
	s := "NO"
	f := feedOf(
		bar(s, 0, 100, 101, 99, 100),
		bar(s, 1, 100, 101, 99, 100),
	)
	p := portfolio.New(1e6)
	bk := broker.New(broker.Config{}, f, p)
	bk.SetNow(f.Timeline()[0])
	reason, err := bk.Submit(order.Order{
		ID: "naked", Symbol: s, Side: order.Sell, Type: order.Market,
		Qty: 10, Submitted: f.Timeline()[0],
	})
	if err == nil || reason != order.RejectNoPosition {
		t.Fatalf("want no_position rejection, got reason=%v err=%v", reason, err)
	}
}

func TestShortBlockedWhenDisabled(t *testing.T) {
	s := "SH"
	f := feedOf(
		bar(s, 0, 100, 101, 99, 100),
		bar(s, 1, 100, 101, 99, 100),
	)
	p := portfolio.New(1e6)
	bk := broker.New(broker.Config{AllowShort: false}, f, p)
	bk.SetNow(f.Timeline()[0])
	reason, _ := bk.Submit(order.Order{
		ID: "sh", Symbol: s, Side: order.Sell, Type: order.Market,
		Qty: 10, ShortSale: true, Submitted: f.Timeline()[0],
	})
	if reason != order.RejectShortNotAllowed {
		t.Fatalf("want short_not_allowed, got %v", reason)
	}
}

func TestShortAllowedWithinQuotaAndNeverNegativeLong(t *testing.T) {
	s := "SH"
	f := feedOf(
		bar(s, 0, 100, 101, 99, 100),
		bar(s, 1, 100, 101, 99, 100),
	)
	p := portfolio.New(1e6)
	bk := broker.New(broker.Config{AllowShort: true, MaxShortQty: 5}, f, p)
	bk.SetNow(f.Timeline()[0])
	// Qty 10 exceeds quota 5.
	if reason, _ := bk.Submit(order.Order{
		ID: "over", Symbol: s, Side: order.Sell, Type: order.Market,
		Qty: 10, ShortSale: true, Submitted: f.Timeline()[0],
	}); reason != order.RejectShortQuota {
		t.Fatalf("want short_quota, got %v", reason)
	}
	// Qty 5 within quota -> fills next bar, position -5 (a controlled short,
	// never a negative result for a long account).
	if _, err := bk.Submit(order.Order{
		ID: "ok", Symbol: s, Side: order.Sell, Type: order.Market,
		Qty: 5, ShortSale: true, Submitted: f.Timeline()[0],
	}); err != nil {
		t.Fatal(err)
	}
	fills, _ := bk.ProcessBar(f.Timeline()[1], nil)
	if len(fills) != 1 {
		t.Fatalf("want 1 short fill, got %d", len(fills))
	}
	pos := p.Position(s)
	if pos.Qty != -5 {
		t.Fatalf("position=%v want -5", pos.Qty)
	}
}

func TestInvalidQtyAndPrice(t *testing.T) {
	s := "V"
	f := feedOf(bar(s, 0, 100, 101, 99, 100))
	p := portfolio.New(1e6)
	bk := broker.New(broker.Config{}, f, p)
	bk.SetNow(f.Timeline()[0])
	if reason, _ := bk.Submit(order.Order{
		Symbol: s, Side: order.Buy, Type: order.Market, Qty: 0,
	}); reason != order.RejectInvalidQty {
		t.Fatalf("want invalid_qty, got %v", reason)
	}
	if reason, _ := bk.Submit(order.Order{
		Symbol: s, Side: order.Buy, Type: order.Limit, Qty: 1, LimitPrice: 0,
	}); reason != order.RejectInvalidPrice {
		t.Fatalf("want invalid_price, got %v", reason)
	}
}

// Two market buys placed on the same earlier bar: once the first fills and
// consumes the cash, the second must be rejected at fill time. Cash can never
// go negative.
func TestSecondBuyRejectedWhenCashDepleted(t *testing.T) {
	s := "C2"
	f := feedOf(
		bar(s, 0, 100, 101, 99, 100),
		bar(s, 1, 100, 101, 99, 100),
	)
	p := portfolio.New(15000)
	bk := broker.New(broker.Config{}, f, p)
	bk.SetNow(f.Timeline()[0])
	// Each buy costs 100*100 = 10000 at zero cost.
	bk.Submit(order.Order{ID: "a", Symbol: s, Side: order.Buy, Type: order.Market, Qty: 100, Submitted: f.Timeline()[0]})
	bk.Submit(order.Order{ID: "b", Symbol: s, Side: order.Buy, Type: order.Market, Qty: 100, Submitted: f.Timeline()[0]})
	fills, rejects := bk.ProcessBar(f.Timeline()[1], nil)
	if len(fills) != 1 {
		t.Fatalf("want exactly 1 fill, got %d", len(fills))
	}
	if len(rejects) != 1 || rejects[0].Reason != order.RejectInsufficientCash {
		t.Fatalf("want 1 insufficient_cash rejection, got %+v", rejects)
	}
	if p.Cash() < 0 {
		t.Fatalf("negative cash: %v", p.Cash())
	}
	if !closeFloat(p.Cash(), 5000, 1e-9) {
		t.Fatalf("cash=%v want 5000", p.Cash())
	}
}

func closeFloat(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}
