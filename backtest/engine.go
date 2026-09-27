package backtest

import (
	"fmt"
	"sort"
	"time"

	"github.com/guoyyit/quant-backtest-go/market"
)

// Strategy 由策略实现：OnBar 在每根 Bar 收盘后被调用，
// 此时 ctx 只暴露截至当前 Bar（含）的信息，严禁前瞻。
type Strategy interface {
	OnBar(ctx *Context, bar market.Bar)
	Finish(ctx *Context)
}

// Context 是策略下单与查询的唯一入口，屏蔽未来数据。
type Context struct {
	now    time.Time
	broker *Broker
	pf     *Portfolio
	bars   map[string][]market.Bar // 每标的截至当前（含）的历史 Bar
}

func (c *Context) Now() time.Time { return c.now }

func (c *Context) Buy(symbol string, qty float64) *Order {
	return c.submit(&Order{
		Symbol: symbol, Side: Buy, Type: OrderMarket, Quantity: qty,
		OneShot: true, TIF: TIFFOK, Submitted: c.now,
	})
}

func (c *Context) Sell(symbol string, qty float64) *Order {
	return c.submit(&Order{
		Symbol: symbol, Side: Sell, Type: OrderMarket, Quantity: qty,
		OneShot: true, TIF: TIFFOK, Submitted: c.now,
	})
}

func (c *Context) BuyLimit(symbol string, qty, px float64, tif TIF) *Order {
	return c.submit(&Order{
		Symbol: symbol, Side: Buy, Type: OrderLimit, Quantity: qty,
		LimitPx: px, TIF: tif, Submitted: c.now,
	})
}

func (c *Context) SellLimit(symbol string, qty, px float64, tif TIF) *Order {
	return c.submit(&Order{
		Symbol: symbol, Side: Sell, Type: OrderLimit, Quantity: qty,
		LimitPx: px, TIF: tif, Submitted: c.now,
	})
}

func (c *Context) submit(o *Order) *Order {
	if o.TIF == "" {
		if o.Type == OrderLimit {
			o.TIF = TIFGTC
		} else {
			o.TIF = TIFFOK
		}
	}
	if o.Type == OrderMarket {
		o.OneShot = true
	}
	if o.TIF == TIFFOK || o.TIF == TIFFAK || o.TIF == TIFDAY {
		o.OneShot = true
	}

	return c.broker.Submit(o)
}

// PlaceOrder 允许策略设置过期时间等高级字段（TIF=GTC 时可用）。
func (c *Context) PlaceOrder(o *Order) *Order {
	if o.Submitted.IsZero() {
		o.Submitted = c.now
	}
	return c.broker.Submit(o)
}

func (c *Context) Cancel(orderID int64) bool { return c.broker.Cancel(orderID) }

func (c *Context) Cash() float64                   { return c.pf.Cash }
func (c *Context) InitialCash() float64            { return c.pf.Initial }
func (c *Context) Position(symbol string) Position { return c.pf.Position(symbol) }

// MaxBuyQuantity 按券商当前的最坏买入成本（价差/滑点/费用/跳空缓冲）
// 与可用现金，估算可买入的最大股数（未取整）。策略据此下单可通过资金校验。
func (c *Context) MaxBuyQuantity(symbol string) float64 {
	return c.broker.MaxBuyQuantity(symbol)
}

func (c *Context) Positions() map[string]Position {
	out := make(map[string]Position, len(c.pf.Positions))
	for sym, pos := range c.pf.Positions {
		if pos.Quantity != 0 {
			out[sym] = *pos
		}
	}
	return out
}

// LastClose 返回某标的截至当前最近一根 Bar 的收盘价；无数据时返回 0,false。
func (c *Context) LastClose(symbol string) (float64, bool) {
	h := c.bars[symbol]
	if len(h) == 0 {
		return 0, false
	}
	return h[len(h)-1].Close, true
}

// History 返回截至当前（含当前 Bar）的历史行情副本，策略无法借其窥探未来。
func (c *Context) History(symbol string) []market.Bar {
	h := c.bars[symbol]
	out := make([]market.Bar, len(h))
	copy(out, h)
	return out
}

// Result 是一次回测的完整产物。
type Result struct {
	Equity     []EquityPoint
	Fills      []Fill
	Orders     []*Order
	Skips      []SkipRecord
	FinalValue float64
}

