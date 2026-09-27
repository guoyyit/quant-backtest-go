package backtest

import (
	"math"
	"testing"
	"time"

	"github.com/guoyyit/quant-backtest-go/market"
)

var t0 = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

func bar(t time.Time, o, h, l, c float64) market.Bar {
	return market.Bar{Symbol: "AAA", Time: t, Open: o, High: h, Low: l, Close: c, Volume: 1000}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// 市价买入：成交价计入半价差与滑点，手续费按比例收取。
func TestMarketBuyPriceIncludesSpreadSlippageFee(t *testing.T) {
	pf := NewPortfolio(100000)
	b := NewBroker(BrokerConfig{
		Commission: RateCommission{Rate: 0.002},
		Spread:     0.01, // 1%
		Slippage:   0.001,
	}, pf)

	b.SetLastPrice("AAA", 10)
	o := b.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderMarket, Quantity: 100, Submitted: t0})
	if o.Status != StatusWorking {
		t.Fatalf("order not working: %s %s", o.Status, o.Reject)
	}

	t1 := t0.Add(24 * time.Hour)
	b.ProcessBar(bar(t1, 10, 10.5, 9.5, 10.2))

	fills := b.Fills()
	if len(fills) != 1 {
		t.Fatalf("want 1 fill, got %d", len(fills))
	}
	f := fills[0]
	// 10 * (1 + 0.005) * (1 + 0.001)
	wantPx := 10.0 * 1.005 * 1.001
	if !approx(f.Price, wantPx) {
		t.Errorf("price = %.6f, want %.6f", f.Price, wantPx)
	}
	wantFee := f.Price * 100 * 0.002
	if !approx(f.Commission, wantFee) {
		t.Errorf("fee = %.6f, want %.6f", f.Commission, wantFee)
	}
	wantCash := 100000 - f.Price*100 - wantFee
	if !approx(pf.Cash, wantCash) {
		t.Errorf("cash = %.6f, want %.6f", pf.Cash, wantCash)
	}
	if pos := pf.Position("AAA"); !approx(pos.Quantity, 100) {
		t.Errorf("qty = %v, want 100", pos.Quantity)
	}
}

// 限价买入：价格条件满足前保持挂单；触及限价后成交且不劣于限价。
func TestLimitBuyStaysWorkingThenFills(t *testing.T) {
	pf := NewPortfolio(100000)
	b := NewBroker(BrokerConfig{Commission: FlatCommission{Fee: 1}}, pf)

	b.SetLastPrice("AAA", 10)
	o := b.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderLimit, Quantity: 50,
		LimitPx: 9.5, TIF: TIFGTC, Submitted: t0})

	t1 := t0.Add(24 * time.Hour)
	t2 := t1.Add(24 * time.Hour)
	b.ProcessBar(bar(t1, 10.2, 10.4, 10.1, 10.3)) // Low=10.1 > 9.5，不成交
	if o.Status != StatusWorking {
		t.Fatalf("limit order should stay working, got %s", o.Status)
	}
	b.ProcessBar(bar(t2, 9.8, 9.9, 9.4, 9.6)) // Low=9.4 <= 9.5，成交
	if o.Status != StatusFilled {
		t.Fatalf("want filled, got %s", o.Status)
	}
	if px := b.Fills()[0].Price; px > 9.5+1e-12 {
		t.Errorf("limit buy filled at %.6f > limit 9.5", px)
	}
}

// 限价单撤销：撤销后不再成交且释放预留现金。
func TestLimitCancel(t *testing.T) {
	pf := NewPortfolio(100000)
	b := NewBroker(BrokerConfig{}, pf)
	b.SetLastPrice("AAA", 10)
	o := b.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderLimit, Quantity: 10,
		LimitPx: 9, TIF: TIFGTC, Submitted: t0})
	if !b.Cancel(o.ID) {
		t.Fatal("cancel failed")
	}
	if o.Status != StatusCanceled {
		t.Fatalf("status = %s", o.Status)
	}
	if b.cashReserved != 0 {
		t.Errorf("reserved cash = %v, want 0", b.cashReserved)
	}
	b.ProcessBar(bar(t0.Add(24*time.Hour), 8, 9, 8, 8.5))
	if len(b.Fills()) != 0 {
		t.Fatal("canceled order must not fill")
	}
}

// FOK 限价单在当根 Bar 条件不满足时立即过期。
func TestLimitFOKExpiresSameBar(t *testing.T) {
	pf := NewPortfolio(100000)
	b := NewBroker(BrokerConfig{}, pf)
	b.SetLastPrice("AAA", 10)
	o := b.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderLimit, Quantity: 10,
		LimitPx: 8, TIF: TIFFOK, Submitted: t0})
	b.ProcessBar(bar(t0.Add(24*time.Hour), 10, 10, 10, 10))
	if o.Status != StatusExpired {
		t.Fatalf("status = %s, want expired", o.Status)
	}
}

