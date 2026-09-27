package backtest

import (
	"testing"
	"time"

	"github.com/guoyyit/quant-backtest-go/market"
)

// recStrategy 在每根 Bar 的 OnBar 中立刻下市价买单，
// 用于检验"信号在 t 产生、成交只能发生在 t 之后"的防前瞻语义。
type recStrategy struct {
	ctxHistLen []int
	seenFuture []bool
	barTimes   []time.Time
}

func (s *recStrategy) OnBar(ctx *Context, bar market.Bar) {
	h := ctx.History(bar.Symbol)
	s.ctxHistLen = append(s.ctxHistLen, len(h))
	// 最后一根 Bar 的时间必须正好等于当前 Bar，绝不能出现更晚的 Bar。
	if h[len(h)-1].Time.After(bar.Time) {
		s.seenFuture = append(s.seenFuture, true)
	}
	s.barTimes = append(s.barTimes, bar.Time)
	ctx.Buy(bar.Symbol, 10)
}
func (s *recStrategy) Finish(ctx *Context) {}

func feed3Bars(symbol string) *market.MemoryFeed {
	days := []struct{ o, c float64 }{{10, 10}, {11, 11}, {12, 12}}
	f := market.NewMemoryFeed()
	for i, d := range days {
		f.Add(market.Bar{Symbol: symbol, Time: t0.AddDate(0, 0, i),
			Open: d.o, High: d.c + 0.5, Low: d.o - 0.5, Close: d.c, Volume: 1})
	}
	return f
}

// 防前瞻：第 1 根 Bar 产生的买单不得在第 1 根成交，最早只能在第 2 根成交。
func TestNoLookaheadOrderFillsNextBar(t *testing.T) {
	feed := feed3Bars("AAA")
	st := &recStrategy{}
	eng := NewEngine(feed, st, Config{InitialCash: 100000})
	res, err := eng.Run()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Fills) == 0 {
		t.Fatal("expected fills on later bars")
	}
	first := res.Fills[0]
	if !first.Time.After(t0) {
		t.Errorf("first fill at %v, must be strictly after signal bar %v", first.Time, t0)
	}
	// 第 3 根 Bar 提交的订单在回测结束时应仍挂单（没有未来 Bar 可成交）。
	var hasWorking bool
	for _, o := range res.Orders {
		if o.Status == StatusWorking {
			hasWorking = true
		}
	}
	if !hasWorking {
		t.Error("order submitted on final bar must remain working, never filled")
	}
	// History 长度随 Bar 递增，且从不包含未来数据。
	for i, n := range st.ctxHistLen {
		if n != i+1 {
			t.Errorf("bar %d: history len = %d, want %d", i, n, i+1)
		}
	}
	for _, f := range st.seenFuture {
		if f {
			t.Error("strategy observed a bar later than current (lookahead leak)")
		}
	}
}

// 停牌：挂单在停牌 Bar 不成交，并产生 suspended 跳过记录。
func TestSuspendedBarSkips(t *testing.T) {
	pf := NewPortfolio(100000)
	br := NewBroker(BrokerConfig{}, pf)
	br.SetLastPrice("AAA", 10)
	br.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderLimit, Quantity: 10,
		LimitPx: 100, TIF: TIFGTC, Submitted: t0})

	b1 := bar(t0.AddDate(0, 0, 1), 10, 11, 9, 10)
	b1.Suspended = true
	br.ProcessBar(b1)

	if len(br.Fills()) != 0 {
		t.Fatal("no fill allowed on suspended bar")
	}
	skips := br.Skips()
	if len(skips) != 1 || skips[0].Reason != market.SkipSuspended {
		t.Fatalf("want 1 suspended skip, got %+v", skips)
	}
	// 下一根恢复交易，GTC 单仍在挂单并可成交。
	br.ProcessBar(bar(t0.AddDate(0, 0, 2), 10, 120, 9, 11))
	if len(br.Fills()) != 1 {
		t.Fatalf("gtc order should fill after resumption, fills=%d", len(br.Fills()))
	}
}

