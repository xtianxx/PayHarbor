# PayHarbor

PayHarbor 是一个用 Go 编写的多链加密支付网关与钱包基础设施：商户可以用它收单，开发者可以生成用户级多链地址，钱包/交易所类产品可以在同一套资金核心上完成入账、支付、出账、退款、归集、Webhook 与对账。

语言：[**简体中文**](README.md) | [English](README.en.md)

> **当前状态**：本地可验证的工程项目，未部署到生产、未做安全审计或认证。当前边界见[已知边界](#已知边界)。

## 功能特性

- **收单与商户 API**：托管收银台、发票/支付链接、静态充值地址、`/api/v1` 商户 API。
- **多链钱包基础设施**：基于 Trust Wallet Core 的 BIP39/HD 地址派生与地址校验。
- **链上事实与入账**：链监听器把链上交易整理为持久化事实，经过确认策略与支付匹配后进入账本。
- **双分录账本**：充值、支付、提现、退款、归集以借贷分录记录，余额按商户/域/链/资产隔离。
- **出账与安全栅栏**：提现/退款/归集通过单事务发送授权（资源行锁 + CAS + 已持久化尝试栅栏）取得广播权；广播结果不确定时升级为人工任务，不自动重发。
- **退款**：退款创建在单个事务内锁定支付单、按同一支付单的有效退款总额（含已成功退款）封顶，并原子写入退款、hold 与生命周期事件。
- **Webhook 与事件投递**：商户 API 使用签名与时间窗；Webhook 由平台签名投递，事件携带事件 ID/幂等键供消费方去重；失败投递有记录与重试。
- **对账与运维**：资金不变量检查、储备余额检查、卡住生命周期案件、归集运维台账与操作员后台。
- **商户/管理后台**：服务端渲染的商户门户与管理门户，支持中英文界面。

## 技术栈

- Go `1.25.4`
- Go Fiber v3（HTTP）、GORM（ORM）
- PostgreSQL 16
- Trust Wallet Core（原生库，CGo）
- Tailwind CSS 与服务端 HTML 模板

## 架构简述

- `api/`：路由、处理器与中间件；`api/routes/routes.go` 负责装配。
- `services/`：业务服务（资金、实时推送、Webhook、定价、对账、观察等）。
- `repositories/`：全部数据库访问；账本、支付、出账、退款、Webhook 投递等各有仓储。
- `models/`：GORM 模型；`blockchain/`：链接口与各链实现；`workers/`：监听器与分发器；`asset/`：资产注册表。
- `application/configuration/`：链与资产的注册入口（新增链在 `chains.go`，新增代币在 `assets.go`）。
- `main.go`：装配、迁移/种子、worker 监督者与各周期任务。

资金主链路：支付会话 → 客户选链选币 → 地址/二维码 → 链上交易事实 → 确认与支付匹配 → 双分录账本 → 事件 Outbox → Webhook 投递。

发送授权是出账的最终防线：授权在单事务内按固定顺序加锁并以 CAS 判定，抢不到就不广播；已持久化的 hash 与尝试时间戳构成栅栏。worker lease 只用于减少多实例重复认领，不是互斥保证。

## 项目结构

```text
api/          HTTP 路由、处理器与中间件
services/     业务服务（资金、Webhook、实时推送、对账、观察等）
repositories/ 数据库访问与事务边界
models/       GORM 模型
blockchain/   链接口与各链实现
workers/      链监听器与事件分发器
asset/        资产注册表
application/  装配与链/资产配置入口
views/        服务端渲染模板（收银台、商户与后台）
```

## 支持网络

Bitcoin、Ethereum、TRON（含 Nile 测试网）、Solana、Avalanche C-Chain、BNB Chain、Chiliz（含 Spicy 测试网）、Base、Arbitrum One、Unichain。

链记录在 `application/configuration/chains.go`，资产记录在 `application/configuration/assets.go`。

## 快速开始

要求：Go `1.25.4`、PostgreSQL 16、Node.js/npm（Tailwind 输出）、CGo 与 clang/cmake（Trust Wallet Core 原生构建）。

克隆（包含子模块）：

```bash
git clone --recurse-submodules https://github.com/xtianxx/PayHarbor.git PayHarbor
cd PayHarbor
```

原生库首次构建耗时较长，请预留时间：

```bash
./scripts/build_wallet_core.sh
```

准备配置（参考 `.env.sample`；至少需要 `DATABASE_URL`、`MASTER_KEY`、`PORT`、`MNEMONIC_PHRASE`）：

```bash
cp .env.sample .env
```

先编辑 `.env`，填入本地开发配置；不要使用生产凭据或存放真实资金的助记词。首次启动：

```bash
go run . -install
```

`-install` 执行迁移与种子步骤后会继续启动 HTTP 服务，不会自动退出。后续启动可使用 `go run .`（默认监听 `:3001`）。

不使用宿主网络的隔离验证栈见[离线开发环境](docs/offline-development.md)；容器化基线测试与门禁见[基线测试栈](deploy/compose/baseline/README.md)。

## 安全要点

- 商户 API 请求使用 API Key/Secret 签名，并校验时间窗；商户密钥以 AES-GCM 加密存储（由 `MASTER_KEY` 派生）。
- Webhook 由平台签名投递，消费方应校验签名，并用事件 ID/幂等键做投递去重；平台侧不声称替代消费方的全部重放防护。
- 出站策略包含地址白名单、限额与链级维护开关；未满足条件时不广播。
- 幂等分层：API 请求以幂等键去重，资源与账务写入以唯一键去重，二者不依赖同一条事务记录；退款行、hold 与生命周期事件在同一事务提交，出账广播权由独立事务授权并以持久化尝试栅栏约束。
- 生产托管必须使用外部签名器适配器，进程内软件签名仅用于开发环境。

## 验证

```bash
go test ./...                          # Go 测试套件（部分方案需要 PostgreSQL 测试库）
go test ./services/outboundobserver/   # 只读链上观察组件回归
```

- 容器化基线运行器以真实退出码与 JSON 事件流作为门禁：`scripts/baseline/run-tests.sh` + `scripts/baseline/verify-test-json.py`。
- 分阶段测试证据（不能相加成一次“最新全量”）：A10/R1 native 全量 1846 passed / 1 处豁免 skip；A11/R1 只读观察组件 88 passed / 0 skip（含 `-race`）。
- 本仓库未声明 CI 已通过；CI 状态以实际运行结果为准。

## 文档

- [开发者指南](docs/developer-guide.md)：本地开发循环、迁移、worker、测试与 PR 约定。
- [离线开发环境](docs/offline-development.md)：`GATEWAY_OFFLINE=true`，无监听器、无资金 worker、无出站流量。
- [基线测试栈](deploy/compose/baseline/README.md)：Prepare/Build/Test/Smoke 四阶段，内部网络不发布宿主端口。
- [资金链路可观测性手册](docs/money-path-observability-runbook.md) 与 [归集运维手册](docs/sweep-operations-runbook.md)。
- [集成指南](docs/integration-guide.md)：签名、端点、状态、Webhook 与错误处理契约。
- [项目笔记](docs/portfolio.md)：架构与验证说明。

## 已知边界

- 本项目是**本地可验证工程**：未部署到生产，未做安全审计或认证；本文档不声明生产可用性。
- 已广播但结果不确定的出账**不会自动重发**，也不会自动释放 hold；这类案件进入操作员处理。
- 归集/出账在广播后保留资金 hold，需要**人工对账**；BTC/SOL/TRON 的自动出账终态尚未实现。
- `services/outboundobserver` 是**只读**组件：不写数据库、不签名、不广播，且**未接入运行中的 worker**。它报告的确认深度只是观察值，不代表 PoS/L2 最终性。
- 仓库根目录暂无 `LICENSE` 文件；第三方组件（如 `third_party/trustwallet/` 的 Trust Wallet Core）保留各自上游许可，本项目未新增或变更许可证。

## 第三方许可

`third_party/` 下的组件（包括 Trust Wallet Core 子模块）遵循其各自上游许可证；发布或分发前请按上游要求保留相应声明。
