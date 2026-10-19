# 离线开发环境

离线环境在私有 Docker 网络中运行网关（**不带任何链监听器、不带任何周期性资金/事件 worker**）以及一个隔离的 PostgreSQL。它的存在是为了让本地开发、HTTP 相关工作、迁移与测试套件工作永远不触碰公共 RPC 端点、真实余额或真实资金。

它**不是**生产环境，`/api/v1/common/readiness` 也会刻意把它报告为未就绪。

<a id="isolation-model"></a>
## 隔离模型

| 属性 | 实现方式 |
| --- | --- |
| 无链上流量、无资金 worker | `GATEWAY_OFFLINE=true` 搭配 `APP_ENV=development`。仅在明确的开发环境有效；任何其他 `APP_ENV` 都会在打开数据库之前直接失败关闭。 |
| 应用/数据库无出网能力 | `gateway` 与 `postgres` **只**接入 `gateway-offline-internal`（`internal: true`）。在本机实测：两个容器都无法解析或访问外部主机（公网 IP 与 DNS 探测）。relay 容器是唯一属于非内部网络的成员，它确实有出网能力（它只是一个不持有任何凭据的惰性 TCP 转发器）。 |
| 仅回环发布端口 | PostgreSQL 在 `127.0.0.1:55432`，HTTP 在 `127.0.0.1:3001`。两者都由 `loopback` relay 服务发布。在本机（WSL2、Docker 29.6.1）实测：只接入 `internal: true` 网络的容器无法拥有可用的端口映射，因此需要 relay 来提供映射；其他主机与 Docker 版本可能不同。 |
| 数据持久化 | 命名数据卷 `gateway-offline-pgdata`。`docker compose down` 会保留它；只有 `down -v` 会删除。 |
| 密钥 | `scripts/generate-offline-env.sh` 只创建一次 `.env`，权限 `0600`，绝不覆盖，绝不打印取值，也不配置任何 OIDC/RPC/托管凭据。 |

`loopback` relay 是一个惰性的 `alpine/socat` TCP 转发器。它不承载任何资金链路逻辑与密钥；网关与数据库自身仍然没有出网能力。

<a id="prerequisites"></a>
## 前置条件

- 带 Compose v2 的 Docker Engine（`docker compose version`）。
- 镜像构建所需的、固定版本的 Wallet Core 子模块：
  `git submodule update --init --recursive third_party/trustwallet/wallet-core`
- `postgres:16-alpine`（本地已有）与 `alpine/socat:1.8.1.3`（首次 `up` 时拉取）。

<a id="quick-start"></a>
## 快速开始

所有命令都在仓库根目录执行。

```bash
# 1. Create the development .env (never overwrites an existing one)
scripts/generate-offline-env.sh

# 2. Build the image (native Trust Wallet Core compile, tens of minutes) and start the stack
docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml up -d --build

# 3. Follow the gateway logs
docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml logs -f gateway

# 4. Verify the runtime contract
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3001/            # 200 from the merchant portal

# Readiness is authenticated: without an API key it answers 401 by design.
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3001/api/v1/common/readiness   # 401, no credentials

# Authenticated acceptance signal: use an API key whose domain has the "read" scope.
# `read -rs` keeps the value out of the terminal and out of shell history.
read -rs GATEWAY_DEV_API_KEY

# The header travels over stdin (`--header @-`), so the expanded secret never lands in the
# curl process argv. Do not run `set -x` here, and never echo or interpolate the variable
# into a command line.
printf 'X-API-Key: %s\n' "$GATEWAY_DEV_API_KEY" | \
  curl -sS -o /tmp/opencode/gateway-readiness.json -w '%{http_code}\n' \
    --header @- http://127.0.0.1:3001/api/v1/common/readiness   # 503, body has offline.mode ok=false
unset GATEWAY_DEV_API_KEY

# 5. Stop the stack (the data volume is preserved)
docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml down
```

<a id="where-the-development-api-key-comes-from"></a>
### 开发用 API key 从哪来

`.env` 刻意不含任何 API key，`scripts/generate-offline-env.sh` 也不会创建商户或域。要拿到一个 key：

1. 通过 `http://127.0.0.1:3001/merchant/register` 的门户注册一个商户；
2. 在仪表盘创建域（`/merchant/dashboard`、`POST /merchant/domains`——它要求 domain URL、webhook URL 与 webhook secret）；
3. 复制仪表盘域列表中显示的域 **API Key**。新域会获得默认 scope，其中包含 `read`。

如何解读：未认证的 `401` 是预期结果，**不是**离线断言。离线验收信号是认证后的 `503`，其响应体包含 `offline.mode` 且 `ok=false`。为一次性验证而创建的 key（例如放在 `/tmp` 下）是一次性证据——绝不要把它当作环境配置。

确认离线模式的日志行：

