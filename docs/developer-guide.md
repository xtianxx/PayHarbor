# 开发者指南

Language: **简体中文**（本指南为中文版本；仓库暂无同篇英文译本）| [English README](../README.en.md)

本指南说明如何安全地开发 Gateway。开启特性分支或评审资金链路改动时，请结合主 README、集成指南与模块边界文档一起使用。

<a id="first-read"></a>
## 首先阅读

- 从 `README.md` 开始，了解环境搭建、环境变量、运行命令与 API 概览。
- 修改充值、支付、账本、出站流程、Webhook、对账、监听器或 worker 之前，先读 `docs/module-boundaries.md`。
- 新增或修改持久化状态之前，先读 `docs/production-migration-discipline.md`。
- 修改 `/api/v1` 合作方 API 行为之前，先读 `docs/integration-guide.md`。
- 新增、重命名或修改 Webhook/资金事件之前，先读 `docs/money-event-catalog.md`。

<a id="local-setup"></a>
## 本地环境搭建

Gateway 是一个 Go 模块化单体，使用 PostgreSQL、GORM、Fiber v3、服务端渲染 HTML、Tailwind CSS 与 Trust Wallet Core。

本地必需工具：

- Go `1.25.4`
- PostgreSQL
- Node.js 与 npm
- Git 子模块支持
- Trust Wallet Core 所需的原生构建工具链，通过 `scripts/build_wallet_core.sh` 使用

一次性初始化：

```bash
git submodule update --init --recursive third_party/trustwallet/wallet-core
go mod download
npm install
./scripts/build_wallet_core.sh
npm run build:css
cp .env.sample .env
```

必须初始化 Trust Wallet Core 子模块，因为 `go.mod` 把 `tw` replace 到 `./third_party/trustwallet/wallet-core/samples/go`。如果 `go test`、`go run` 或 `go mod download` 报缺少 `tw` 替换目录，请初始化子模块，而不是删掉 replace 指令。

`.env` 中请使用仅用于开发的密钥与仅用于开发的助记词。绝不要提交真实密钥、生产助记词、Webhook secret、API secret 或 RPC 凭据。

<a id="development-loop"></a>
## 开发循环

初始化或更新本地数据库：

```bash
go run . -migrate
go run . -seed
```

当你想同时执行迁移与种子时使用 `-install`：

```bash
go run . -install
```

直接运行应用：

```bash
go run .
```

使用 Go 与 HTML 自动重载运行：

```bash
go install github.com/air-verse/air@latest
npm run dev
```

使用 Air 时打开 `http://localhost:3000`。Air 会代理到 `.env` 中的应用端口，通常是 `PORT=:3001`。若修改 `PORT`，请同时更新 `.air.toml` 的 `proxy.app_port`。

编辑 Tailwind 源 CSS 时，在第二个终端运行：

```bash
npm run dev:css
```

提交视图或 CSS 改动之前，先构建生成的/压缩后的 CSS：

```bash
npm run build:css
```

<a id="runtime-shape"></a>
## 运行时结构

应用启动流程都经过 `main.go`：

- 加载 `.env`。
- 依据 `DATABASE_URL` 初始化数据库。
- `routes.NewRouter` 构建 Fiber 应用、repositories、services、chain factory、asset registry、payment hub、门户、`/api/v1`、metrics、Swagger 与 docs 路由。
- `services/database.Migrate` 在非生产环境执行启动迁移，在生产环境校验 schema。
- worker supervisor 启动周期性 worker。
- 地址索引加载、管理员引导、链基础设施与 HTTP 服务器启动。

主要组合根是 `application.CORE`。新的共享依赖应通过组合根与 `api/routes/routes.go` 接线，而不是隐藏在包级全局变量里。

<a id="code-ownership"></a>
## 代码归属边界

请沿用既有包边界：

| 领域 | 改动位置 |
| --- | --- |
| HTTP 路由 | `api/routes/routes.go`；旧命令路径见 `constants/commands.go` |
| HTTP 处理器 | `api/handlers/` |
| 请求/响应 DTO 与校验 | `types/` |
| 业务服务 | `services/` |
| 数据库访问 | `repositories/` |
| GORM 模型 | `models/` |
| 链配置 | `constants/chains.go`、`application/configuration/chains.go`、`blockchain/chains/` |
| 资产/代币配置 | `application/configuration/assets.go`、`asset/` |
| 周期性/后台工作 | `workers/`、`services/*`、`workers/supervisor` |
| 商户/管理/收银台界面 | `views/`、`views/assets/` |
| OpenAPI 产物 | `docs/swagger.yaml`、`docs/swagger.json`、`docs/docs.go` |

