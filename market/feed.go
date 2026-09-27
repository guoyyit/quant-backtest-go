package market

import (
	"sort"
	"time"
)

// Feed 是历史行情只读数据源。所有实现必须保证：
//  1. 每个标的的 Bar 按时间升序排列；
//  2. 数据完全来自本地内存/文件，回测过程不访问任何外部服务。
type Feed interface {
	// Symbols 返回数据源中全部标的代码（顺序确定：字典序）。
	Symbols() []string
	// Bars 返回某标的的全部历史 Bar（升序）。
	Bars(symbol string) []Bar
}

// MemoryFeed 是内存行情源，用于测试与 CSV 载入后的回放。
type MemoryFeed struct {
	bars map[string][]Bar
}

func NewMemoryFeed() *MemoryFeed {
	return &MemoryFeed{bars: make(map[string][]Bar)}
}

// Add 追加一根 Bar；不保证有序，载入完成后由 Sort 统一排序。
func (f *MemoryFeed) Add(b Bar) {
	f.bars[b.Symbol] = append(f.bars[b.Symbol], b)
}

// Sort 对每个标的的 Bar 按时间升序排序。
func (f *MemoryFeed) Sort() {
	for sym := range f.bars {
		s := f.bars[sym]
		sort.Slice(s, func(i, j int) bool { return s[i].Time.Before(s[j].Time) })
		f.bars[sym] = s
	}
}

func (f *MemoryFeed) Symbols() []string {
	out := make([]string, 0, len(f.bars))
	for sym := range f.bars {
		out = append(out, sym)
	}
	sort.Strings(out)
	return out
}

func (f *MemoryFeed) Bars(symbol string) []Bar {
	return f.bars[symbol]
}

// Timeline 返回全部标的合并、去重并升序排列的时间点。
func (f *MemoryFeed) Timeline() []time.Time {
	seen := make(map[int64]time.Time)
	for _, list := range f.bars {
		for _, b := range list {
			seen[b.Time.UnixNano()] = b.Time
		}
	}
	out := make([]time.Time, 0, len(seen))
	for _, t := range seen {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}
