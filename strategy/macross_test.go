package strategy

import (
	"testing"
	"time"

	"github.com/guoyyit/quant-backtest-go/backtest"
	"github.com/guoyyit/quant-backtest-go/market"
	"github.com/guoyyit/quant-backtest-go/stats"
)

func TestMACrossEndToEnd(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	f := market.NewMemoryFeed()
	// 先跌后涨再跌，制造金叉与死叉。
	closes := []float64{
		20, 19.5, 19, 18.5, 18, 18, 18.5, 19, 19.5, 20,
		20.5, 21, 21.5, 22, 22.5, 23, 23, 22, 21, 20,
	}
	for i, c := range closes {
		f.Add(market.Bar{Symbol: "AAA", Time: base.AddDate(0, 0, i),
			Open: c, High: c + 0.3, Low: c - 0.3, Close: c, Volume: 10000})
	}

	cfg := backtest.Config{
		InitialCash: 100000,
		Broker: backtest.BrokerConfig{
			Commission: backtest.RateCommission{Rate: 0.001, MinFee: 5},
			Spread:     0.002,
			Slippage:   0.0005,
		},
	}
	eng := backtest.NewEngine(f, NewMACross("AAA", 3, 6, 100), cfg)
	res, err := eng.Run()
	if err != nil {
		t.Fatal(err)
	}

	// 1) 成交记录逐笔一致：每笔现金与持仓都合法。
	cash := cfg.InitialCash
	holdQty := 0.0
	for _, fl := range res.Fills {
		if fl.Side == backtest.Buy {
			cash -= fl.Price*fl.Quantity + fl.Commission
			holdQty += fl.Quantity
		} else {
			cash += fl.Price*fl.Quantity - fl.Commission
			holdQty -= fl.Quantity
		}
		if cash < -1e-9 {
			t.Errorf("cash went negative after fill %+v: %.6f", fl, cash)
		}
		if holdQty < -1e-9 {
			t.Errorf("position went negative after fill %+v: %.6f", fl, holdQty)
		}
		if fl.Quantity != float64(int64(fl.Quantity/100)*100) {
			t.Errorf("fill qty %v not round lot", fl.Quantity)
		}
	}
	if holdQty < -1e-9 {
		t.Errorf("ending qty negative: %v", holdQty)
	}

	// 2) 最后一期权益与引擎 FinalValue 一致。
	last := res.Equity[len(res.Equity)-1]
	if d := last.Equity - res.FinalValue; d > 1e-9 || d < -1e-9 {
		t.Errorf("final equity %.6f != FinalValue %.6f", last.Equity, res.FinalValue)
	}

	// 3) 指标可由结果复算且非 NaN。
	m := stats.ComputeResult(res, stats.DefaultOptions())
	if m.NumPeriods != len(res.Equity) {
		t.Errorf("num periods %d != equity points %d", m.NumPeriods, len(res.Equity))
	}
	if m.MaxDrawdown < 0 || m.MaxDrawdown > 1 {
		t.Errorf("max drawdown out of range: %.4f", m.MaxDrawdown)
	}
	if m.TurnoverRate < 0 {
		t.Errorf("turnover negative: %.4f", m.TurnoverRate)
	}

	// 4) 同输入再跑一次，逐项完全一致（可复现）。
	eng2 := backtest.NewEngine(f, NewMACross("AAA", 3, 6, 100), cfg)
	res2, _ := eng2.Run()
	if res2.FinalValue != res.FinalValue || len(res2.Fills) != len(res.Fills) {
		t.Fatal("rerun produced different results")
	}
	for i := range res.Fills {
		if res.Fills[i] != res2.Fills[i] {
			t.Errorf("fill %d differs on rerun:\n%+v\n%+v", i, res.Fills[i], res2.Fills[i])
		}
	}
}
