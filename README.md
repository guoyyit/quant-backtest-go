# quant-backtest-go

一套**本地可复现**的量化策略回测引擎：在历史行情上按时间顺序回放交易信号，
逐笔核算组合收益与风险指标，并支持确定性的并行参数扫描。

- 纯本地：行情来自内存或本地 CSV，不访问任何外部服务。
- 防前瞻：策略在时刻 `t` 只能看到截至 `t`（含）的数据；`t` 产生的订单**最早**在 `t` 之后的 Bar 成交。
- 真实成交语义：买卖价差、滑点、手续费全部计入成交价与现金；市价单、限价单（GTC/FOK/DAY）、撤单、过期。
- 严格风控：资金不足的委托在提交与成交两个环节都会被拒绝；未启用卖空时持仓与现金永不为负；卖空受额度约束。
- 异常行情：停牌、涨跌停、数据缺失均不产生成交，并记录**可区分的跳过原因**。
- 可复现：引擎无随机量；并行参数扫描的结果与 worker 数、调度顺序无关，逐项完全一致。

## 目录结构

| 包 | 职责 |
|---|---|
| `market` | Bar 数据结构、内存行情源、合并时间线、CSV 加载、可交易性/跳过原因 |
| `backtest` | 订单模型、组合逐笔核算、模拟券商（撮合/费用/风控）、回测引擎（防前瞻） |
| `strategy` | 示例策略：双均线交叉 `MACross` |
| `stats` | 净值序列、累计收益、年化、最大回撤、夏普、胜率、换手率 |
| `scan` | 参数网格笛卡尔积、并行扫描、确定性种子派生 |
| `cmd/demo` | 可运行示例：单次回测 + 9 组参数并行扫描 |

## 快速开始

```bash
go run ./cmd/demo
```

最小回测：

```go
feed := market.NewMemoryFeed()
feed.Add(market.Bar{Symbol: "AAA", Time: t, Open: 10, High: 10.2, Low: 9.8, Close: 10})
// ... 添加更多 Bar（无需有序，Sort 后回放）
feed.Sort()

cfg := backtest.Config{
    InitialCash: 1_000_000,
    Broker: backtest.BrokerConfig{
        Commission:      backtest.RateCommission{Rate: 0.0003, MinFee: 5},
        Spread:          0.001,   // 0.1% 相对价差
        Slippage:        0.0005,  // 0.05% 相对滑点
        MarketCashBuffer: 0.02,  // 市价买单预留 2% 跳空缓冲
    },
}
eng := backtest.NewEngine(feed, strategy.NewMACross("AAA", 5, 20, 100), cfg)
res, err := eng.Run()

m := stats.ComputeResult(res, stats.DefaultOptions())
fmt.Println(m.TotalReturn, m.MaxDrawdown, m.SharpeRatio, m.TurnoverRate)
```

## 回测时间模型（防前瞻）

引擎把多标的 Bar 合并为全局时间线，对每个时间点 `t` 严格按下列顺序处理：

1. **撮合**：只处理 `Submitted < t` 的挂单，用 `t` 这根 Bar 的价格撮合。
   在 `t` 的 `OnBar` 中提交的订单 `Submitted == t`，**绝不可能**在 `t` 成交，最早在 `t+1`。
2. **更新可见历史**：当前 Bar 此刻成为历史，写入 `ctx.History`；券商估值价更新为当根收盘价。
3. **盯市**：以当根收盘价把组合权益写入净值序列（当根成交立即反映）。
4. **策略决策**：调用 `OnBar(ctx, bar)`。`ctx.History` 只含截至当前（含）的 Bar，无任何未来数据。

策略通过 `backtest.Context` 下单与查询，无法直接访问行情源，因此无法窥探未来。
限价单是否成交由 `Bar.High/Low` 是否触及限价判断（不是只看收盘价）。

## 成交语义与订单生命周期

| 类型 | 成交时机 | 有效期 |
|---|---|---|
| 市价单 `Buy/Sell` | 提交后**下一根** Bar，以开盘价（或 Bid/Ask）加成本成交 | 一次性（OneShot）：下一根不可交易即过期 |
| 限价单 `GTC` | `Low<=限价`（买）/`High>=限价`（卖）后成交 | 一直挂单，直至成交 / 撤单 / 超过 `ExpiresAt` |
| 限价单 `FOK`/`FAK`/`DAY` | 条件满足则成交 | 仅下一根 Bar，条件不满足即过期 |

- **限价买入**成交价 `min(限价, 开盘买价)`；**限价卖出**成交价 `max(限价, 开盘卖价)`，保证不劣于限价。
- 撤单 `ctx.Cancel(id)` 立即释放预留现金 / 卖出额度。
- 委托状态：`new / working / filled / canceled / expired / rejected`。

### 撮合参考价与成本（费用模型）

- Bar 自带 `Bid/Ask` 时直接使用；否则以 Bar 参考价按相对价差 `Spread` 合成：
  - 买参考价 = `Open × (1 + Spread/2)`
  - 卖参考价 = `Open × (1 − Spread/2)`
- 滑点 `Slippage`：买入价再 `×(1+Slippage)`，卖出价再 `×(1−Slippage)`。
- 手续费 `CommissionModel`：
  - `FlatCommission{Fee}`：每笔固定；
  - `RateCommission{Rate, MinFee}`：`max(成交额×Rate, MinFee)`。

### 资金与持仓核算

