package backtest

import (
	"time"

	"github.com/guoyyit/quant-backtest-go/market"
)

// CommissionModel 计算单笔成交的手续费。
type CommissionModel interface {
	Commission(notional, qty float64) float64
}

// FlatCommission 每笔固定费用。
type FlatCommission struct{ Fee float64 }

func (c FlatCommission) Commission(notional, qty float64) float64 { return c.Fee }

// RateCommission 按成交额比例收费，并设最低费用。
type RateCommission struct {
	Rate   float64
	MinFee float64
}

func (c RateCommission) Commission(notional, qty float64) float64 {
	fee := notional * c.Rate
	if fee < c.MinFee {
		return c.MinFee
	}
	return fee
}

// BrokerConfig 描述费用与撮合模型参数。
type BrokerConfig struct {
	Commission CommissionModel
	Spread     float64 // 相对价差（0.001=0.1%），Bar 无 Bid/Ask 时在中间价两侧各加一半
	Slippage   float64 // 相对滑点，买入上浮、卖出下压
	// MarketCashBuffer 是市价买单预留资金时在最坏价之外额外预留的比例
	// （如 0.02=2%），用于覆盖隔夜跳空。成交后多预留的部分立即释放。
	MarketCashBuffer float64
	AllowShort       bool
	MaxShortQty      map[string]float64 // 各标的最大卖空数量（绝对值）
	DefaultShortQty  float64            // 未单独配置时的默认卖空额度
}

// SkipRecord 记录因异常行情导致的跳过（无成交），原因可区分。
type SkipRecord struct {
	Time    time.Time
	Symbol  string
	Reason  market.SkipReason
	Detail  string
	OrderID int64 // 0 表示 Bar 级别的跳过，不针对具体订单
}

type Broker struct {
	cfg     BrokerConfig
	pf      *Portfolio
	orders  map[int64]*Order
	working []*Order
	fills   []Fill
	skipped []SkipRecord
	nextID  int64

	lastPx       map[string]float64 // 最近一根 Bar 的收盘价（提交时的可用价格）
	cashReserved float64
	sellReserved map[string]float64
	buyReserve   map[int64]float64
	sellReserve  map[int64]float64
}

func NewBroker(cfg BrokerConfig, pf *Portfolio) *Broker {
	if cfg.Commission == nil {
		cfg.Commission = FlatCommission{}
	}
	return &Broker{
		cfg:          cfg,
		pf:           pf,
		orders:       make(map[int64]*Order),
		lastPx:       make(map[string]float64),
		sellReserved: make(map[string]float64),
		buyReserve:   make(map[int64]float64),
		sellReserve:  make(map[int64]float64),
	}
}

// SetLastPrice 由引擎在每根 Bar 的策略阶段前调用，保证下单估值只用截至当前的信息。
func (b *Broker) SetLastPrice(symbol string, px float64) {
	b.lastPx[symbol] = px
}

func (b *Broker) commission(notional, qty float64) float64 {
	return b.cfg.Commission.Commission(notional, qty)
}

func (b *Broker) shortQuota(symbol string) float64 {
	if !b.cfg.AllowShort {
		return 0
	}
	if q, ok := b.cfg.MaxShortQty[symbol]; ok {
		return q
	}
	return b.cfg.DefaultShortQty
}

// Submit 校验并接受/拒绝一笔委托。被接受的委托从“下一根 Bar”起才可能成交，
// 因此即便提交发生在当前 Bar 的 OnBar 中，也绝不可能使用当前 Bar 的价格成交。
func (b *Broker) Submit(o *Order) *Order {
	b.nextID++
	o.ID = b.nextID
	o.Status = StatusNew
	if o.Submitted.IsZero() {
		o.Submitted = time.Time{}
	}

	if o.Quantity <= 0 {
		o.Status = StatusRejected
		o.Reject = RejectInvalidOrder
	} else if o.Type == OrderLimit && o.LimitPx <= 0 {
		o.Status = StatusRejected
		o.Reject = RejectInvalidOrder
	} else if o.Side != Buy && o.Side != Sell {
		o.Status = StatusRejected
		o.Reject = RejectInvalidOrder
	}

	if o.Status == StatusRejected {
		b.orders[o.ID] = o
		return o
	}

	if o.Side == Buy {
		cost := b.estimatedBuyCost(o)
		if cost > b.pf.Cash-b.cashReserved+1e-9 {
			o.Status = StatusRejected
			o.Reject = RejectInsufficientCash
			b.orders[o.ID] = o
			return o
		}
		b.cashReserved += cost
		b.buyReserve[o.ID] = cost
	} else {
		pos := b.pf.Position(o.Symbol)
		available := pos.Quantity + b.shortQuota(o.Symbol) - b.sellReserved[o.Symbol]
		if o.Quantity > available+1e-9 {
			o.Status = StatusRejected
			if !b.cfg.AllowShort {
				if pos.Quantity <= 0 {
					o.Reject = RejectNoPosition
				} else {
					o.Reject = RejectBelowZero
				}
			} else {
				o.Reject = RejectExceedShortLimit
			}
			b.orders[o.ID] = o
			return o
		}
		b.sellReserved[o.Symbol] += o.Quantity
		b.sellReserve[o.ID] = o.Quantity
	}

	o.Status = StatusWorking
	b.orders[o.ID] = o
	b.working = append(b.working, o)
	return o
}

