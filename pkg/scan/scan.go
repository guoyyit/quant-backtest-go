package scan

import (
	"context"
	"math"
	"runtime"
	"sort"
	"sync"

	"github.com/guoyyit/quant-backtest-go/pkg/engine"
	"github.com/guoyyit/quant-backtest-go/pkg/marketdata"
	"github.com/guoyyit/quant-backtest-go/pkg/strategy"
)

// Grid maps each parameter name to the values it may take.
type Grid map[string][]float64

// Row is one parameter combination and its run result.
type Row struct {
	Params strategy.Params
	Result *engine.Result
	Err    error
}

// Options controls parallel execution.
type Options struct {
	Workers int
	// Deterministic forces a per-combo fixed seed and stable result ordering.
	Deterministic bool
}

// Expand converts a grid into every parameter combination. Keys are iterated
// in sorted order and each unique combination is emitted once.
func Expand(g Grid) []strategy.Params {
	keys := make([]string, 0, len(g))
	for k := range g {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	combos := []strategy.Params{{}}
	for _, k := range keys {
		values := g[k]
		var next []strategy.Params
		for _, base := range combos {
			for _, v := range values {
				p := make(strategy.Params, len(base)+1)
				for bk, bv := range base {
					p[bk] = bv
				}
				p[k] = v
				next = append(next, p)
			}
		}
		combos = next
	}
	return combos
}

// comboSeed derives a stable seed from the base seed and the combination.
// The same base seed + same params always map to the same derived seed,
// independent of scheduling or worker count.
func comboSeed(base int64, p strategy.Params) int64 {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := base
	const prime = int64(1000003)
	for _, k := range keys {
		h = hashString(h, k)
		h = h*prime + int64(int64FromFloat(p[k]))
	}
	return h
}

func hashString(h int64, s string) int64 {
	const prime = int64(31)
	for i := 0; i < len(s); i++ {
		h = h*prime + int64(s[i])
	}
	return h
}

// int64FromFloat uses the IEEE-754 bit pattern for stable hashing.
func int64FromFloat(f float64) int64 {
	return int64(math.Float64bits(f))
}

// Run executes every combination. With Deterministic=true and equal inputs,
// each run is fully isolated (its own engine/feed-derived strategy and RNG
// seed) and the returned slice is in input-combo order, so results are
// identical run-to-run regardless of worker count.
func Run(ctx context.Context, combos []strategy.Params, factory strategy.Factory, feed FeedFactory, base engine.Config, opts Options) []Row {
	rows := make([]Row, len(combos))

	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > len(combos) {
		workers = len(combos)
	}
	if workers < 1 {
		workers = 1
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if err := ctx.Err(); err != nil {
					rows[idx] = Row{Params: combos[idx], Err: err}
					continue
				}
				p := combos[idx]
				cfg := base
				if opts.Deterministic {
					cfg.Seed = comboSeed(base.Seed, p)
				}
				f, err := feed()
				if err != nil {
					rows[idx] = Row{Params: p, Err: err}
					continue
				}
				eng := engine.New(cfg, f)
				s := factory(p)
				res, err := eng.RunWithParams(s, p)
				rows[idx] = Row{Params: p, Result: res, Err: err}
			}
		}()
	}
	for i := range combos {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return rows
}

// FeedFactory constructs an independent feed per run so concurrent runs never
// share mutable state.
type FeedFactory func() (marketdata.Feed, error)
