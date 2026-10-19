# PayHarbor 项目笔记（架构与验证）

**这是什么**：Go 后端 / Web3 后端方向的工程实践项目——多链加密支付网关与钱包基础设施（Go Fiber v3、GORM、PostgreSQL、Trust Wallet Core，服务端渲染后台）。
**当前状态**：本地可验证的工程实现；未部署到生产、未做安全审计或认证；出账/归集在广播后保留资金 hold 并进入人工对账；多条链的自动出账终态尚未实现。

## 一、三条可直接粘贴到简历的项目经历

1. **Go 后端 · 支付网关资金核心**：在 PostgreSQL 上实现双分录账本统一承载充值、支付、提现、退款与归集的资金事实；用幂等键 + 单事务资金变更保证重复请求不产生第二笔分录；并发退款以「锁定支付单行 + 同支付在途退款求和 + 原子写入退款/hold/事件」封顶；对外出站广播权收敛为单事务授权，只有仍处于可发送状态且无任何广播证据的意图才能拿到发送权。
2. **Web3 后端 · 多链出账与只读链上观察**：实现链级出站策略（地址白名单、限额、链维护开关）与「广播后保留 hold + 人工对账」的安全边界；独立编写只读链上观察组件：按标准 JSON-RPC 重建签名交易、校验回执状态/交易索引/canonical 区块与 ERC20 Transfer 日志归属，非 EVM 链在发起任何 RPC 之前即拒绝，任何无法证明的情况只输出 `unknown`，不产生副作用。
3. **工程化与可复现交付**：Money Event Inbox/Outbox + 重试、webhook 事件签名与投递记录、离线开发栈（无监听器/无出站流量）以及以「JSON 事件流 + 真实退出码」为门禁的容器化基线测试运行器（非 grep 判绿）。

## 二、核心机制与安全边界（可核对的技术笔记）

- **双分录账本**：`models/ledger_entry.go` 定义借贷方向与提现/退款/归集分录类型，`repositories/ledger_repo.go` 承担记账与 hold 释放；余额按商户/域/链/资产维度汇总，充值、支付、退款、提现不共用同一写入路径。
- **幂等**：商户 API 使用请求幂等键，资源与账务使用唯一键约束重复写入；退款行、hold 与生命周期事件在同一事务提交，不将 API 幂等记录与所有业务写入笼统视为同一事务。
- **并发退款的真实封顶机制**：`repositories/refund_repo.go` 的 `CreateWithHoldGuarded` 在一个事务内先 `FOR UPDATE` 锁定支付单行，再用 `activeRefundTotalRawWithDB` 对该支付单**同支付**的退款（pending/processing/approved/succeeded）求和，与可退上限比较后**原子**写入退款行、退款 hold 与生命周期事件；账本资产锁采用**非阻塞 tryLock**，资产繁忙时立即回滚并返回 `ErrRefundAssetBusy`（API 层返回 503「refund asset is busy, retry shortly」，幂等记录保持可重试）。
- **广播权的最终防线**：`OutboundTransactionRepo.AcquireSendGrant` / `AcquireSweepSendGrant` 在单事务内按固定顺序锁定（资源行 → outbound 行 → 链资源预留），并以 `RowsAffected != 1` 作为 CAS 判定：抢不到就返回 block 而不是广播；已持久化的 hash/尝试时间戳构成不可绕过的栅栏（`RecordObservedBroadcastHash` 只允许把 hash 绑定到同一次已授权尝试）。
- **worker lease 的准确定位**：`acquireWorkerLease`（`WorkerLeaseRepo`）用于减少多实例重复认领，**不是**互斥保证——lease 过期或旧 worker 卡顿都会出现重叠执行；资金安全依赖上面的行锁与 CAS，而不是 lease。
- **只读链上观察组件**：`services/outboundobserver`。输入是已持久化的意图（金额为原始整数、地址来自可信钱包记录），输出只有只读判定：`Status`/`Execution`/`IntentMatched`/`Canonical`/`BlockNumber`/`BlockHash`/`Confirmations`/`Reason`/有界 `Evidence`。它**不写数据库、不签名、不广播，也没有接入运行中的 worker**；返回值本身不构成终态结论。
- **接口与密钥边界**：商户 API 请求验签含时间窗（`helpers/ValidateTimestamp`，30 秒）；webhook 由平台签名后投递，消费方按签名校验并用事件 ID/幂等键做投递去重（平台侧不声称能替消费方阻止所有重放）；商户密钥以 AES-GCM 加密存储（`MASTER_KEY` 派生）。