// 资金不足：按最坏成本估算的买单必须在提交时被拒绝。
func TestInsufficientCashRejected(t *testing.T) {
	pf := NewPortfolio(100)
	b := NewBroker(BrokerConfig{Commission: FlatCommission{Fee: 5}}, pf)
	b.SetLastPrice("AAA", 10)
	o := b.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderMarket, Quantity: 100, Submitted: t0})
	if o.Status != StatusRejected || o.Reject != RejectInsufficientCash {
		t.Fatalf("want rejected(insufficient_cash), got %s %s", o.Status, o.Reject)
	}
}

// 无持仓卖出被拒绝；不允许卖空时持仓不得为负。
func TestNoShortSellRejectedAndNoNegativePosition(t *testing.T) {
	pf := NewPortfolio(100000)
	b := NewBroker(BrokerConfig{}, pf)
	b.SetLastPrice("AAA", 10)
	o := b.Submit(&Order{Symbol: "AAA", Side: Sell, Type: OrderMarket, Quantity: 10, Submitted: t0})
	if o.Status != StatusRejected || o.Reject != RejectNoPosition {
		t.Fatalf("want rejected(no_position), got %s %s", o.Status, o.Reject)
	}

	// 持仓 100，卖 150：不允许卖空 -> 拒绝
	pf.ApplyFill(&Fill{Symbol: "AAA", Side: Buy, Quantity: 100, Price: 10})
	o2 := b.Submit(&Order{Symbol: "AAA", Side: Sell, Type: OrderMarket, Quantity: 150, Submitted: t0})
	if o2.Status != StatusRejected || o2.Reject != RejectBelowZero {
		t.Fatalf("want rejected(position_would_be_negative), got %s %s", o2.Status, o2.Reject)
	}
}

// 显式启用卖空并受额度约束：超额卖单被拒；额度内可成交，持仓为负。
func TestShortWithQuota(t *testing.T) {
	pf := NewPortfolio(100000)
	b := NewBroker(BrokerConfig{
		AllowShort:      true,
		DefaultShortQty: 30,
	}, pf)
	b.SetLastPrice("AAA", 10)

	tooMuch := b.Submit(&Order{Symbol: "AAA", Side: Sell, Type: OrderMarket, Quantity: 31, Submitted: t0})
	if tooMuch.Status != StatusRejected || tooMuch.Reject != RejectExceedShortLimit {
		t.Fatalf("want rejected(exceeds_short_limit), got %s %s", tooMuch.Status, tooMuch.Reject)
	}

	ok := b.Submit(&Order{Symbol: "AAA", Side: Sell, Type: OrderMarket, Quantity: 30, Submitted: t0})
	b.ProcessBar(bar(t0.Add(24*time.Hour), 10, 10.2, 9.8, 10))
	if ok.Status != StatusFilled {
		t.Fatalf("want filled, got %s", ok.Status)
	}
	if q := pf.Position("AAA").Quantity; q != -30 {
		t.Errorf("qty = %v, want -30", q)
	}
}

// 已实现盈亏：先买后卖，平仓盈亏 = (卖价-买均价)*数量 - 费用。
func TestRealizedPLOnRoundTrip(t *testing.T) {
	pf := NewPortfolio(100000)
	b := NewBroker(BrokerConfig{Commission: FlatCommission{Fee: 0}}, pf)
	b.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderMarket, Quantity: 100, Submitted: t0})
	b.ProcessBar(bar(t0.Add(24*time.Hour), 10, 10, 10, 10))
	b.Submit(&Order{Symbol: "AAA", Side: Sell, Type: OrderMarket, Quantity: 100, Submitted: t0.Add(24 * time.Hour)})
	b.ProcessBar(bar(t0.Add(48*time.Hour), 11, 11, 11, 11))
	if len(b.Fills()) != 2 {
		t.Fatalf("want 2 fills, got %d", len(b.Fills()))
	}
	if pl := b.Fills()[1].RealizedPL; !approx(pl, 100) {
		t.Errorf("realized PL = %.4f, want 100", pl)
	}
}