func (b *Broker) estimatedBuyCost(o *Order) float64 {
	px := b.lastPx[o.Symbol]
	if o.Type == OrderLimit {
		// 限价买入的最坏成本就是限价本身（忽略滑点，作为保守资金预留）。
		px = o.LimitPx
	} else if px > 0 {
		px = px * (1 + b.cfg.Spread/2) * (1 + b.cfg.Slippage) * (1 + b.cfg.MarketCashBuffer)
	}
	notional := o.Quantity * px
	return notional + b.commission(notional, o.Quantity)
}

// MaxBuyQuantity 返回当前可用现金按市价最坏成本能够买入的最大股数。
// 对固定手续费与比例手续费均成立（通过指数扩容+二分搜索求最大可行数量）。
func (b *Broker) MaxBuyQuantity(symbol string) float64 {
	probe := &Order{Symbol: symbol, Side: Buy, Type: OrderMarket}
	unit := b.estimatedBuyCost(&Order{Symbol: symbol, Side: Buy,
		Type: OrderMarket, Quantity: 1})
	if unit <= 0 {
		return 0
	}
	available := b.pf.Cash - b.cashReserved
	if available <= 0 {
		return 0
	}
	cost := func(q float64) float64 {
		probe.Quantity = q
		return b.estimatedBuyCost(probe)
	}
	hi := available / unit // 初始上界（费率非负时必为可行数量）
	// 指数扩容找到不可行上界。
	for cost(hi) <= available {
		hi *= 2
	}
	lo := 0.0
	for i := 0; i < 64; i++ {
		mid := (lo + hi) / 2
		if cost(mid) <= available {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// Cancel 撤销一笔尚在挂单的委托，并释放其预留资源。
func (b *Broker) Cancel(orderID int64) bool {
	o := b.orders[orderID]
	if o == nil || o.Status != StatusWorking {
		return false
	}
	o.Status = StatusCanceled
	b.release(o)
	b.removeWorking(orderID)
	return true
}

func (b *Broker) release(o *Order) {
	if r, ok := b.buyReserve[o.ID]; ok {
		b.cashReserved -= r
		delete(b.buyReserve, o.ID)
	}
	if r, ok := b.sellReserve[o.ID]; ok {
		b.sellReserved[o.Symbol] -= r
		delete(b.sellReserve, o.ID)
	}
}

func (b *Broker) removeWorking(id int64) {
	out := b.working[:0]
	for _, o := range b.working {
		if o.ID != id {
			out = append(out, o)
		}
	}
	b.working = out
}

func (b *Broker) expire(o *Order) {
	o.Status = StatusExpired
	b.release(o)
	b.removeWorking(o.ID)
}

// ProcessMissing 处理某标的在时间 t 完全缺失 Bar 的情形：
// GTC 限价单继续挂起；市价单与 FOK/DAY 限价单过期；全程不产生成交。
func (b *Broker) ProcessMissing(symbol string, t time.Time) {
	b.scanOrders(symbol, t, false, market.Bar{}, market.SkipMissing,
		"data missing: no bar for this timestamp")
}

// ProcessBar 在一根新 Bar 上处理挂单并撮合。
// 仅处理提交时间严格早于 Bar 时间的订单（防前瞻）。
func (b *Broker) ProcessBar(bar market.Bar) {
	b.lastPx[bar.Symbol] = bar.Close
	if ok, why := bar.Tradable(); !ok {
		b.scanOrders(bar.Symbol, bar.Time, true, bar, why,
			"bar not tradable")
		return
	}
	b.scanOrders(bar.Symbol, bar.Time, true, bar, market.SkipNone, "")
}

// scanOrders 按订单 ID 顺序（确定性）遍历某标的挂单。
func (b *Broker) scanOrders(symbol string, t time.Time, hasBar bool, bar market.Bar, blocked market.SkipReason, detail string) {
	snapshot := append([]*Order(nil), b.working...)
	for _, o := range snapshot {
		if o.Symbol != symbol {
			continue
		}
		if !o.Submitted.Before(t) {
			continue // 防前瞻：本 Bar 或之后提交的订单不得在本 Bar 成交
		}
		if o.Type == OrderLimit && !o.ExpiresAt.IsZero() && t.After(o.ExpiresAt) {
			b.expire(o)
			continue
		}

		sideBlocked := blocked
		if hasBar && blocked == market.SkipNone {
			buy := o.Side == Buy
			if ok, why := bar.SideTradable(buy); !ok {
				sideBlocked = why
			}
		}
		if sideBlocked != market.SkipNone {
			// GTC 限价单在方向被涨跌停/停牌屏蔽时继续挂起；其余单到期。
			if !o.OneShot && o.Type == OrderLimit && o.TIF == TIFGTC && (o.ExpiresAt.IsZero() || !t.After(o.ExpiresAt)) {
				b.skipped = append(b.skipped, SkipRecord{
					Time: t, Symbol: symbol, Reason: sideBlocked,
					Detail: detail + "; gtc order remains working", OrderID: o.ID,
				})
				continue
			}
			b.skipped = append(b.skipped, SkipRecord{
				Time: t, Symbol: symbol, Reason: sideBlocked,
				Detail: detail, OrderID: o.ID,
			})
			b.expire(o)
			continue
		}

		if !hasBar {
			// 数据缺失：GTC 限价单保持挂单；市价单与 FOK/DAY 限价单过期。
			if !o.OneShot && o.Type == OrderLimit && o.TIF == TIFGTC {
				continue
			}
			b.expire(o)
			continue
		}

		fillPx, canFill := b.matchPrice(o, bar)
		if !canFill {
			// 限价条件未满足：FOK/DAY 当根过期，GTC 保持挂单。
			if o.Type == OrderLimit && o.TIF == TIFGTC {
				continue
			}
			b.expire(o)
			continue
		}
		b.execute(o, bar, fillPx, t)
	}
}

// matchPrice 返回含价差与滑点的成交价，以及限价条件是否满足。
// 市价单在 Bar 开盘价（或 Bid/Ask）上成交；限价单在限价或更优价格成交。
func (b *Broker) matchPrice(o *Order, bar market.Bar) (float64, bool) {
	if o.Side == Buy {
		ref := bar.Open
		if bar.Ask > 0 {
			ref = bar.Ask
		} else {
			ref = ref * (1 + b.cfg.Spread/2)
		}
		ref *= 1 + b.cfg.Slippage
		if o.Type == OrderMarket {
			return ref, true
		}
		// 限价买入：Bar 最低价触及限价才可能成交；成交价不劣于限价。
		if bar.Low > o.LimitPx {
			return 0, false
		}
		if ref < o.LimitPx {
			return ref, true // 跳空低开，按更优的开盘买价成交
		}
		return o.LimitPx, true
	}

	ref := bar.Open
	if bar.Bid > 0 {
		ref = bar.Bid
	} else {
		ref = ref * (1 - b.cfg.Spread/2)
	}
	ref *= 1 - b.cfg.Slippage
	if o.Type == OrderMarket {
		return ref, true
	}
	// 限价卖出：Bar 最高价触及限价才可能成交。
	if bar.High < o.LimitPx {
		return 0, false
	}
	if ref > o.LimitPx {
		return ref, true
	}
	return o.LimitPx, true
}

// execute 在最终资金/卖空复核通过后生成逐笔成交；复核失败则拒绝该单，
// 保证现金与多头持仓在未启用卖空时永不为负。
func (b *Broker) execute(o *Order, bar market.Bar, px float64, t time.Time) {
	notional := o.Quantity * px
	fee := b.commission(notional, o.Quantity)

	if o.Side == Buy {
		otherReserve := b.cashReserved - b.buyReserve[o.ID]
		if notional+fee > b.pf.Cash-otherReserve+1e-9 {
			o.Status = StatusRejected
			o.Reject = RejectInsufficientCash
			b.release(o)
			b.removeWorking(o.ID)
			return
		}
	} else {
		pos := b.pf.Position(o.Symbol)
		otherReserve := b.sellReserved[o.Symbol] - b.sellReserve[o.ID]
		post := pos.Quantity - otherReserve - o.Quantity
		if post < -1e-9 {
			if !b.cfg.AllowShort {
				o.Status = StatusRejected
				o.Reject = RejectBelowZero
			} else if -post > b.shortQuota(o.Symbol)+1e-9 {
				o.Status = StatusRejected
				o.Reject = RejectExceedShortLimit
			}
			if o.Status == StatusRejected {
				b.release(o)
				b.removeWorking(o.ID)
				return
			}
		}
	}

	fill := Fill{
		OrderID:    o.ID,
		Symbol:     o.Symbol,
		Side:       o.Side,
		Quantity:   o.Quantity,
		Price:      px,
		Commission: fee,
		Time:       t,
	}
	b.release(o)
	b.pf.ApplyFill(&fill)
	b.fills = append(b.fills, fill)
	o.Status = StatusFilled
	b.removeWorking(o.ID)
}

func (b *Broker) Fills() []Fill {
	out := make([]Fill, len(b.fills))
	copy(out, b.fills)
	return out
}

func (b *Broker) Orders() []*Order {
	ids := make([]int64, 0, len(b.orders))
	for id := range b.orders {
		ids = append(ids, id)
	}
	out := make([]*Order, 0, len(ids))
	for _, id := range ids {
		out = append(out, b.orders[id])
	}
	return out
}

func (b *Broker) Skips() []SkipRecord {
	out := make([]SkipRecord, len(b.skipped))
	copy(out, b.skipped)
	return out
}