// 涨跌停：涨停时买单不可成交（limit_up），跌停时卖单不可成交（limit_down）。
func TestLimitUpDownSkips(t *testing.T) {
	pf := NewPortfolio(100000)
	br := NewBroker(BrokerConfig{}, pf)
	br.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderLimit, Quantity: 10,
		LimitPx: 100, TIF: TIFGTC, Submitted: t0})
	up := bar(t0.AddDate(0, 0, 1), 11, 11, 11, 11)
	up.LimitUp = true
	br.ProcessBar(up)
	found := false
	for _, s := range br.Skips() {
		if s.Reason == market.SkipLimitUp {
			found = true
		}
	}
	if !found {
		t.Error("buy on limit-up bar should be skipped with limit_up")
	}
	if len(br.Fills()) != 0 {
		t.Error("no fill on limit-up")
	}

	// 卖单 + 跌停（预置 100 股多头，使卖单通过资金/持仓校验）
	pf2 := NewPortfolio(100000)
	pf2.ApplyFill(&Fill{Symbol: "AAA", Side: Buy, Quantity: 100, Price: 10})
	br2 := NewBroker(BrokerConfig{}, pf2)
	br2.Submit(&Order{Symbol: "AAA", Side: Sell, Type: OrderMarket, Quantity: 10, Submitted: t0})
	down := bar(t0.AddDate(0, 0, 1), 9, 9, 9, 9)
	down.LimitDown = true
	br2.ProcessBar(down)
	found = false
	for _, s := range br2.Skips() {
		if s.Reason == market.SkipLimitDown {
			found = true
		}
	}
	if !found {
		t.Error("sell on limit-down bar should be skipped with limit_down")
	}
}

// 数据缺失：某时间点缺失 Bar 时不产生成交；GTC 限价单存活，市价单过期。
func TestMissingDataNoFill(t *testing.T) {
	pf := NewPortfolio(100000)
	br := NewBroker(BrokerConfig{}, pf)
	br.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderLimit, Quantity: 10,
		LimitPx: 100, TIF: TIFGTC, Submitted: t0})
	mo := br.Submit(&Order{Symbol: "AAA", Side: Buy, Type: OrderMarket, Quantity: 10, Submitted: t0})

	br.ProcessMissing("AAA", t0.AddDate(0, 0, 1))

	if len(br.Fills()) != 0 {
		t.Fatal("missing data must not produce fills")
	}
	var miss int
	for _, s := range br.Skips() {
		if s.Reason == market.SkipMissing {
			miss++
		}
	}
	if miss != 2 {
		t.Errorf("want 2 missing skips (2 orders), got %d", miss)
	}
	if mo.Status != StatusExpired {
		t.Errorf("market order should expire on missing data, got %s", mo.Status)
	}
	// GTC 单仍存活，下一真实 Bar 可成交。
	br.ProcessBar(bar(t0.AddDate(0, 0, 2), 10, 120, 9, 11))
	if len(br.Fills()) != 1 {
		t.Fatalf("gtc order should survive missing data and fill later, fills=%d", len(br.Fills()))
	}
}

// 多标的时间线：某标的缺失某天时，当天不触发该标的的 OnBar，也不成交。
func TestEngineMissingSymbolTimestamp(t *testing.T) {
	feed := market.NewMemoryFeed()
	feed.Add(market.Bar{Symbol: "AAA", Time: t0, Open: 10, High: 10, Low: 10, Close: 10})
	feed.Add(market.Bar{Symbol: "AAA", Time: t0.AddDate(0, 0, 1), Open: 11, High: 11, Low: 11, Close: 11})
	feed.Add(market.Bar{Symbol: "BBB", Time: t0, Open: 20, High: 20, Low: 20, Close: 20})
	// BBB 缺失第 2 天

	calls := map[string]int{}
	st := &fnStrategy{onBar: func(ctx *Context, b market.Bar) {
		calls[b.Symbol]++
		if b.Time.Equal(t0) {
			ctx.Buy(b.Symbol, 5)
		}
	}}
	eng := NewEngine(feed, st, Config{InitialCash: 100000})
	res, err := eng.Run()
	if err != nil {
		t.Fatal(err)
	}
	if calls["AAA"] != 2 || calls["BBB"] != 1 {
		t.Errorf("onbar calls = %v", calls)
	}
	// BBB 第 2 天缺失：其买单应因 missing 过期，无成交。
	for _, f := range res.Fills {
		if f.Symbol == "BBB" {
			t.Error("BBB order must not fill on its missing day")
		}
	}
	var bbbMissing bool
	for _, s := range res.Skips {
		if s.Symbol == "BBB" && s.Reason == market.SkipMissing {
			bbbMissing = true
		}
	}
	if !bbbMissing {
		t.Error("expected missing_data skip for BBB")
	}
}

type fnStrategy struct {
	onBar  func(ctx *Context, b market.Bar)
	finish func(ctx *Context)
}

func (s *fnStrategy) OnBar(ctx *Context, b market.Bar) {
	if s.onBar != nil {
		s.onBar(ctx, b)
	}
}
func (s *fnStrategy) Finish(ctx *Context) {
	if s.finish != nil {
		s.finish(ctx)
	}
}
