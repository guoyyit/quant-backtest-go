package market

import "time"

// Bar 表示一个标的在某个时间区间（如日线、1 分钟线）上的 OHLCV 行情。
// Bid/Ask 可选；为 0 时由撮合引擎根据价差参数合成。
type Bar struct {
	Symbol    string
	Time      time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	Bid       float64
	Ask       float64
	Suspended bool // 停牌：该时间段整根 Bar 不可交易
	LimitUp   bool // 涨停：买入方向不可成交
	LimitDown bool // 跌停：卖出方向不可成交
}

// SkipReason 描述一根 Bar 或一笔委托被跳过、不可成交的可区分原因。
type SkipReason string

const (
	SkipNone      SkipReason = ""
	SkipSuspended SkipReason = "suspended"    // 停牌
	SkipLimitUp   SkipReason = "limit_up"     // 涨停，买单不可成交
	SkipLimitDown SkipReason = "limit_down"   // 跌停，卖单不可成交
	SkipMissing   SkipReason = "missing_data" // 行情缺失
)

// Tradable 返回该 Bar 本身是否可交易（与方向无关）。
// 停牌或价格数据非法（<=0）时视为不可交易，原因可区分。
func (b Bar) Tradable() (bool, SkipReason) {
	if b.Suspended {
		return false, SkipSuspended
	}
	if b.Open <= 0 || b.High <= 0 || b.Low <= 0 || b.Close <= 0 || b.High < b.Low {
		return false, SkipMissing
	}
	return true, SkipNone
}

// SideTradable 返回指定方向（true=买, false=卖）在该 Bar 上是否允许成交。
// 先检查 Bar 本身是否可交易，再检查涨跌停方向限制。
func (b Bar) SideTradable(buy bool) (bool, SkipReason) {
	if ok, why := b.Tradable(); !ok {
		return false, why
	}
	if buy && b.LimitUp {
		return false, SkipLimitUp
	}
	if !buy && b.LimitDown {
		return false, SkipLimitDown
	}
	return true, SkipNone
}
