# 资金链路可观测性 Runbook

本 runbook 定义受控生产运行的基线信号。它不是完整的交易所级 NOC 流程。

<a id="metrics"></a>
## 指标

`GET /metrics` 暴露低基数 Prometheus gauge：

- `gateway_money_event_outbox_backlog`，按 status。
- `gateway_webhook_delivery_backlog`，按 status。
- `gateway_sweep_job_backlog`，按 status。
- `gateway_withdrawal_backlog`，按 status。
- `gateway_refund_backlog`，按 status。
- `gateway_reconciliation_jobs`，按未解决 status。
- `gateway_chain_last_processed_block`、`gateway_chain_last_confirmed_block` 与 `gateway_chain_state_age_seconds`。
- `gateway_provider_health`、`gateway_provider_latest_height`、`gateway_provider_lag_blocks`、`gateway_provider_response_latency_ms`、`gateway_provider_consecutive_failures` 与 `gateway_provider_failover_decision`。
- `gateway_wallet_address_lookup_rows`，按 chain。
- `gateway_migration_strategy_ready` 与 `gateway_production_signer_ready`。

指标标签必须保持低基数且安全。Provider 标签已脱敏；凭据、密钥材料、原始签名与 payload 体不得出现在指标或日志中。

<a id="baseline-alerts"></a>
## 基线告警

Prometheus 告警规则位于 `deploy/prometheus/gateway-alerts.yaml`。

受控 Beta 告警规则：

- Provider 健康：任一生产链 5 分钟内没有健康且被选中的 provider 时告警。
- 链延迟：链状态新鲜度超过 10 分钟，或 provider 延迟超过配置的 stale 阈值持续 5 分钟时告警。
- Webhook：出现任何 `dead_letter` 投递，或失败投递持续增长 10 分钟时告警。
- 资金 outbox：`pending` 或 `processing` 积压持续增长 10 分钟时告警。
- 提现/退款：`processing` 行停留超过一个终局性窗口时告警。
- 出站交易：广播权由一个短事务（`AcquireSendGrant`）授予，该事务在锁内重新校验该行及其提现/退款，因此竞争失败的 worker 不持有任何链资源，也永远无法释放胜出者的资源；广播结果未知（`broadcast_attempted`）的出站行会被转入 `needs_operator_action` 且类别为 `broadcast_recovery_required`，绝不能自动重发。若 hash 只在此类行被移入操作员队列之后才变得已知，只能通过把它绑定到恰好那次已授权尝试（相同 `broadcast_attempted_at`、空 hash）来回填——绝不能作为操作员提供的 hash；在任何替换之前应依据该证据解决该行。
- 归集任务：出现任何 `dead_letter` 任务，或反复出现 `broadcast_uncertain` 失败类别时告警。
- 对账：出现任何 `needs_operator_action`，或未解决漂移超过 30 分钟时告警。
- 签名器：生产环境 `gateway_production_signer_ready` 为 `0` 时告警。
- 迁移：生产环境 `gateway_migration_strategy_ready` 为 `0` 时告警。

在首批试点量级已知、进入公开真实资金生产之前，必须收紧阈值。

<a id="logs-and-traces"></a>
## 日志与追踪

生产日志通过运维 logger 输出结构化 JSON。每个 HTTP 请求都会获得 `X-Request-ID`；若存在 W3C `traceparent` 头，会作为 `trace_id` 传播到请求 context、响应头与请求日志中。

操作员应按以下键检索：

- `request_id`：API 请求、活动日志与即时处理器诊断。
- `trace_id`：安装 OpenTelemetry provider/exporter 后的跨服务追踪。
- 资金资源 ID，例如支付 ID、Webhook 投递 ID、归集任务 ID、出站交易 ID 与对账任务 ID。

日志不得包含凭据、密钥材料、原始签名、原始交易 payload 或请求体。敏感日志属性会在写入前脱敏。

<a id="dashboard-panels"></a>
## 看板面板

基线看板面板记录在 `docs/money-path-dashboard.md`。最低限度的生产看板应包含：

