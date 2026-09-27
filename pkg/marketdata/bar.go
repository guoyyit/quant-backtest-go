package marketdata

import "time"

// Side describes whether a bar is halted (suspended) or locked by a price limit.
type Side int

// Bar is one OHLCV interval for one symbol. EndTime = StartTime + Interval.
// A Bar is "closed" at EndTime: strategies may only see bars whose EndTime
// is not after the current engine time, to prevent look-ahead bias.
type Bar struct {
	Symbol    string
	StartTime time.Time
	Interval  time.Duration
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64

	// Halted is true when the symbol is suspended for the whole bar.
	Halted bool
	// LimitUp / LimitDown mark bars locked at the daily price limit.
	LimitUp   bool
	LimitDown bool
}

// EndTime returns the time at which this bar finishes forming.
func (b Bar) EndTime() time.Time { return b.StartTime.Add(b.Interval) }

// Tradable reports whether any order may trade during this bar.
func (b Bar) Tradable() bool { return !b.Halted && !b.LimitUp && !b.LimitDown }

// SkipReason explains why a bar produced no fills.
type SkipReason string

const (
	SkipNone       SkipReason = ""
	SkipHalted     SkipReason = "halted"
	SkipLimitUp    SkipReason = "limit_up"
	SkipLimitDown  SkipReason = "limit_down"
	SkipMissing    SkipReason = "missing"
	SkipNoSignal   SkipReason = "no_signal"
	SkipBeforeOpen SkipReason = "before_open"
)

// SkipEvent records that, at a given time for a given symbol, no trading happened.
type SkipEvent struct {
	Time   time.Time
	Symbol string
	Reason SkipReason
	Detail string
}