// 次日大幅跳空高开，超过提交时的最坏成本预留：
// 成交会使现金为负，必须在成交前一刻拒绝（现金安全网）。
func TestGapUpBeyondReserveRejectedAtExecute(t *testing.T) {
	pf := NewPortfolio(1000)
	br := NewBroker(BrokerConfig{MarketCashBuffer: 0.01}, pf)
	br.SetLastPrice("AAA", 10)
	// 预留成本约 100*10*1.01 = 1010 > 现金 1000，因此提交即拒。
	o := br.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderMarket, Quantity: 100, Submitted: t0})
	if o.Status == StatusWorking {
		t.Fatal("order should be rejected at submit for insufficient reserve")
	}

	// 现金恰好够按缓冲买入的数量，但次日跳空 +20%。
	pf2 := NewPortfolio(1010)
	br2 := NewBroker(BrokerConfig{MarketCashBuffer: 0.01}, pf2)
	br2.SetLastPrice("AAA", 10)
	o2 := br2.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderMarket, Quantity: 100, Submitted: t0})
	if o2.Status != StatusWorking {
		t.Fatalf("submit should pass with reserve, got %s", o2.Status)
	}
	gap := bar(t0.Add(24*time.Hour), 12, 12.2, 11.8, 12)
	br2.ProcessBar(gap)
	if o2.Status != StatusRejected || o2.Reject != RejectInsufficientCash {
		t.Fatalf("gap-up must be rejected at execute, got %s %s", o2.Status, o2.Reject)
	}
	if len(br2.Fills()) != 0 {
		t.Fatal("rejected gap-up must not produce a fill")
	}
	if pf2.Cash != 1010 {
		t.Errorf("cash must remain untouched, got %.2f", pf2.Cash)
	}
}

// MaxBuyQuantity 与实际最坏成本一致：据此下单提交必通过。
func TestMaxBuyQuantityConsistent(t *testing.T) {
	pf := NewPortfolio(10000)
	br := NewBroker(BrokerConfig{
		Commission:       RateCommission{Rate: 0.002},
		Spread:           0.01,
		Slippage:         0.001,
		MarketCashBuffer: 0.02,
	}, pf)
	br.SetLastPrice("AAA", 20)
	q := br.MaxBuyQuantity("AAA")
	if q <= 0 {
		t.Fatal("max qty should be positive")
	}
	o := br.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderMarket, Quantity: q, Submitted: t0})
	if o.Status != StatusWorking {
		t.Fatalf("order sized by MaxBuyQuantity must be accepted, got %s %s", o.Status, o.Reject)
	}
}

// Bar 自带 Bid/Ask 时，成交直接以 Bid/Ask 为基准（不使用 Spread 合成），仅加滑点。
func TestFillUsesQuotedBidAsk(t *testing.T) {
	pf := NewPortfolio(100000)
	br := NewBroker(BrokerConfig{Slippage: 0.001, Spread: 0.02}, pf)
	br.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderMarket, Quantity: 10, Submitted: t0})
	qb := bar(t0.Add(24*time.Hour), 10, 10.2, 9.8, 10)
	qb.Ask = 10.05
	br.ProcessBar(qb)
	if got := br.Fills()[0].Price; !approx(got, 10.05*1.001) {
		t.Errorf("buy px = %.5f, want ask+slippage %.5f", got, 10.05*1.001)
	}

	pf2 := NewPortfolio(100000)
	br2 := NewBroker(BrokerConfig{Slippage: 0.001, Spread: 0.02}, pf2)
	pf2.ApplyFill(&Fill{Symbol: "AAA", Side: Buy, Quantity: 10, Price: 10})
	br2.Submit(&Order{Symbol: "AAA", Side: Sell, Type: OrderMarket, Quantity: 10, Submitted: t0})
	qb2 := bar(t0.Add(24*time.Hour), 10, 10.2, 9.8, 10)
	qb2.Bid = 9.95
	br2.ProcessBar(qb2)
	if got := br2.Fills()[0].Price; !approx(got, 9.95*0.999) {
		t.Errorf("sell px = %.5f, want bid-slippage %.5f", got, 9.95*0.999)
	}
}

// GTC 限价单超过 ExpiresAt 后过期，不再成交。
func TestLimitExpiryTime(t *testing.T) {
	pf := NewPortfolio(100000)
	br := NewBroker(BrokerConfig{}, pf)
	br.SetLastPrice("AAA", 10)
	exp := t0.Add(48 * time.Hour)
	o := br.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderLimit, Quantity: 10,
		LimitPx: 9, TIF: TIFGTC, ExpiresAt: exp, Submitted: t0})

	// t1（未过期）价格未触及 -> 继续挂单
	br.ProcessBar(bar(t0.Add(24*time.Hour), 10, 10, 10, 10))
	if o.Status != StatusWorking {
		t.Fatalf("want working at t1, got %s", o.Status)
	}
	// t3（已过 ExpiresAt）即便价格触及也不成交，而是过期
	br.ProcessBar(bar(t0.Add(72*time.Hour), 8, 9, 8, 8.5))
	if o.Status != StatusExpired {
		t.Fatalf("want expired after ExpiresAt, got %s", o.Status)
	}
	if len(br.Fills()) != 0 {
		t.Fatal("expired order must not fill")
	}
}
