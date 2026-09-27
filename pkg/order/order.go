package order

import "time"

type Side int

const (
	Buy Side = iota
	Sell
)

func (s Side) String() string {
	if s == Buy {
		return "buy"
	}
	return "sell"
}

type Type int

const (
	Market Type = iota
	Limit
)

type TIF int

const (
	// GTC: good till cancelled (still bounded by backtest end).
	GTC TIF = iota
	// DAY: expires at the end of the bar on which it becomes active.
	DAY
	// GTD: good till the configured expiry time.
	GTD
)

// Order is a strategy instruction. Orders submitted at bar t can only be
// matched against bars strictly after t (no look-ahead).
type Order struct {
	ID         string
	Symbol     string
	Side       Side
	Type       Type
	Qty        float64
	LimitPrice float64
	TIF        TIF
	Expiry     time.Time
	Submitted  time.Time

	// ShortSale marks an explicit short sell, allowed only when enabled.
	ShortSale bool
}

type Status int

const (
	Pending Status = iota
	Submitted
	Filled
	PartiallyFilled
	Cancelled
	Expired
	Rejected
)

// Fill records one execution. Price already includes slippage; Commission is
// charged separately.
type Fill struct {
	OrderID    string
	Symbol     string
	Side       Side
	Qty        float64
	Price      float64
	Commission float64
	Time       time.Time
}

// RejectReason distinguishes failure causes.
type RejectReason string

const (
	RejectNone             RejectReason = ""
	RejectInsufficientCash RejectReason = "insufficient_cash"
	RejectInvalidQty       RejectReason = "invalid_qty"
	RejectInvalidPrice     RejectReason = "invalid_price"
	RejectNoPosition       RejectReason = "no_position"
	RejectShortNotAllowed  RejectReason = "short_not_allowed"
	RejectShortQuota       RejectReason = "short_quota_exceeded"
	RejectInactiveBar      RejectReason = "inactive_bar"
	RejectUnknownSymbol    RejectReason = "unknown_symbol"
)

// Rejection pairs an order with why it was refused.
type Rejection struct {
	Order  Order
	Reason RejectReason
	Detail string
	Time   time.Time
}
