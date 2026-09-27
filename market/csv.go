package market

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// LoadCSV 从本地 CSV 文件加载某标的的历史 Bar。
// 期望表头（顺序不限，除 time,open,high,low,close 外均可缺省）：
//
//	time,open,high,low,close,volume,bid,ask,suspended,limit_up,limit_down
//
// time 支持 RFC3339 或 "2006-01-02"。以 # 开头的行被忽略。
// 缺失或无法解析的行情行会被跳过（不载入），由回测侧在时间线上表现为数据缺失。
func LoadCSV(path, symbol string) ([]Bar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.Comment = '#'
	r.TrimLeadingSpace = true

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, col := range []string{"time", "open", "high", "low", "close"} {
		if _, ok := idx[col]; !ok {
			return nil, fmt.Errorf("csv %s: missing required column %q", path, col)
		}
	}

	get := func(rec []string, col string) (string, bool) {
		i, ok := idx[col]
		if !ok || i >= len(rec) {
			return "", false
		}
		return strings.TrimSpace(rec[i]), true
	}
	parseFloat := func(rec []string, col string, dflt float64) float64 {
		v, ok := get(rec, col)
		if !ok || v == "" {
			return dflt
		}
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return dflt
		}
		return n
	}
	parseBool := func(rec []string, col string) bool {
		v, ok := get(rec, col)
		if !ok {
			return false
		}
		b, _ := strconv.ParseBool(v)
		return b
	}

	var out []Bar
	line := 1
	for {
		rec, err := r.Read()
		line++
		if err != nil {
			break
		}
		tv, _ := get(rec, "time")
		t, perr := parseTime(tv)
		if perr != nil {
			return nil, fmt.Errorf("csv %s line %d: %w", path, line, perr)
		}
		b := Bar{
			Symbol:    symbol,
			Time:      t,
			Open:      parseFloat(rec, "open", 0),
			High:      parseFloat(rec, "high", 0),
			Low:       parseFloat(rec, "low", 0),
			Close:     parseFloat(rec, "close", 0),
			Volume:    parseFloat(rec, "volume", 0),
			Bid:       parseFloat(rec, "bid", 0),
			Ask:       parseFloat(rec, "ask", 0),
			Suspended: parseBool(rec, "suspended"),
			LimitUp:   parseBool(rec, "limit_up"),
			LimitDown: parseBool(rec, "limit_down"),
		}
		out = append(out, b)
	}
	return out, nil
}

func parseTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q", v)
}