```text
Offline mode: GATEWAY_OFFLINE=true with APP_ENV=development; chain listeners and periodic money/event workers stay disabled
Offline mode: chain listeners are NOT started (no RPC dialing, no chain state reads, no listener registration)
Offline mode: worker supervisor started with 0 periodic tasks
```

<a id="command-reference"></a>
## 命令参考

| 任务 | 命令（在仓库根目录执行） |
| --- | --- |
| 启动（重建镜像） | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml up -d --build` |
| 启动（不重建） | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml up -d` |
| 停止并保留数据 | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml down` |
| 仅停止容器 | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml stop` |
| 日志（gateway） | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml logs -f gateway` |
| 日志（数据库） | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml logs -f postgres` |
| 状态 | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml ps` |
| 进入数据库 shell | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml exec postgres psql -U postgres -d gateway` |
| 迁移 | 自动执行：只要 `APP_ENV` 不是 `production`，gateway 就在启动时迁移（`ALLOW_AUTOMIGRATE_IN_PRODUCTION` 默认 false）。`-migrate` / `-seed` / `-install` 参数**不会**让进程变成一次性执行——它们只强制跑迁移/种子步骤，之后 HTTP 服务仍会启动并继续提供服务，因此不要用 `compose run --rm gateway -install` 并期待它自行退出。 |
| 运行 Go 测试套件（无需宿主机工具链） | 构建测试镜像：`docker build --build-arg BUILD_JOBS=4 --target gotest -t gateway-offline:test .`。先一次性创建隔离测试库：`docker compose … exec postgres psql -U postgres -c 'CREATE DATABASE gateway_test'`。然后运行：`docker run --rm --network gateway-offline-internal -e TEST_DATABASE_URL="host=postgres port=5432 user=postgres password=<db password> dbname=gateway_test sslmode=disable" -e OUTBOX_TEST_DATABASE_URL="<same dsn>" -e MONEY_OUTBOX_TEST_DATABASE_URL="<same dsn>" -e TXRESCAN_TEST_DATABASE_URL="<same dsn>" gateway-offline:test go test -p=1 -count=1 ./...` ——没有这些 DSN 时，Postgres 门禁测试会 skip。 |
| 离线策略单元测试（宿主机，无需原生库） | `GOTOOLCHAIN=local CGO_ENABLED=1 go test ./helpers -run Offline -count=1` |
| shell 语法检查 | `sh -n scripts/generate-offline-env.sh && sh -n scripts/build_wallet_core.sh` |
| Compose 校验 | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml config --quiet`（只看退出码，不打印内容） |

`docker compose config` 会渲染完整的 `env_file` 取值，且 `--no-interpolate` 不会隐藏它们（本机实测）。共享渲染结果时，请用 `config --quiet` 校验，或把输出过滤成不含敏感信息的投影。

<a id="what-offline-mode-disables-and-what-still-works"></a>
## 离线模式禁用了什么，什么仍然可用

已禁用（完全跳过，而不是打桩）：

- 全部十二个已注册链的监听器构造/启动（不做 RPC 拨号、不读链状态）；
- 周期性 worker：`webhook-retry`、`session-expiry`、`transaction-finality`、`deposit-facts`、`outbound-transactions`、`sweep-jobs`、`reconciliation`、`transfer-finalization`、`provider-health`；
- 地址回填与完整地址索引预加载（内存索引对象仍保持接线，因此 HTTP 路径不会 panic）。

仍然可用（未改动）：

- HTTP 服务、路由、中间件、商户/管理/收银台界面，**包括资金链路端点**：离线开关移除的是后台循环，它不会拒绝 API 调用。让这些调用不会触达真实价值的是环境本身而非开关：容器完全没有出网能力、`SIGNER_MODE=software` 只能用于非生产、助记词是全新的开发专用短语、且没有配置任何链 RPC 端点；
- 数据库连接与启动迁移；
- Trust Wallet Core 初始化路径（仍需要原生构建；此处永不使用 `walletcorefallback` 标签，`GATEWAY_OFFLINE` 也不能豁免它）；
- 生产安全门禁：软件签名器的生产阻断、规模模式门禁与就绪检查。

资金流转（充值、归集、出账、退款、对账）**不在本环境的验收范围内**。它不会被演练，而且在没有出网能力、没有 RPC 端点的情况下也无法触达真实网络。

<a id="functional-limitations"></a>
## 功能限制

- `/api/v1/common/readiness` 需要认证调用方：未认证请求得到 `401`；而认证请求若其域具有 `read` scope，则始终包含 `offline.mode: ok=false` 并按设计返回 503。
- 观察不到任何链上事件，因此充值、终局性、支付匹配、归集、退款与出账都不会推进。
- 需要实时链上数据的端点（余额、针对网络的地址校验、实时余额管理视图）会失败或返回错误；因为没有配置 RPC 端点。
- 钱包/地址派生使用 env 生成器创建的开发助记词；这些地址是一次性的，绝不能接收真实资金。
- HTTP 客户端 IP 是 relay 的地址，而不是调用方的地址（relay 是纯 TCP 转发器）。
- 本环境仅支持 `linux/amd64`。

<a id="safety-rules"></a>
## 安全规则

- 绝不要复用曾经持有真实资金的助记词。`.env` 已被 git 忽略，必须保持权限 `0600`。
- 这里绝不要设置 `APP_ENV=production`、`ALLOW_AUTOMIGRATE_IN_PRODUCTION` 或 `ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION`。
- 在 `APP_ENV=development` 之外设置 `GATEWAY_OFFLINE=true` 会中止启动：离线模式绝不能成为绕过生产保证的手段。
- 不要提交生成的 `.env` 文件、数据库导出或 RPC/OIDC 凭据。

<a id="verified-behaviour-env-03-measured-on-this-host"></a>
## 已验证行为（ENV-03，在本机实测）

Docker 构建会从固定子模块编译真实的 Trust Wallet Core（`[100%] Built target TrustWalletCore`，产出 `libTrustWalletCore.a` 与 `libwallet_core_rs.a`），且从不使用 `walletcorefallback`。观察到的事实：

| 检查 | 结果 |
| --- | --- |
| 完整镜像构建 | 退出码 0，`BUILD_JOBS=4` 时约 32 分钟；宿主内存峰值约 11.4 GiB / 15.9 GiB（不受限的默认值在此处会不安全） |
| 运行时镜像 | `gateway-offline:local`，磁盘占用 435 MB（压缩 manifest 约 124 MB），以 uid 10001（`gateway`）运行，内置健康检查为 healthy |
| 启动 | `Migration:BEGIN/END`，随后是离线模式日志，然后是 `App running on :3001`；零条监听器启动日志；`worker supervisor started with 0 periodic tasks` |
| HTTP | `/` → 200；`/assets/tailwind.css` → 200（25 876 字节，与仓库文件逐字节一致） |
| 就绪 | 未认证 → 401；用 `read` scope 的开发 key 认证 → 503 且 `offline.mode ok=false`（`offline development mode is active`），46/59 项检查 ok |
| 数据库 | PostgreSQL healthy，`uuid-ossp` 已存在，迁移创建了完整 schema；`sweep_jobs`、`outbound_transactions` 与 `transactions` 均为 0 |
| 出网 | Gateway：公网 IP 探测 `http://1.1.1.1` 失败（`Couldn't connect to server`）且 DNS 解析失败；PostgreSQL：`Network unreachable`；对照组：relay 容器**可以**访问同一 IP，证明该探测确实能检测出网 |
| 端口 | 宿主机只绑定了 `127.0.0.1:3001` 与 `127.0.0.1:55432`；gateway 与数据库自身不发布任何端口 |
| 测试套件 | 在镜像内运行（`--target gotest`）并连向独立的 `gateway_test` 数据库：`go build ./...` 与 `go vet ./...` 退出码 0；`go test -p=1 -count=1 ./...` 退出码 0 → 43 个包 ok（8 个包没有测试文件） |

