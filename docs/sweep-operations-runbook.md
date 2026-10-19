# 归集运维 Runbook

自动归集仍使用持久化 `sweep_jobs`；规划器只对符合资格的任务分组并记录元数据。执行仍要经过账本 hold、出站交易、签名器、费用/资源与对账门禁。

<a id="planning-rules"></a>
## 规划规则

- 只对具有相同商户、链、资产/代币、储备钱包与来源策略键的任务进行批处理。
- 分配批次之前先确认链能力。Bitcoin 可以按批次规划；EVM、Solana 与 TRON 默认逐笔执行，除非操作员启用了链特定批处理实现。
- 不要批处理 pending、succeeded 或活动 processing 任务。批次元数据只分配给 pending 或 failed 任务。
- 规划之前遵守策略阈值：最小归集金额、dust 金额、最大费用估算与代币归集所需的最小原生 gas 余额。

<a id="gas-funding"></a>
## Gas 资金准备

需要原生 gas 的代币归集使用 `sweep_jobs` 上关联的父状态：

- `prefund_last_attempt_at` 在 `SWEEP_PREFUND_RETRY_AFTER` 内门禁重复的资金广播。
- `prefund_attempts` 与 `prefund_max_attempts` 强制执行重试上限。
- 当 prefund 尝试达到上限时，`operator_action` 变为 `review_gas_funding`。
- 资金准备与后续归集仍受同一钱包/链认领策略约束，因此同一钱包不会被竞争 worker 同时充值并归集。

<a id="operator-recovery"></a>
## 操作员恢复

广播不确定的任务不得盲目重试。它们会带着 `operator_action=reconcile_broadcast` 与范围化对账任务进入死信。操作员必须检查链上结果，然后选择一种恢复动作：

- `retry`：链上证据显示广播未落地；任务以 `next_run_at` 返回 failed。
- `mark_success`：链上证据确认归集或替换交易；任务记录 tx hash 并成功。
- `preserve_hold`：结果仍不确定；保留账本 hold 与死信状态。
- `release_hold`：证据证明未发生花费，可通过账本恢复路径释放账本 hold。

<a id="verification"></a>
## 校验

运行：

- `go test -p=1 -count=1 ./services/sweeps ./repositories`
- `go test -p=1 -count=1 ./services/database ./services/dbmigrations`

生产就绪仍要求 `GATEWAY_DB_MIGRATION_VERSION` 与 `dbmigrations.LatestID()` 一致，并通过 `services/database.VerifySchema`。