## 三、演示路线（先完成原生构建，命令以文档为准）

1. 原生构建与测试镜像：按 `docs/developer-guide.md` 执行（Trust Wallet Core 首次原生构建耗时较长，请预留时间）。
2. 隔离验证栈：`deploy/compose/baseline/README.md` 的 Prepare → Build → Test → Smoke 四个阶段，使用内部网络（`internal: true`，不发布宿主端口），测试门禁为 `scripts/baseline/run-tests.sh` + `scripts/baseline/verify-test-json.py` 的真实退出码与 JSON 事件流。
3. 可选页面级演示：离线栈见 `docs/offline-development.md`（`GATEWAY_OFFLINE=true`，无监听器/无出站流量）；该页面级走查**未在本轮执行**，仅作路线指引。
4. 只读组件回归：`go test ./services/outboundobserver/`（纯本地，不触网）。

## 四、面试追问与真实回答

1. **"广播成功但长时间未确认或发生回滚，如何避免重复出账？"** 广播权由单事务授权 + CAS 争抢，只授予一次；广播后立即持久化 hash 并保留 hold，同时写入对账案件；终态之前不会再次自动广播，广播结果不确定（`broadcast_uncertain`）时直接升级为操作员任务。只读观察组件能给出区块/回执事实，但**没有**自动终态能力——这是当前明确的边界。
2. **"并发退款会不会超额？"** 不会越过**同支付单**上限：创建事务先锁支付单行，再对同支付在途退款求和并比较上限，然后原子写入退款+hold+事件；请求重复由幂等键拦截；账本资产繁忙时非阻塞失败并返回 503，调用方可用同一幂等键重试。
3. **"webhook 被伪造或重放怎么办？"** 平台用商户密钥对投递体签名，消费方按签名校验；事件携带事件 ID 与幂等键，供消费方做投递去重与乱序处理。商户 API 侧的 30 秒时间窗（`helpers/ValidateTimestamp`）只约束**入站请求**，不能等同于 webhook 消费方的全部防重放策略。

## 五、已知边界（不要写成亮点）

- 本地工程实现：**未部署、未审计、未认证**；不做吞吐/延迟/用户量/真实资金规模等任何未经测量的声明。
- 出账/归集广播后**保留 hold 且需人工对账**；BTC/SOL/TRON 的自动出账终态未实现。
- `services/outboundobserver` 是只读组件且**未接入运行 worker**（未接即未运行）；没有 finality 策略、没有自动结算、没有 token 分类器。
- 仓库根目录暂无 `LICENSE`；第三方组件按其上游许可（如 `third_party/trustwallet/`），本项目未新增或改动任何许可证声明。

## 六、测试证据（分阶段，不能相加成一次"最新全量"）

| 阶段 | 范围 | 结果 |
|---|---|---|
| A10/R1 | native 全量 `go build` / `go vet` / `go test`（容器内） | build/vet rc=0；1846 passed / 0 failed / 1 处豁免 skip |
| A11/R1 | 新增只读观察组件包 | 88 passed / 0 skip；`-race` 通过 |
| A13 | 隔离未验收增量后的字节比对 | 资金与 observer 源码逐文件 hash 与 A10/R1、A11/R1 基准一致；恢复后 observer 88 passed，`-race` 通过 |
| A14 | 文档修订 + 发布前只读预检 | 无代码/测试改动（不重跑全量） |

证据目录（本机临时路径）：`/tmp/opencode/payharbor-a10r1-lQu1eq/evidence/summary.json`、`/tmp/opencode/payharbor-a11r1-XopV7O/`、`/tmp/opencode/payharbor-portfolio-XopV7O/`。
