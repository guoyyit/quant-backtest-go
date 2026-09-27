package marketdata

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Feed provides historical bars for one or many symbols in chronological order.
type Feed interface {
	// Symbols returns all symbols in the feed.
	Symbols() []string
	// Interval returns the bar interval.
	Interval() time.Duration
	// Timeline returns the sorted, de-duplicated union of all bar start times.
	Timeline() []time.Time
	// BarAt returns the bar for symbol at startTime and whether it exists.
	BarAt(symbol string, startTime time.Time) (Bar, bool)
}

// memoryFeed is the concrete Feed used for in-memory data and CSV files.
type memoryFeed struct {
	interval time.Duration
	symbols  []string
	bySymbol map[string]map[time.Time]Bar // symbol -> start time -> bar
	timeline []time.Time
}

// NewMemoryFeed builds a feed directly from bars (used by tests and examples).
// Bars are sorted and de-duplicated per symbol; identical (symbol,start) rows
// later in the slice overwrite earlier ones.
func NewMemoryFeed(interval time.Duration, bars []Bar) Feed {
	f := &memoryFeed{
		interval: interval,
		bySymbol: make(map[string]map[time.Time]Bar),
	}
	for _, b := range bars {
		b.Interval = interval
		f.add(b)
	}
	f.rebuild()
	return f
}

func (f *memoryFeed) add(b Bar) {
	m, ok := f.bySymbol[b.Symbol]
	if !ok {
		m = make(map[time.Time]Bar)
		f.bySymbol[b.Symbol] = m
	}
	m[b.StartTime] = b
}

func (f *memoryFeed) rebuild() {
	f.symbols = f.symbols[:0]
	seen := map[time.Time]struct{}{}
	var tl []time.Time
	for sym, m := range f.bySymbol {
		f.symbols = append(f.symbols, sym)
		for t := range m {
			if _, ok := seen[t]; !ok {
				seen[t] = struct{}{}
				tl = append(tl, t)
			}
		}
	}
	sort.Strings(f.symbols)
	sort.Slice(tl, func(i, j int) bool { return tl[i].Before(tl[j]) })
	f.timeline = tl
}

func (f *memoryFeed) Symbols() []string       { return append([]string(nil), f.symbols...) }
func (f *memoryFeed) Interval() time.Duration { return f.interval }
func (f *memoryFeed) Timeline() []time.Time   { return append([]time.Time(nil), f.timeline...) }

func (f *memoryFeed) BarAt(symbol string, t time.Time) (Bar, bool) {
	if m, ok := f.bySymbol[symbol]; ok {
		b, ok := m[t]
		return b, ok
	}
	return Bar{}, false
}

// Merge builds the sorted de-duplicated union timeline of several time slices.
func Merge(times ...[]time.Time) []time.Time {
	seen := map[time.Time]struct{}{}
	var out []time.Time
	for _, ts := range times {
		for _, t := range ts {
			if _, ok := seen[t]; !ok {
				seen[t] = struct{}{}
				out = append(out, t)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// CSVFeed is a Feed loaded from one CSV file. It is itself combinable through
// NewMultiFeed.
type CSVFeed struct {
	symbol string
	bars   []Bar
}

// LoadCSV parses bars from a reader for one symbol.
//
// The expected columns are: time,open,high,low,close,volume.
// An optional header row is detected when the first field is "time"/"date".
// With a header, extra boolean columns halted,limit_up,limit_down are honored
// (truthy: 1/true/yes). Time accepts RFC3339, "2006-01-02 15:04:05" or a
// plain date "2006-01-02".
func LoadCSV(symbol string, interval time.Duration, r io.Reader) (*CSVFeed, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true

	rows, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("marketdata: read csv: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("marketdata: csv for %q is empty", symbol)
	}

	col := map[string]int{
		"time": 0, "open": 1, "high": 2, "low": 3, "close": 4, "volume": 5,
	}
	start := 0
	if h := rows[0]; len(h) > 0 && isHeader(h[0]) {
		start = 1
		for i, name := range h {
			col[strings.ToLower(strings.TrimSpace(name))] = i
		}
	}

	var bars []Bar
	for rowIdx := start; rowIdx < len(rows); rowIdx++ {
		row := rows[rowIdx]
		get := func(key string) (string, bool) {
			i, ok := col[key]
			if !ok || i >= len(row) {
				return "", false
			}
			return strings.TrimSpace(row[i]), true
		}
		ts, ok := get("time")
		if !ok {
			ts, ok = get("date")
		}
		if !ok || ts == "" {
			return nil, fmt.Errorf("marketdata: row %d missing time", rowIdx+1)
		}
		t, err := parseTime(ts)
		if err != nil {
			return nil, fmt.Errorf("marketdata: row %d: %w", rowIdx+1, err)
		}
		o, err := parseFloat(row, col, "open", rowIdx)
		if err != nil {
			return nil, err
		}
		h, err := parseFloat(row, col, "high", rowIdx)
		if err != nil {
			return nil, err
		}
		l, err := parseFloat(row, col, "low", rowIdx)
		if err != nil {
			return nil, err
		}
		c, err := parseFloat(row, col, "close", rowIdx)
		if err != nil {
			return nil, err
		}
		v, err := parseFloat(row, col, "volume", rowIdx)
		if err != nil {
			return nil, err
		}
		b := Bar{
			Symbol:    symbol,
			StartTime: t,
			Interval:  interval,
			Open:      o,
			High:      h,
			Low:       l,
			Close:     c,
			Volume:    v,
		}
		if s, ok := get("halted"); ok {
			b.Halted = parseBool(s)
		}
		if s, ok := get("limit_up"); ok {
			b.LimitUp = parseBool(s)
		}
		if s, ok := get("limit_down"); ok {
			b.LimitDown = parseBool(s)
		}
		bars = append(bars, b)
	}

	sort.Slice(bars, func(i, j int) bool { return bars[i].StartTime.Before(bars[j].StartTime) })
	return &CSVFeed{symbol: symbol, bars: bars}, nil
}

// LoadCSVFile loads bars from a CSV file path for one symbol.
func LoadCSVFile(symbol, path string, interval time.Duration) (*CSVFeed, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("marketdata: open %s: %w", path, err)
	}
	defer f.Close()
	return LoadCSV(symbol, interval, f)
}

// NewMultiFeed combines per-symbol feeds into one Feed over a union timeline.
// A symbol absent at a timeline point simply yields no bar there (missing),
// which the engine records as a skip event.
func NewMultiFeed(feeds ...*CSVFeed) (Feed, error) {
	var bars []Bar
	known := map[string]bool{}
	for _, cf := range feeds {
		if cf == nil {
			continue
		}
		if known[cf.symbol] {
			return nil, fmt.Errorf("marketdata: duplicate symbol %q", cf.symbol)
		}
		known[cf.symbol] = true
		bars = append(bars, cf.bars...)
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("marketdata: no bars in any feed")
	}
	iv := bars[0].Interval
	return NewMemoryFeed(iv, bars), nil
}

func isHeader(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "time", "date", "timestamp", "datetime":
		return true
	}
	return false
}

func parseTime(s string) (time.Time, error) {
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable time %q", s)
}

func parseFloat(row []string, col map[string]int, key string, rowIdx int) (float64, error) {
	i, ok := col[key]
	if !ok || i >= len(row) {
		return 0, fmt.Errorf("marketdata: row %d missing column %q", rowIdx+1, key)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(row[i]), 64)
	if err != nil {
		return 0, fmt.Errorf("marketdata: row %d column %q: %w", rowIdx+1, key, err)
	}
	return v, nil
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y", "halted":
		return true
	}
	return false
}
