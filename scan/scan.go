package scan

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"

	"github.com/guoyyit/quant-backtest-go/backtest"
	"github.com/guoyyit/quant-backtest-go/market"
)

// ParamSet 是一组命名参数取值。
type ParamSet map[string]float64

// Builder 依据参数组合构造一次回测。
// 为保证可复现：同一参数组合必须返回等价的输入（行情/配置），
// 策略如需随机量应使用以参数内容派生的固定种子（见 Seed）。
type Builder func(ps ParamSet) (market.Feed, backtest.Strategy, backtest.Config)

// ScanResult 保留输入参数与对应回测结果，按网格顺序返回。
type ScanResult struct {
	Params ParamSet
	Index  int
	Result *backtest.Result
	Err    error
}

// Grid 生成参数网格的笛卡尔积。输出顺序确定：参数名按字典序排序，
// 靠后的参数变化最快。
func Grid(params map[string][]float64) []ParamSet {
	names := make([]string, 0, len(params))
	for n := range params {
		names = append(names, n)
	}
	sort.Strings(names)

	sets := []ParamSet{{}}
	for _, n := range names {
		vals := append([]float64(nil), params[n]...)
		base := sets
		sets = make([]ParamSet, 0, len(base)*len(vals))
		for _, ps := range base {
			for _, v := range vals {
				np := make(ParamSet, len(ps)+1)
				for k, x := range ps {
					np[k] = x
				}
				np[n] = v
				sets = append(sets, np)
			}
		}
	}
	return sets
}

// Seed 依据参数组合内容派生一个确定性的 64 位种子（FNV-1a）。
// 相同参数（含取值与参数名集合）始终得到相同种子，与执行顺序/并行度无关。
func Seed(ps ParamSet) int64 {
	names := make([]string, 0, len(ps))
	for n := range ps {
		names = append(names, n)
	}
	sort.Strings(names)

	var h uint64 = 1469598103934665603
	mix := func(b byte) {
		h ^= uint64(b)
		h *= 1099511628211
	}
	for _, n := range names {
		for i := 0; i < len(n); i++ {
			mix(n[i])
		}
		mix('=')
		bits := math.Float64bits(ps[n])
		for i := 0; i < 8; i++ {
			mix(byte(bits >> (8 * i)))
		}
		mix(';')
	}
	return int64(h)
}

// Run 并行执行参数扫描。workers<=0 时取 GOMAXPROCS。
// 无论并行度与调度如何：
//   - 输出切片顺序与输入 sets 完全一致（按位置回填）；
//   - 每组使用独立的 feed/strategy/portfolio，互不共享可变状态；
//   - 回测引擎本身无随机量，配合 Builder 的固定输入，结果逐项完全一致。
//
// 取消 ctx 会让尚未派发的组合返回 ctx.Err()。
func Run(ctx context.Context, sets []ParamSet, build Builder, workers int) []ScanResult {
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers > len(sets) && len(sets) > 0 {
		workers = len(sets)
	}
	if workers <= 0 {
		workers = 1
	}

	results := make([]ScanResult, len(sets))
	type job struct{ i int }
	jobs := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				ps := sets[j.i]
				res := ScanResult{Params: cloneParams(ps), Index: j.i}
				func() {
					defer func() {
						if r := recover(); r != nil {
							res.Err = fmt.Errorf("param set %d panicked: %v", j.i, r)
						}
					}()
					feed, strat, cfg := build(ps)
					eng := backtest.NewEngine(feed, strat, cfg)
					r, err := eng.Run()
					res.Result = r
					res.Err = err
				}()
				results[j.i] = res
			}
		}()
	}

	for i := range sets {
		select {
		case <-ctx.Done():
			results[i] = ScanResult{Params: cloneParams(sets[i]), Index: i, Err: ctx.Err()}
		case jobs <- job{i}:
		}
	}
	close(jobs)
	wg.Wait()
	return results
}

func cloneParams(ps ParamSet) ParamSet {
	np := make(ParamSet, len(ps))
	for k, v := range ps {
		np[k] = v
	}
	return np
}
