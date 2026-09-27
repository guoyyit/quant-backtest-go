package marketdata_test

import (
	"strings"
	"testing"
	"time"

	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
)

func TestLoadCSVBasic(t *testing.T) {
	csvData := "time,open,high,low,close,volume\n" +
		"2024-01-01,10,11,9,10.5,1000\n" +
		"2024-01-02,10.5,12,10.4,11.5,2000\n"
	f, err := marketdata.LoadCSV("X", 24*time.Hour, strings.NewReader(csvData))
	if err != nil {
		t.Fatal(err)
	}
	mf, err := marketdata.NewMultiFeed(f)
	if err != nil {
		t.Fatal(err)
	}
	tl := mf.Timeline()
	if len(tl) != 2 {
		t.Fatalf("timeline len=%d want 2", len(tl))
	}
	b0, ok := mf.BarAt("X", tl[0])
	if !ok || b0.Close != 10.5 || b0.Volume != 1000 {
		t.Fatalf("bad bar0: %+v", b0)
	}
	if b0.Halted || b0.LimitUp || b0.LimitDown {
		t.Fatal("bar0 flags should be false")
	}
}

func TestLoadCSVFlagsAndNoHeader(t *testing.T) {
	csvData := "2024-01-01T00:00:00Z,10,11,9,10,5,1,0,0\n" +
		"2024-01-02T00:00:00Z,10,11,9,10,5,0,1,0\n" +
		"2024-01-03T00:00:00Z,10,11,9,10,5,0,0,1\n"
	// Without a header the extra flag columns are ignored (only 6 read).
	f, err := marketdata.LoadCSV("Y", 24*time.Hour, strings.NewReader(csvData))
	if err != nil {
		t.Fatal(err)
	}
	mf, _ := marketdata.NewMultiFeed(f)
	if _, ok := mf.BarAt("Y", mf.Timeline()[0]); !ok {
		t.Fatal("expected bar")
	}
}

func TestLoadCSVHeaderWithFlags(t *testing.T) {
	csvData := "time,open,high,low,close,volume,halted,limit_up,limit_down\n" +
		"2024-01-01,10,11,9,10,5,1,0,0\n" +
		"2024-01-02,10,11,9,10,5,0,1,0\n" +
		"2024-01-03,10,11,9,10,5,0,0,1\n"
	f, err := marketdata.LoadCSV("Z", 24*time.Hour, strings.NewReader(csvData))
	if err != nil {
		t.Fatal(err)
	}
	mf, _ := marketdata.NewMultiFeed(f)
	tl := mf.Timeline()
	b0, _ := mf.BarAt("Z", tl[0])
	b1, _ := mf.BarAt("Z", tl[1])
	b2, _ := mf.BarAt("Z", tl[2])
	if !b0.Halted {
		t.Fatal("want halted bar0")
	}
	if !b1.LimitUp {
		t.Fatal("want limit_up bar1")
	}
	if !b2.LimitDown {
		t.Fatal("want limit_down bar2")
	}
}

func TestMultiFeedUnionTimelineAndMissing(t *testing.T) {
	d := 24 * time.Hour
	a := "2024-01-01,1,1,1,1,1\n2024-01-02,1,1,1,1,1\n"
	b := "2024-01-02,2,2,2,2,2\n2024-01-03,2,2,2,2,2\n"
	fa, _ := marketdata.LoadCSV("A", d, strings.NewReader(a))
	fb, _ := marketdata.LoadCSV("B", d, strings.NewReader(b))
	mf, err := marketdata.NewMultiFeed(fa, fb)
	if err != nil {
		t.Fatal(err)
	}
	if len(mf.Timeline()) != 3 {
		t.Fatalf("union timeline=%d want 3", len(mf.Timeline()))
	}
	// B missing on day1, A missing on day3.
	if _, ok := mf.BarAt("B", mf.Timeline()[0]); ok {
		t.Fatal("B should be missing day1")
	}
	if _, ok := mf.BarAt("A", mf.Timeline()[2]); ok {
		t.Fatal("A should be missing day3")
	}
}
