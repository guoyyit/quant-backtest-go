package scan

import (
	"context"
	"testing"

	"github.com/guoyyit/quant-backtest-go/backtest"
	"github.com/guoyyit/quant-backtest-go/market"
)

// 构造一个确定性的小幅上涨行情（fast 上穿 slow 时会买入）。
func buildFeed() *market.MemoryFeed {
	f := market.NewMemoryFeed()
	for i := 0; i < 40; i++ {
		c := 10.0 + float64(i)*0.25
		f.Add(market.Bar{Symbol: "AAA", Time: t0plus(i),
			Open: c - 0.1, High: c + 0.2, Low: c - 0.2, Close: c, Volume: 1000})
	}
	return f
}

// Builder：每个参数组合得到等价行情与独立策略实例，引擎内无随机量。
func builder(ps ParamSet) (market.Feed, backtest.Strategy, backtest.Config) {
	_ = Seed(ps) // 演示：需要随机时使用确定性种子
	return buildFeed(),
		strat{fast: int(ps["fast"]), slow: int(ps["slow"])},
		backtest.Config{
			InitialCash: 100000,
			Broker:      backtest.BrokerConfig{Commission: backtest.RateCommission{Rate: 0.001}},
		}
}

// strat 是本测试包内的最小策略：fast 根 Bar 后持有 100 股直到 slow 根后卖出，
// 仅用历史收盘价决策，无随机性。
type strat struct{ fast, slow int }

func (s strat) OnBar(ctx *backtest.Context, b market.Bar) {
	pos := ctx.Position("AAA")
	h := ctx.History("AAA")
	if len(h) == s.fast && pos.Quantity == 0 {
		ctx.Buy("AAA", 100)
	}
	if len(h) == s.slow && pos.Quantity > 0 {
		ctx.Sell("AAA", pos.Quantity)
	}
}
func (s strat) Finish(ctx *backtest.Context) {}

func TestGridOrderDeterministic(t *testing.T) {
	g := Grid(map[string][]float64{"fast": {5, 10}, "slow": {20, 30}})
	if len(g) != 4 {
		t.Fatalf("grid size = %d, want 4", len(g))
	}
	// 参数名字典序 fast<slow，slow 变化最快。
	want := []ParamSet{
		{"fast": 5, "slow": 20},
		{"fast": 5, "slow": 30},
		{"fast": 10, "slow": 20},
		{"fast": 10, "slow": 30},
	}
	for i := range want {
		if g[i]["fast"] != want[i]["fast"] || g[i]["slow"] != want[i]["slow"] {
			t.Errorf("grid[%d] = %v, want %v", i, g[i], want[i])
		}
	}
}

func TestScanReproducibleAcrossWorkers(t *testing.T) {
	sets := Grid(map[string][]float64{
		"fast": {3, 5, 8},
		"slow": {10, 15, 20, 25},
	})

	one := Run(context.Background(), sets, builder, 1)
	many := Run(context.Background(), sets, builder, 8)

	if len(one) != len(many) || len(one) != len(sets) {
		t.Fatalf("result lengths: %d / %d / %d", len(one), len(many), len(sets))
	}
	for i := range sets {
		if one[i].Err != nil || many[i].Err != nil {
			t.Fatalf("set %d unexpected err: %v / %v", i, one[i].Err, many[i].Err)
		}
		if one[i].Index != i || many[i].Index != i {
			t.Errorf("set %d index mismatch: %d / %d", i, one[i].Index, many[i].Index)
		}
		r1, r2 := one[i].Result, many[i].Result
		if r1.FinalValue != r2.FinalValue {
			t.Errorf("set %d final value %.10f != %.10f", i, r1.FinalValue, r2.FinalValue)
		}
		if len(r1.Fills) != len(r2.Fills) {
			t.Fatalf("set %d fill count %d != %d", i, len(r1.Fills), len(r2.Fills))
		}
		for k := range r1.Fills {
			f1, f2 := r1.Fills[k], r2.Fills[k]
			if f1 != f2 {
				t.Errorf("set %d fill %d mismatch:\n%+v\n%+v", i, k, f1, f2)
			}
		}
		if len(r1.Equity) != len(r2.Equity) {
			t.Fatalf("set %d equity length mismatch", i)
		}
		for k := range r1.Equity {
			if r1.Equity[k] != r2.Equity[k] {
				t.Errorf("set %d equity point %d mismatch: %+v vs %+v", i, k, r1.Equity[k], r2.Equity[k])
			}
		}
	}
}

// 同一参数集的 Seed 必须稳定，参数相同（不同 map）种子一致。
func TestSeedDeterministic(t *testing.T) {
	a := Seed(ParamSet{"fast": 5, "slow": 20})
	b := Seed(ParamSet{"slow": 20, "fast": 5})
	if a != b {
		t.Errorf("seed depends on map iteration order: %d != %d", a, b)
	}
	c := Seed(ParamSet{"fast": 5, "slow": 21})
	if a == c {
		t.Error("different params produced same seed")
	}
}
