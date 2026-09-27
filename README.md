# quant-backtest-go

一套**本地可复现**的量化策略回测引擎。历史行情从内存或本地 CSV 读取，订单在内存中撮合，
**不依赖任何外部服务**。引擎按时间顺序回放行情驱动策略决策，逐笔核算现金、持仓、
已实现/未实现盈亏，并输出风险收益指标与支持参数扫描。

## 特性

1. **无前瞻（no look-ahead）**：策略在时间点 `t` 只能看到 `StartTime <= t` 的已收盘 bar；
   在 `t` 提交的订单最早在 **`t` 之后的下一根 bar** 才能成交。订单资金校验所用的参考价
   也严格限制在 `t` 之前的收盘价。
2. **市价单 / 限价单**：成交价计入买卖价差、滑点与手续费；限价单在条件未满足前保持挂单，
   支持撤单与 `GTC / DAY / GTD` 过期控制。
3. **逐笔核算**：现金、持仓、已实现盈亏、未实现盈亏逐笔更新；资金不足的委托被拒绝；
   现金不会为负；卖空默认禁止，显式启用后受每标的卖空额度约束。
4. **参数扫描**：多参数组合并行执行；固定随机种子 + 相同输入时，结果**逐项完全一致**，
   与 worker 数量、调度顺序无关。
5. **异常行情**：停牌、涨停、跌停、数据缺失均**不产生成交**，并记录可区分的跳过原因。
6. **指标输出**：净值序列、累计收益、年化收益(CAGR)、年化波动、夏普比率、最大回撤、换手率，
   且可由逐笔成交记录复算。
7. 覆盖前瞻偏差、限价成交与撤销、资金不足、扫描可复现、停牌与缺失行情的自动化测试。

## 目录结构

```
pkg/marketdata  Bar / Feed / CSV 解析 / 多标的时间轴合并 / 跳过原因
pkg/order       订单、成交、方向、有效期、拒绝原因类型
pkg/broker      撮合：价差/滑点/手续费、市价/限价、撤单/过期、资金与卖空约束
pkg/portfolio   现金、持仓、已实现/未实现盈亏
pkg/strategy    Strategy 接口、Context、确定性 RNG
pkg/engine      主事件循环（保证无前瞻）、结果汇总
pkg/metrics     净值与风险指标
pkg/scan        参数网格展开与确定性并行执行
examples/ma     均线交叉示例策略
cmd/backtest    命令行入口
```

## 快速开始

```bash
# 单次回测
go run ./cmd/backtest -csv testdata/sample.csv -symbol AAA -fast 5 -slow 20 \
  -cash 1000000 -equity-out equity.csv

# 参数扫描（并行，逗号分隔）
go run ./cmd/backtest -csv testdata/sample.csv -symbol AAA \
  -scan-fast 3,5,10 -scan-slow 15,20,30 -workers 4
```

## CSV 数据格式

基础列为 `time,open,high,low,close,volume`，首行可为表头。带表头时还支持三个布尔列：
`halted,limit_up,limit_down`（真值：`1/true/yes`）。时间支持 RFC3339、
`2006-01-02 15:04:05`、`2006-01-02`。

```csv
time,open,high,low,close,volume,halted,limit_up,limit_down
2024-01-02,10.0,10.2,9.9,10.1,10000,0,0,0
2024-01-03,10.1,11.1,10.1,11.1,10000,0,1,0   # 涨停，不成交
```

多标的时取各标的 bar 起始时间的**并集时间轴**；某标的在某时间点缺 bar 即记为 `missing`。

## 成交语义（重要）

### 时间与执行延迟

- 引擎遍历并集时间轴。在时间点 `t`，先撮合「更早提交」的挂单，再把**已收盘**的 bar
  交给策略 `OnBar`，最后按收盘价盯市。
- 订单在 `t` 提交后进入挂单表，**绝不**在 `t` 当根 bar 成交，最早在 `t+1` 成交。
  最后一根 bar 上提交的订单因此不会成交。
- `Context.History/CurrentBar` 只返回 `StartTime <= Now()` 的 bar。

### 市价单

- 以下一根 bar 的 **Open** 作为原始成交价（raw price）。
- 遇到不可成交 bar 时按下表处理：
  - `halted`（停牌）：买卖双向均不成交；
  - `limit_up`（涨停）：买入不成交，**卖出仍可成交**（封板时买不进、但可退出）；
  - `limit_down`（跌停）：卖出不成交，**买入仍可成交**；
  - `missing`（缺失）：该 bar 不成交。
  被阻挡的挂单继续挂起；市价单会等待第一根对其方向可成交的 bar。

### 限价单

- 买单：当 `bar.Low <= LimitPrice` 时成交，原始成交价为 `min(bar.Open, LimitPrice)`。
- 卖单：当 `bar.High >= LimitPrice` 时成交，原始成交价为 `max(bar.Open, LimitPrice)`。
- 条件未满足保持挂单。有效期：
  - `GTC`：撤销前一直有效（仍以回测结束为界）；
  - `DAY`：在其**首个被评估的 bar**（含停牌/缺失）结束时失效；
  - `GTD`：`t > Expiry` 时失效（`t == Expiry` 仍有效）。
