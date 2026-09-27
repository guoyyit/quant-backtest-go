package strategy

import (
	"github.com/guoyyit/quant-backtest-go/backtest"
	"github.com/guoyyit/quant-backtest-go/market"
)

// MACross 是双均线交叉示例策略：
//   - 快线上穿慢线（金叉）：按券商最坏买入成本估算，全仓（整手）买入；
//   - 快线下穿慢线（死叉）：市价全部卖出。
//
// 策略仅读取 ctx.History（截至当前 Bar），不使用任何随机源，
// 因此固定输入与参数下结果完全可复现。
type MACross struct {
	Symbol  string
	Fast    int
	Slow    int
	LotSize float64 // 每手股数（100 表示 A 股整手），<=0 视为 1
}

func NewMACross(symbol string, fast, slow int, lotSize float64) *MACross {
	if lotSize <= 0 {
		lotSize = 1
	}
	return &MACross{Symbol: symbol, Fast: fast, Slow: slow, LotSize: lotSize}
}

func sma(vals []market.Bar, period int) float64 {
	if len(vals) < period {
		return 0
	}
	sum := 0.0
	for _, b := range vals[len(vals)-period:] {
		sum += b.Close
	}
	return sum / float64(period)
}

func (s *MACross) OnBar(ctx *backtest.Context, bar market.Bar) {
	if bar.Symbol != s.Symbol || s.Fast <= 0 || s.Slow <= 0 || s.Fast >= s.Slow {
		return
	}
	hist := ctx.History(s.Symbol)
	if len(hist) < s.Slow+1 {
		return
	}

	fastNow := sma(hist, s.Fast)
	slowNow := sma(hist, s.Slow)
	fastPrev := sma(hist[:len(hist)-1], s.Fast)
	slowPrev := sma(hist[:len(hist)-1], s.Slow)

	pos := ctx.Position(s.Symbol)

	switch {
	case fastPrev <= slowPrev && fastNow > slowNow && pos.Quantity <= 0:
		// 金叉：按券商最坏成本（价差/滑点/费用/跳空缓冲）取整手买入，
		// 保证能通过提交时的资金校验。
		maxQty := ctx.MaxBuyQuantity(s.Symbol)
		qty := float64(int64(maxQty/s.LotSize)) * s.LotSize
		if qty >= s.LotSize {
			ctx.Buy(s.Symbol, qty)
		}
	case fastPrev >= slowPrev && fastNow < slowNow && pos.Quantity > 0:
		// 死叉：全部市价卖出。
		ctx.Sell(s.Symbol, pos.Quantity)
	}
}

func (s *MACross) Finish(ctx *backtest.Context) {}
