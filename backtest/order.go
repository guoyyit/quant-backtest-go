package backtest

import "time"

type Side string

const (
	Buy  Side = "buy"
	Sell Side = "sell"
)

type OrderType string

const (
	OrderMarket OrderType = "market"
	OrderLimit  OrderType = "limit"
)

// TIF（Time In Force）控制限价单有效期。
type TIF string

const (
	TIFFOK TIF = "FOK" // Fill-Or-Kill：当根 Bar 条件不满足立即撤销
	TIFFAK TIF = "FAK" // Fill-And-Kill：同 FOK，命名别名
	TIFGTC TIF = "GTC" // Good-Till-Cancelled：保持挂单直至成交或撤销
	TIFDAY TIF = "DAY" // 当根 Bar 结束未成交则过期
)

type OrderStatus string

const (
	StatusNew      OrderStatus = "new"
	StatusWorking  OrderStatus = "working"
	StatusFilled   OrderStatus = "filled"
	StatusCanceled OrderStatus = "canceled"
	StatusExpired  OrderStatus = "expired"
	StatusRejected OrderStatus = "rejected"
)

type RejectReason string

const (
	RejectNone             RejectReason = ""
	RejectInsufficientCash RejectReason = "insufficient_cash"
	RejectNoPosition       RejectReason = "no_position"
	RejectBelowZero        RejectReason = "position_would_be_negative"
	RejectExceedShortLimit RejectReason = "exceeds_short_limit"
	RejectInvalidOrder     RejectReason = "invalid_order"
)

type Order struct {
	ID        int64
	Symbol    string
	Side      Side
	Type      OrderType
	Quantity  float64
	LimitPx   float64
	TIF       TIF
	OneShot   bool // 市价单/DAY/FOK：仅对提交之后的第一根 Bar 有效
	Submitted time.Time
	ExpiresAt time.Time // 零值表示不过期（配合 TIF 解释）
	Status    OrderStatus
	Reject    RejectReason
}

// Fill 是一笔已成交记录（逐笔，可用于指标复算）。
type Fill struct {
	OrderID    int64
	Symbol     string
	Side       Side
	Quantity   float64
	Price      float64 // 含价差、滑点后的实际成交价
	Commission float64
	RealizedPL float64 // 该笔成交产生的已实现盈亏（平仓部分）
	Time       time.Time
}