<a id="known-limitations"></a>
## 已知限制

- 开发环境下静态中间件以禁用压缩的方式运行，因此资源以 identity 编码提供，仓库中提交的 `.fiber.br` sidecar 不会被使用；它们由 `TestFiberBrotliSidecarsMatchSources` 守护，该测试要求每个 sidecar 解压后与源文件一致。每当 `npm run build:css` 改动某个资源时，用 `go run ./scripts/brotli-sidecars <asset files...>` 重新生成其 sidecar（`go run ./scripts/brotli-sidecars --verify` 会审查全部 sidecar）。
- 离线模式下 `chain_states` 保持**为空**：这些行由链监听器路径创建，而离线模式会跳过它。任何需要链状态的功能（支付确认跟踪、余额视图）都会报错，这与就绪接口返回 503 是一致的。依赖链状态的工作请使用非离线运行时。
- relay 容器是唯一属于非内部网络的成员，因此它有出网能力（它只是一个不持有任何凭据的惰性 TCP 转发器）。网关与数据库没有。
- HTTP 请求 IP 是 relay 的地址。
- 离线开关不会拒绝 HTTP 资金链路调用；它们的保护来自缺失的出网能力、缺失的链 RPC 端点、开发专用助记词以及生产签名器阻断——而不是开关本身。

<a id="deferred-work"></a>
## 推迟的工作

不属于上述已验证环境的内容：

1. 演练资金链路 HTTP 工作流（充值、归集、出账、退款、对账）——刻意排除在本环境验收范围之外；
2. 在宿主机运行 `scripts/build_wallet_core.sh`（镜像改为在容器内构建同一份源码）；
3. 在宿主机运行 `./scripts/regression.sh`，它会重装系统包并重建 Wallet Core；等价做法是在测试镜像内执行 `go build ./... && go vet ./... && go test -p=1 -count=1 ./...`；
4. 生产托管、KMS/HSM/Vault 签名以及任何主网连通性。
