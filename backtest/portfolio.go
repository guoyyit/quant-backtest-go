package backtest

import "time"

// Position 记录单标的持仓与成本基础。
// Quantity 为正表示多头，为负表示空头（仅在显式允许卖空时出现）。
type Position struct {
	Symbol     string
	Quantity   float64
	AvgCost    float64 // 每股平均建仓成本（含摊入的买入费用）
	RealizedPL float64 // 该标的累计已实现盈亏（含平仓费用）
}

// EquityPoint 是某一时点的组合快照，用于净值序列与指标复算。
type EquityPoint struct {
	Time          time.Time
	Cash          float64
	PositionValue float64 // 持仓按盯市价格计值（空头为负贡献）
	Equity        float64 // Cash + PositionValue
	Turnover      float64 // 截止该时点的累计成交额（不含手续费）
}

type Portfolio struct {
	Cash      float64
	Initial   float64
	Positions map[string]*Position
	Equity    []EquityPoint
	Turnover  float64 // 累计成交额
	Fees      float64 // 累计手续费
}

func NewPortfolio(initialCash float64) *Portfolio {
	return &Portfolio{
		Cash:      initialCash,
		Initial:   initialCash,
		Positions: make(map[string]*Position),
	}
}

func (p *Portfolio) Position(symbol string) Position {
	if pos, ok := p.Positions[symbol]; ok {
		return *pos
	}
	return Position{Symbol: symbol}
}

// ApplyFill 按逐笔成交更新现金、持仓数量、成本基础与已实现盈亏。
// 返回该笔成交贡献的已实现盈亏。约定：
//   - 买入：现金 -= 成交额 + 手续费；
//   - 卖出：现金 += 成交额 - 手续费；
//   - 平仓部分按移动加权平均成本结转已实现盈亏，平仓费用直接扣减；
//   - 开仓部分的手续费摊入新仓的 AvgCost。
func (p *Portfolio) ApplyFill(f *Fill) float64 {
	notional := f.Price * f.Quantity
	p.Turnover += notional
	p.Fees += f.Commission

	pos, ok := p.Positions[f.Symbol]
	if !ok {
		pos = &Position{Symbol: f.Symbol}
		p.Positions[f.Symbol] = pos
	}

	realized := 0.0
	switch f.Side {
	case Buy:
		p.Cash -= notional + f.Commission
		realized = p.applyBuy(pos, *f)
	case Sell:
		p.Cash += notional - f.Commission
		realized = p.applySell(pos, *f)
	}
	pos.RealizedPL += realized
	f.RealizedPL = realized
	return realized
}

func (p *Portfolio) applyBuy(pos *Position, f Fill) float64 {
	qty, px, fee := f.Quantity, f.Price, f.Commission
	realized := 0.0
	if pos.Quantity < 0 {
		// 先回补空头
		cover := qty
		if cover > -pos.Quantity {
			cover = -pos.Quantity
		}
		feeCover := fee * cover / qty
		realized += (pos.AvgCost-px)*cover - feeCover
		pos.Quantity += cover
		qty -= cover
		fee -= feeCover
		if abs(pos.Quantity) < 1e-12 {
			pos.Quantity = 0
			pos.AvgCost = 0
		}
	}
	if qty > 0 {
		// 剩余部分开/加多仓，费用摊入成本
		newQty := pos.Quantity + qty
		pos.AvgCost = (pos.Quantity*pos.AvgCost + qty*px + fee) / newQty
		pos.Quantity = newQty
	}
	return realized
}

func (p *Portfolio) applySell(pos *Position, f Fill) float64 {
	qty, px, fee := f.Quantity, f.Price, f.Commission
	realized := 0.0
	if pos.Quantity > 0 {
		// 先平多仓
		closeQty := qty
		if closeQty > pos.Quantity {
			closeQty = pos.Quantity
		}
		feeClose := fee * closeQty / qty
		realized += (px-pos.AvgCost)*closeQty - feeClose
		pos.Quantity -= closeQty
		qty -= closeQty
		fee -= feeClose
		if abs(pos.Quantity) < 1e-12 {
			pos.Quantity = 0
			pos.AvgCost = 0
		}
	}
	if qty > 0 {
		// 剩余部分开/加空仓（额度由券商校验），费用摊入成本
		newQty := -pos.Quantity + qty
		pos.AvgCost = ((-pos.Quantity)*pos.AvgCost + qty*px + fee) / newQty
		pos.Quantity = -newQty
	}
	return realized
}

// MarkToMarket 以给定盯市价格快照组合权益并追加到净值序列。
func (p *Portfolio) MarkToMarket(t time.Time, prices map[string]float64) EquityPoint {
	pv := 0.0
	for sym, pos := range p.Positions {
		if px, ok := prices[sym]; ok {
			pv += pos.Quantity * px
		}
	}
	pt := EquityPoint{
		Time:          t,
		Cash:          p.Cash,
		PositionValue: pv,
		Equity:        p.Cash + pv,
		Turnover:      p.Turnover,
	}
	p.Equity = append(p.Equity, pt)
	return pt
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