- 买入：`现金 -= 成交额 + 手续费`；卖出：`现金 += 成交额 − 手续费`。
- 持仓按**移动加权平均成本**计价；开仓手续费摊入 `AvgCost`，平仓手续费直接减少已实现盈亏。
- 买入提交时按**最坏成本**（参考价 × (1+价差/2) × (1+滑点) × (1+`MarketCashBuffer`)）预留现金；
  卖出提交时校验“持仓 + 卖空额度 − 已预留”。
- 成交前一刻再次复核：若成交会使现金为负，则拒单（`insufficient_cash`），现金分文不动。
  `MarketCashBuffer` 用于覆盖提交与成交之间的隔夜跳空；缓冲不足时的跳空单会被安全拒绝。
- 策略可用 `ctx.MaxBuyQuantity(symbol)` 直接取得“按券商最坏成本可买的最大股数”，据此整手下单必通过资金校验。

### 卖空

`BrokerConfig.AllowShort=false`（默认）时：无持仓卖出 → `no_position`；会导致负持仓的卖出 →
`position_would_be_negative`。启用卖空后受额度约束：

- `MaxShortQty[symbol]`：单标的最大卖空数量（绝对值）；
- `DefaultShortQty`：未单独配置标的的默认额度；
- 超过额度 → `exceeds_short_limit`，持仓最多为 `-额度`。

## 异常行情与跳过原因

`Bar` 上可标记 `Suspended / LimitUp / LimitDown`；价格非法（≤0 或 High<Low）视为数据缺失。

| `SkipReason` | 触发条件 | 成交 |
|---|---|---|
| `suspended` | `Suspended=true`（停牌） | 否 |
| `limit_up` | 涨停且方向为**买** | 否（卖仍可成交） |
| `limit_down` | 跌停且方向为**卖** | 否（买仍可成交） |
| `missing_data` | 该时间点无 Bar，或价格非法 | 否 |

行为：GTC 限价单在被屏蔽时**继续挂单**并记录跳过；市价单 / FOK / DAY 单过期并记录跳过。
某标的在某时间点完全缺失 Bar 时，当天不触发其 `OnBar`，并产生 `missing_data` 跳过。
所有跳过可通过 `Result.Skips []SkipRecord`（含时间、标的、原因、关联订单 ID、明细）区分与审计。

## 参数扫描（并行且可复现）

```go
sets := scan.Grid(map[string][]float64{
    "fast": {3, 5, 10},
    "slow": {20, 30, 60},
})
results := scan.Run(ctx, sets, func(ps scan.ParamSet) (market.Feed, backtest.Strategy, backtest.Config) {
    return buildFeed(), strategy.NewMACross("AAA", int(ps["fast"]), int(ps["slow"]), 100), cfg
}, 4)
```

- `Grid` 的输出顺序确定：参数名按字典序排序，靠后的参数变化最快。
- `Run` 并行执行，但结果**按输入位置回填**；每组使用独立的 feed / strategy / portfolio，不共享可变状态。
- 引擎本身无随机量；相同输入在 `workers=1` 与 `workers=N` 下结果逐项一致（净值、成交、手续费、指标）。
- 策略若需要随机量，应使用由参数内容派生的固定种子：`seed := scan.Seed(ps)`（FNV-1a，与 map 迭代顺序无关）。

## 指标（`stats`）

全部由净值序列 `[]EquityPoint` 与逐笔成交 `[]Fill` 计算，可独立复算：

- 累计收益 = 期末权益/期初权益 − 1；年化收益按 `PeriodsPerYear`（默认 252）复利折算。
- 最大回撤 = 权益曲线峰谷最大跌幅。
- 夏普比率 = 逐期超额收益均值/标准差 × √PeriodsPerYear，无风险利率由 `Options.RiskFreeRate` 给出。
- 胜率 = 产生已实现盈亏的成交中盈利笔数占比。
- 换手率 = 期间累计成交额 / 平均权益。

## CSV 行情格式

```
time,open,high,low,close,volume,bid,ask,suspended,limit_up,limit_down
2024-01-01,10,11,9,10,1000000,,,false,false,false
```

`time` 支持 `RFC3339`、`2006-01-02 15:04:05`、`2006-01-02`；除 `time/open/high/low/close`
外的列均可省略。以 `#` 开头的行为注释。用 `market.LoadCSV(path, symbol)` 载入。

## 配置项一览（`backtest.BrokerConfig`）

| 字段 | 含义 | 默认 |
|---|---|---|
| `Commission` | 手续费模型 | `FlatCommission{}`（0 费用） |
| `Spread` | 相对价差，无 Bid/Ask 时合成 | 0 |
| `Slippage` | 相对滑点，买涨卖跌 | 0 |
| `MarketCashBuffer` | 市价买单预留资金的跳空缓冲比例 | 0 |
| `AllowShort` | 是否允许卖空 | false |
| `MaxShortQty` | 各标的最大卖空数量 | nil |
| `DefaultShortQty` | 默认卖空额度 | 0 |

## 测试

```bash
go test ./...        # 全部用例
go test -race ./...  # 含并行扫描的竞态检测
```

覆盖：前瞻偏差（订单只能在下一根成交、History 不含未来）、限价成交与撤单/过期、
资金不足与跳空拒单、未启用卖空时非负约束、卖空额度、参数扫描跨 worker 逐项可复现、
停牌/涨跌停/缺失行情的可区分跳过、指标与逐笔成交一致性、CSV 加载。