资金链路改动必须保持 `docs/module-boundaries.md` 中的归属规则。链监听器只记录事实；它们不直接修改支付、账本余额、Webhook 或归集。充值、支付、账本、出站、Webhook 与对账的改动应通过 services、repositories、worker 命令或资金事件跨越边界。

<a id="common-changes"></a>
## 常见改动

<a id="add-or-change-a-v1-api-endpoint"></a>
### 新增或修改 V1 API 端点

1. 在 `types/` 中新增或更新 DTO 与校验。
2. 在 `api/handlers/` 中实现处理器行为。
3. 在 `api/routes/routes.go` 注册路由。
4. 决定该端点是只读的 API key 访问，还是需要 HMAC 签名的变更类访问。
5. 当端点会产生资金流转或持久化操作时，补上 scope、IP 允许列表、幂等与重放保护行为。
6. 更新 Swagger 注解并重新生成文档：

```bash
swag init -g main.go -o docs
```

7. 面向合作方的行为变化时，更新 `docs/integration-guide.md`。
8. 增加针对处理器、鉴权、仓储与契约的测试。

<a id="add-or-change-a-database-model"></a>
### 新增或修改数据库模型

1. 在 `models/` 下新增或更新 GORM 模型。
2. 在 `repositories/` 下增加仓储方法；不要把查询与事务行为放进处理器。
3. 在 `services/database.autoMigrateModels` 注册模型。
4. 在 `services/database.VerifySchema` 辅助函数与测试中补充 schema 校验覆盖。
5. 在 `services/dbmigrations/migrations.go` 新增版本化工件，包含摘要、前向方案、锁影响、回填、回滚、校验查询与校验命令。
6. 工件列表变化时更新 `docs/production-migration-discipline.md`。
7. 为幂等、加锁、冲突与生命周期状态转换增加仓储测试。

生产 schema 变更不是仅靠启动时 `AutoMigrate` 的改动。生产上线、回填、校验与回滚证据必须明确保留。

<a id="add-a-worker"></a>
### 新增 worker

1. 尽可能把纯逻辑放进带 `RunOnce` 风格方法的 service。
2. 让循环接受 `context.Context`，并在取消时及时停止。
3. 用 `workers/supervisor` 注册生命周期归属。
4. 当只允许一个进程拥有该任务时，使用 `WorkerLeaseRepo`。
5. 在确认外部或跨模块工作之前先持久化进度。
6. 为取消、重试、幂等、锁过期与部分失败恢复增加测试。

<a id="add-a-chain"></a>
### 新增链

1. 在 `constants/chains.go` 增加链 ID、名称、logo slug 与测试网分类。
2. 在 `blockchain/chains/` 实现该链，并满足共享链接口。
3. 在 `application/configuration/chains.go` 注册它，包含别名。
4. 当该链有监听器时，补充 RPC env 处理与监听器起始区块策略支持。
5. 在 `application/configuration/assets.go` 增加原生资产与代币部署。
6. 为链发现与地址校验补充路由/API/就绪/测试覆盖。
7. 当 UI 需要 logo 时，增加静态链/币资产。

<a id="add-an-asset-or-token-deployment"></a>
### 新增资产或代币部署

1. 在 `application/configuration/assets.go` 新增或更新 `asset.AssetDefinition`。
2. 使用正确的链 ID、合约地址或 mint、精度、原生标记、符号与启用标记。
3. 仅当项目有意把包装或替代符号视为规范资产时，才添加别名。
4. 当 CoinGecko 不覆盖该符号时，补充价格回退 env 支持。
5. 当部署影响分组、选择、logo 或 API 输出时，新增或更新资产注册表测试。

<a id="change-webhooks-or-money-events"></a>
### 修改 Webhook 或资金事件

1. 更新 `services/webhook/event_catalog.go` 中的目录。
2. 更新 `docs/money-event-catalog.md`。
3. 保持事件 ID 稳定与幂等。
4. 保持 Webhook 投递为至少一次且重试安全。
5. 为事件形状、顺序、重放与诊断行为增加契约测试。

<a id="change-merchantadmincheckout-views"></a>
### 修改商户/管理/收银台视图

1. 编辑 `views/` 中的模板。
2. 编辑 `views/assets/` 中的 CSS/JS。
3. Tailwind 输入变化时运行 `npm run build:css`。
4. 为你改动过的每个资源重新生成压缩 sidecar：`go run ./scripts/brotli-sidecars <asset files...>`（或用 `go run ./scripts/brotli-sidecars --verify` 审查全部）。若某个 sidecar 解压后与源文件不一致，`TestFiberBrotliSidecarsMatchSources` 会失败。
5. 在 `views_test.go` 或处理器测试中新增或更新视图渲染测试。
6. 在开发模式下验证收银台与门户路由，以覆盖 HTML 重载与禁用缓存的静态资源。