- 按链的 provider 健康与链状态新鲜度。
- Webhook 积压与死信数量。
- 签名器就绪与适配器健康。
- 归集任务死信与操作员动作。
- 按未解决状态的对账任务。
- 迁移与指标访问就绪。

<a id="diagnostic-flow"></a>
## 诊断流程

<a id="chain-lag-or-provider-outage"></a>
### 链延迟或 Provider 故障

- 检查 `gateway_provider_health` 与所选 provider 标签。
- 确认 `PROVIDER_FAILOVER_STRATEGY`。
- 按 request id 与 chain id 查看链监听器日志。
- 若 provider 的 head 不一致，在该链的规范 head 明确之前保持提现暂停。

<a id="webhook-backlog-or-dead-letter"></a>
### Webhook 积压或死信

- 检查 `webhook_deliveries` 的 status 与失败类别。
- 校验商户端点 URL、TLS 与签名校验。
- 仅在商户确认已幂等处理事件之后重放。

<a id="signer-readiness-failure"></a>
### 签名器就绪失败

- 检查 `gateway_production_signer_ready` 与 `gateway_signer_adapter_ready`。
- 生产环境中，软件签名器模式不满足发布就绪要求。
- 在恢复出站广播之前确认外部签名器适配器健康。

<a id="sweep-dead-letter-or-broadcast-uncertain"></a>
### 归集死信或广播不确定

- 检查 `sweep_jobs` 行、`operator_action`、`failure_category` 与关联的对账任务。
- 对广播不确定的行，在重试或替换之前检查链上结果。
- 在链上证据证明未发生花费之前，保留账本 hold。

<a id="stuck-withdrawal-or-refund"></a>
### 卡住的提现或退款

- 确认广播之前已存在账本 hold。
- 检查签名器就绪与广播/终局性时间戳。
- 若广播结果不确定，在重试之前开启或更新对账。

<a id="reconciliation-drift"></a>
### 对账漂移

- 查看未解决的 `reconciliation_jobs`。
- 不要以破坏性方式编辑账本历史。
- 使用补偿分录或范围化对账解决说明。

<a id="emergency-freeze"></a>
### 紧急冻结

- 为受影响的商户/域/链启用出站策略冻结。
- 保持接单/Webhook 可观测，但停止新的出站广播。
- 在活动日志中记录操作员、原因、受影响范围与 correlation id。

<a id="backup-and-restore-drill"></a>
### 备份与恢复演练

- 把最近的备份恢复到隔离环境。
- 运行 `database.VerifySchema`。
- 运行只读的余额、Webhook、outbox 与对账查询。
- 未经操作员明确签字，不要从恢复环境恢复生产流量。

<a id="read-only-outbound-observation-not-wired"></a>
## 只读出站观察（未接线）

`services/outboundobserver` 是一个只读 EVM 观察组件，面向已持久化的出站意图：它接收调用方提供的 RPC 端点与一个已持久化意图，然后报告 `unknown` / `pending` / `included` 以及执行状态、规范区块证据、确认数与意图匹配情况。它不写任何东西（不写数据库、账本、outbox、不释放 hold、不重播），从不声称 PoS 终局性或 L2 结算，确认数只是观察值。覆盖范围刻意很窄：普通 EVM 原生转账，以及金额为精确原始整数的标准直接 ERC20 `transfer` 调用。`Kind` 是调用方声明，因此不支持的 kind 字符串属于拒绝，而不是运行时检测：该组件校验签名调用与回执自身证据（规范区块、交易索引、回执状态 0/1，以及——对 ERC20——本交易/本区块所归属的 Transfer 日志），且无法认证任意代币的代币经济模型、fee-on-transfer/rebasing 行为或代理路由。非 EVM 链 ID 在发起任何 RPC 之前即被拒绝，交易按标准 JSON-RPC 字段重建（不依赖厂商 raw 扩展）。归集“max”实际金额、fee-on-transfer 或代理路由、跨链流程与非 EVM 族会被报告为不支持，绝不报告为成功。该组件**未接入任何 worker**；阈值与自动终态转换仍属于独立的、需评审的步骤。