- `Cancel(id)` 可撤销工作中的挂单。

> 注意：限价约束的是「原始成交价」，在此之上再叠加价差与滑点，因此买入的**含费实际价**
> 可能比限价高出价差+滑点。若希望把成本也压在限价内，可在策略侧相应下调限价。

### 不可成交 bar 与跳过原因

| 原因 (`SkipReason`) | 含义 | 是否成交 |
|---|---|---|
| `halted`     | 停牌整根不可交易 | 买、卖均否 |
| `limit_up`   | 封死涨停 | 买入否，卖出可 |
| `limit_down` | 封死跌停 | 卖出否，买入可 |
| `missing`    | 时间轴上该标的缺 bar（数据缺失） | 否 |

`Result.Skips` 记录每个 `(时间, 标的, 原因)`。挂单在缺失/停牌 bar 上不成交；`DAY` 单
在该 bar 消耗其有效期，`GTC/GTD` 单继续挂起（`GTD` 仍按时间判过期）。

## 费用模型

设原始价为 `raw`，配置项为费率（小数，如 `0.001 = 10bp`）：

```
买入实际价 = raw * (1 + HalfSpread + SlippageRate)
卖出实际价 = raw * (1 - HalfSpread - SlippageRate)
成交金额   = 实际价 * 数量
手续费     = max(CommissionRate * 成交金额, MinCommission)
```

- `HalfSpread`：买卖价差的一半。买吃卖一价、卖吃买一价。
- `SlippageRate`：额外不利滑点（买入更高、卖出更低）。
- `CommissionRate`：按成交金额比例收取；`MinCommission` 为每笔最低佣金。
- 手续费同时减少现金并计入已实现盈亏（视为已实现亏损）。

## 资金与卖空约束

- 买单在**提交时**用「不晚于当前的最近收盘价」（限价单用限价作为最坏情形）估算所需现金，
  现金不足直接拒绝（`insufficient_cash`）；成交前再做一次校验，同一根 bar 多单按顺序
  撮合导致现金耗尽时，后续单在成交时被拒，现金恒不为负。
- 卖单数量超过多头持仓、且未标记 `ShortSale` 时拒绝（`no_position`）。
- 卖空需 `AllowShort=true` 且订单标记 `ShortSale`，否则拒绝（`short_not_allowed`）；
  超过 `MaxShortQty`（每标的绝对空头数量上限，0 表示不限）拒绝（`short_quota_exceeded`）。
- 合法空头表现为负持仓（如 `-5`），受到额度约束；多头账户不会无故出现负持仓。

所有拒绝记录在 `Result.Rejections`，含 `RejectReason` 与明细。

## 配置项 (`engine.Config` / `broker.Config`)

| 配置 | 含义 | 默认 |
|---|---|---|
| `StartingCash`   | 初始现金 | 1,000,000 |
| `Seed`           | 策略 RNG 固定种子 | 0 |
| `PeriodsPerYear` | 夏普/波动年化周期数（日线 252） | 252 |
| `StartTime/EndTime` | 回放时间窗（零值表示不限） | 不限 |
| `Broker.CommissionRate` | 手续费率（按成交金额） | 0 |
| `Broker.MinCommission`  | 每笔最低佣金 | 0 |
| `Broker.HalfSpread`     | 半价差（小数） | 0 |
| `Broker.SlippageRate`   | 滑点率（小数） | 0 |
| `Broker.AllowShort`     | 是否允许显式卖空 | false |
| `Broker.MaxShortQty`    | 每标的最大空头数量（0=不限） | 0 |

## 指标复算口径

- 净值曲线首点为开仓前的初始现金，其后每根 bar 收盘盯市一点
  （停牌/涨跌停 bar 仍按最近收盘价估值）。
- 累计收益 = `FinalEquity / StartEquity - 1`。
- 最大回撤：曲线峰值到后续谷值的最大相对跌幅（总体法）。
- 夏普：逐 bar 简单收益的均值 / 样本标准差 × √PeriodsPerYear（无风险利率取 0）。
- 年化波动：逐 bar 收益总体标准差 × √PeriodsPerYear。
- CAGR：首末**带时间戳**观测点之间的几何年化（365.25 天/年）。
- 换手率：`Σ(每笔成交金额) / 时间加权平均净值`（除以 2 可得单边口径）。

## 可复现性 / 参数扫描

- `scan.Expand(Grid)` 按参数名排序展开网格，输出组合顺序确定。
- `scan.Run(..., Deterministic:true)` 为每个组合由「基础种子 + 组合参数」派生稳定种子
  （哈希与调度无关），且每个组合使用**独立的 feed / engine / strategy / RNG**，并行互不共享
  可变状态；返回结果严格按输入组合顺序排列。
- 因此固定种子与相同输入下，无论 worker=1 还是 N，结果逐项完全一致。
- 策略需要随机数时使用 `ctx.RNG()`（由种子驱动），不要自行用全局随机源。

## 运行测试

```bash
go test ./...
go test -race ./pkg/scan/ ./pkg/engine/   # 并行竞态检测
```
