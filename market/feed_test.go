package market

import (
	"testing"
	"time"
)

func TestMemoryFeedSortedAndTimeline(t *testing.T) {
	f := NewMemoryFeed()
	f.Add(Bar{Symbol: "AAA", Time: time.Unix(200, 0), Open: 2, High: 2, Low: 2, Close: 2})
	f.Add(Bar{Symbol: "AAA", Time: time.Unix(100, 0), Open: 1, High: 1, Low: 1, Close: 1})
	f.Add(Bar{Symbol: "BBB", Time: time.Unix(150, 0), Open: 3, High: 3, Low: 3, Close: 3})
	f.Sort()

	if got := f.Symbols(); len(got) != 2 || got[0] != "AAA" || got[1] != "BBB" {
		t.Fatalf("symbols = %v", got)
	}
	a := f.Bars("AAA")
	if !a[0].Time.Equal(time.Unix(100, 0)) || !a[1].Time.Equal(time.Unix(200, 0)) {
		t.Fatalf("AAA not sorted: %v %v", a[0].Time, a[1].Time)
	}
	tl := f.Timeline()
	if len(tl) != 3 || tl[0].Unix() != 100 || tl[2].Unix() != 200 {
		t.Fatalf("timeline = %v", tl)
	}
}

func TestBarTradability(t *testing.T) {
	b := Bar{Open: 10, High: 11, Low: 9, Close: 10}
	if ok, _ := b.Tradable(); !ok {
		t.Error("normal bar should be tradable")
	}
	bad := Bar{Open: 0, High: 11, Low: 9, Close: 10}
	if ok, why := bad.Tradable(); ok || why != SkipMissing {
		t.Errorf("zero open: %v %s", ok, why)
	}
	susp := Bar{Open: 10, High: 11, Low: 9, Close: 10, Suspended: true}
	if ok, why := susp.SideTradable(true); ok || why != SkipSuspended {
		t.Errorf("suspended: %v %s", ok, why)
	}
	up := Bar{Open: 10, High: 11, Low: 9, Close: 10, LimitUp: true}
	if ok, why := up.SideTradable(true); ok || why != SkipLimitUp {
		t.Errorf("limit-up buy: %v %s", ok, why)
	}
	if ok, _ := up.SideTradable(false); !ok {
		t.Error("limit-up should still allow sells")
	}
	down := Bar{Open: 10, High: 11, Low: 9, Close: 10, LimitDown: true}
	if ok, why := down.SideTradable(false); ok || why != SkipLimitDown {
		t.Errorf("limit-down sell: %v %s", ok, why)
	}
}