type Config struct {
	InitialCash float64
	Broker      BrokerConfig
}

type Engine struct {
	feed  market.Feed
	strat Strategy
	cfg   Config
}

func NewEngine(feed market.Feed, strat Strategy, cfg Config) *Engine {
	return &Engine{feed: feed, strat: strat, cfg: cfg}
}

// Run 按合并时间线逐 Bar 回放。事件顺序（对每个时间点 t）：
//  1. 先用 t 的行情撮合在 t 之前已挂出的订单（提交于 t 的订单不可能在 t 成交）；
//  2. 再以 t 的收盘价盯市；
//  3. 最后把含当前 Bar 的历史交给策略 OnBar 决策。
//
// 全过程中策略仅能访问截至当前 Bar 的数据，杜绝前瞻偏差。
func (e *Engine) Run() (*Result, error) {
	if e.cfg.InitialCash <= 0 {
		return nil, fmt.Errorf("initial cash must be positive, got %v", e.cfg.InitialCash)
	}
	symbols := e.feed.Symbols()
	if len(symbols) == 0 {
		return nil, fmt.Errorf("feed contains no symbols")
	}

	// 建立 symbol -> 时间升序 Bar 列表与索引。
	bySymbol := make(map[string][]market.Bar, len(symbols))
	idx := make(map[string]int, len(symbols))
	timelineSet := make(map[int64]time.Time)
	for _, sym := range symbols {
		bars := e.feed.Bars(sym)
		for i := 1; i < len(bars); i++ {
			if bars[i].Time.Before(bars[i-1].Time) {
				return nil, fmt.Errorf("bars of %s not sorted ascending", sym)
			}
		}
		bySymbol[sym] = bars
		idx[sym] = 0
		for _, b := range bars {
			timelineSet[b.Time.UnixNano()] = b.Time
		}
	}
	timeline := make([]time.Time, 0, len(timelineSet))
	for _, t := range timelineSet {
		timeline = append(timeline, t)
	}
	sort.Slice(timeline, func(i, j int) bool { return timeline[i].Before(timeline[j]) })

	pf := NewPortfolio(e.cfg.InitialCash)
	broker := NewBroker(e.cfg.Broker, pf)
	ctx := &Context{broker: broker, pf: pf, bars: make(map[string][]market.Bar)}

	for _, t := range timeline {
		ctx.now = t

		// 1) 撮合（缺失标的走 ProcessMissing，绝不产生成交）。
		for _, sym := range symbols {
			bars := bySymbol[sym]
			i := idx[sym]
			if i < len(bars) && bars[i].Time.Equal(t) {
				broker.ProcessBar(bars[i])
				i++
				idx[sym] = i
			} else {
				broker.ProcessMissing(sym, t)
			}
		}

		// 2) 当前 Bar 此刻已成为历史：更新策略可见历史、券商估值价与盯市价。
		prices := make(map[string]float64, len(symbols))
		type curBar struct {
			sym string
			bar market.Bar
		}
		var current []curBar
		for _, sym := range symbols {
			i := idx[sym]
			if i > 0 && bySymbol[sym][i-1].Time.Equal(t) {
				cur := bySymbol[sym][i-1]
				ctx.bars[sym] = append(ctx.bars[sym], cur)
				if cur.Close > 0 {
					broker.SetLastPrice(sym, cur.Close)
					prices[sym] = cur.Close
				}
				current = append(current, curBar{sym, cur})
			} else if i > 0 {
				// 该标的当前时间点缺失：沿用最近收盘价盯市（但不产生成交、不触发 OnBar）。
				if last := bySymbol[sym][i-1]; last.Close > 0 {
					prices[sym] = last.Close
				}
			}
		}

		// 3) 撮合后以当根收盘价盯市，保证当根成交立即反映到净值。
		pf.MarkToMarket(t, prices)

		// 4) 把截至当前（含）的历史交给策略决策。下单时 lastPx 已是当根收盘价。
		for _, cb := range current {
			e.strat.OnBar(ctx, cb.bar)
		}
	}

	e.strat.Finish(ctx)

	final := pf.Initial
	if len(pf.Equity) > 0 {
		final = pf.Equity[len(pf.Equity)-1].Equity
	}
	return &Result{
		Equity:     pf.Equity,
		Fills:      broker.Fills(),
		Orders:     broker.Orders(),
		Skips:      broker.Skips(),
		FinalValue: final,
	}, nil
}