<a id="localization-zh-cn--en"></a>
### 本地化（zh-CN / en）

所有面向用户的界面——公开首页、商户门户、管理面板、托管买家流程（支付链接、收银台、支付页、支付结果）与发票——都通过仓库内 `localization` 包渲染文案。机制只有一套：

- **区域**：`zh-CN`（默认）与 `en`。英文是目录键，因此 `localization.T` 在缺少条目时会回退到源文本，而 zh-CN 目录（`localization/catalog_zh*.go`）保存译文。已退役的收银台区域 `tr` 在任何地方都不是受支持取值。
- **解析顺序**：可识别的 `?lang=` 查询参数 → `gateway_portal_lang` cookie → 旧版 `gateway_lang` cookie（只读，仅识别已知取值）→ `zh-CN`。永不参考 `Accept-Language`，因此默认值是可确定的。支付链接落地页还会把商户存储的链接语言作为该旅程的默认值（`localization.BuyerLocaleForProduct`）。
- **记录选择**：`api/middleware/portal_locale.go` 拥有所有用户可见路径。带可识别 `lang` 取值的 GET/HEAD 请求会写入 `gateway_portal_lang`，剥掉该参数并在同一路径重定向。内部生成的 URL 不携带 `lang`，因此切换语言不会留下陈旧参数。
- **浏览器文案**：服务端渲染每请求 JSON bundle（`<script type="application/json" id="portal-i18n-data">`），`views/assets/portal-i18n.js` 将其暴露为 `portalT(key)`；当该运行时缺失时 `views/assets/checkout.js` 降级为英文键。客户端代码绝不能从 `document.documentElement.lang` 或 `navigator.language` 推导文案。
- **新文案规则**：新增 `{{ t .Locale "English source text" }}` 调用（浏览器资源中用 `portalT("…")`），并把 zh-CN 条目加到对应目录文件。不要把汉字硬编码在目录之外：否则 `TestCatalogCoversEveryRequestedKey` 与 CJK 规则测试会失败。

修改模板、资源或目录时，还需重新生成被改动资源的压缩 sidecar，并重跑本地化测试：

```bash
go test -count=1 ./localization ./api/handlers
```

<a id="testing"></a>
## 测试

开发时运行聚焦测试：

```bash
go test ./services/deposits ./repositories -run TestName
```

运行完整 Go 套件：

```bash
go test ./...
```

当本地没有原生 Trust Wallet Core，或需要 CI 风格的隔离钱包模式时，运行 fallback 模式回归：

```bash
GATEWAY_REGRESSION_MODE=fallback ./scripts/regression.sh
```

在触及钱包生成、签名、地址校验、链实现或生产关键资金流转的改动之前，运行原生回归：

```bash
./scripts/regression.sh
```

部分仓储与集成测试需要 PostgreSQL，未提供 DSN 时会 skip。请使用一次性数据库：

```bash
TEST_DATABASE_URL="host=127.0.0.1 port=5432 user=postgres password=postgres dbname=gateway_test sslmode=disable" go test ./repositories ./services/database ./workers/indexer ./services/txrescan
```

通过回归脚本或直接运行 Go vet：

```bash
go vet ./...
```

<a id="safety-rules"></a>
## 安全规则

- 不要用 `-tags walletcorefallback` 运行服务器；除非为窄范围调试显式覆盖，否则 `main.go` 会拒绝 fallback 服务器运行。
- 不要用 `SIGNER_MODE=software` 承担生产托管。生产签名必须留在外部签名模式（如 KMS、HSM、MPC 或 Vault）之后。
- 不要在链监听器中直接添加资金链路状态变更。
- 不要因为仓储可访问，就绕过模块边界在处理器中写跨模块表更新。
- 不要为支付、出账、退款、归集、Webhook 或账本工作绕过幂等。
- 不要为变更类合作方 API 路由削弱 HMAC 签名、时间戳校验、API scope、IP 允许列表行为或重放保护。
- 不要提交 `.env`、私钥、助记词、API secret、Webhook secret、生产 RPC 凭据或生成的本地数据库导出。

<a id="pull-request-checklist"></a>
## Pull Request 检查清单

- 相关文档已更新：视需要更新 README、集成指南、资金事件目录、runbook 或迁移纪律文档。
- 新的持久化状态已在模型、迁移工件、schema 校验与仓储测试中登记。
- 新的合作方行为已更新 API 契约与 Swagger。
- 新的 worker 行为可取消、幂等、可观测，并由 supervisor 托管。
- 资金链路改动遵守模块归属边界。
- CSS 改动已重建 `views/assets/tailwind.css`，并重新生成被改动资源的压缩 `.fiber.br` sidecar。
- 测试覆盖了窄范围包测试，且在改动有跨模块影响时补充了更广的回归。
