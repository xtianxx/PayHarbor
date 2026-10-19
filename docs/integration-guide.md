# PayHarbor 集成指南

本文档面向把商户系统接入本支付网关的开发者与 AI 编码代理。

<a id="quick-summary"></a>
## 快速摘要

```yaml
base_url: "https://your-gateway.example.com"
auth_read_endpoints:
  required_headers:
    X-API-Key: "<domain_api_key>"
auth_write_endpoints:
  required_headers:
    X-API-Key: "<domain_api_key>"
    X-API-Secret: "<domain_api_secret>"
    X-Gateway-Timestamp: "<unix_seconds>"
    X-Gateway-Signature: "sha256=<hmac_sha256_hex(method + path/query + timestamp + raw body)>"
  request_signature_payload: "METHOD\npath?query\ntimestamp\nraw_body"
webhook:
  callback_model: "one selected notification target per domain"
  targets: ["webhook", "nats"]
  configured_in: "merchant portal domain settings or domain creation API"
  signature_header: "X-Gateway-Signature"
  signature_payload: "timestamp + raw_body"
  current_events:
    - native_transfer
    - transaction.reorged.v1
    - payment_succeeded
    - payment_failed
    - payment_expired
    - payment.underpaid.v1
    - payment.overpaid.v1
    - payment.partial_paid.v1
    - payout.requested.v1
    - payout.broadcast.v1
    - payout.finalized.v1
    - payout.rejected.v1
    - payout.failed.v1
    - refund.requested.v1
    - refund.broadcast.v1
    - refund.succeeded.v1
    - refund.rejected.v1
    - refund.failed.v1
    - sweep.requested.v1
    - sweep.succeeded.v1
    - sweep.failed.v1
    - sweep.dead_lettered.v1
```

<a id="credentials"></a>
## 凭据

一个商户域包含：

- `api_key`：公开的 API 标识。
- `api_secret`：用于 HMAC 请求签名的密钥。只在创建域或轮换 API secret 时返回。
- `api_scopes`：逗号分隔的权限列表。新凭据默认拥有 `read,payment:create,wallet:create,payout:create,refund:create,webhook:manage,transaction:rescan`；旧的空 scope 凭据在显式收窄之前按旧版全量权限处理。
- `api_ip_allowlist`：可选的逗号分隔 IP/CIDR 允许列表，用于商户 API 请求。
- `webhook_secret`：网关用于签名 Webhook 回调的密钥。
- `webhook_url`：单一回调 URL，用于当前支持的全部 Webhook 事件。
- `notification_mode`：`webhook` 或 `nats`；投递时恰好只有一个目标处于启用状态。
- `nats_url` / `nats_subject`：当 `notification_mode` 为 `nats` 时使用的 NATS 服务器与主题。

如果 `api_secret` 丢失，可通过商户门户端点轮换：

```http
POST /merchant/domains/{domain_id}/rotate-api-secret
```

轮换后的 secret 只会在响应中返回一次。

轮换策略是立即吊销：新 secret 哈希写入后，旧 API secret 立刻无法通过认证。设置了 `api_secret_revoked_at` 的凭据会在请求签名与业务逻辑之前被拒绝。

<a id="permission-scopes"></a>
### 权限 Scope

需要签名的变更类端点在业务逻辑运行前要求匹配的 scope：

| Scope | 典型端点族 |
| --- | --- |
| `read` | 状态、余额、历史、信息与就绪类读取。 |
| `payment:create` | 托管支付与支付链接创建。 |
| `wallet:create` | 静态充值地址创建。 |
| `payout:create` | 出账创建。 |
| `refund:create` | 退款创建。 |
| `transaction:rescan` | 交易重扫/重放。 |
| `webhook:manage` | Webhook 配置与重放管理界面。 |

IP 允许列表按请求来源 IP 检查。认证失败会以类别级证据记录，例如 `api_scope_denied` 或 `api_ip_not_allowed`；日志不得包含 API secret、原始签名或完整的含密文头。

<a id="rate-limits"></a>
### 限流

`/api/v1` 流量按 API key 或 bearer token 限流。在分布式/生产规模模式下，限流器必须使用共享的数据库计数表，而不是进程内存。若该模式下共享存储不可用，API 会以限流存储错误失败关闭。

<a id="request-signing"></a>
## 请求签名

写端点要求 HMAC 签名。

需要签名的端点包括：

- `POST /payments/create`
- `POST /api/v1/payment/create`
- `POST /api/v1/payment/white-label`
- `POST /api/v1/payment/static-address`
- `POST /api/v1/wallet/create`
- `POST /api/v1/payout/create`
- `POST /api/v1/refund/create`
- `POST /api/v1/transaction/rescan`

读端点通常只需要 `X-API-Key` 或 `Authorization: Bearer <api_key>`。

在生产流量之前，先用域 API key 调用 `GET /api/v1/common/readiness`。只有当数据库、生产门禁、已配置链、监听器 worker、Trust Wallet Core HD 钱包派生，以及最新的 RPC/provider 健康快照都就绪时才返回 `200`；否则返回 `503` 并列出失败检查项。Provider 健康快照由后台 worker 收集，并会抹除原始 RPC URL。

<a id="signature-algorithm"></a>
### 签名算法

V1 请求签名绑定方法、原始路径/查询目标、时间戳与原始请求体。

```text
timestamp = current unix timestamp in seconds
METHOD = uppercase HTTP method, for example POST
path_and_query = request path plus query string, for example /api/v1/payment/create
body = exact raw JSON request body bytes
message = METHOD + "\n" + path_and_query + "\n" + timestamp + "\n" + body
signature = hex(HMAC_SHA256(api_secret, message))
header = "sha256=" + signature
```

对于 `POST /api/v1/payment/create`，规范前缀是 `POST\n/api/v1/payment/create\n<timestamp>\n<body>`。如果签名端点带查询参数，请在 `path_and_query` 中按实际发送形式原样包含它们。

网关接受 `X-Gateway-Signature` 为 `sha256=<hex>` 或 `<hex>`。

<a id="nodejs-signing-example"></a>
### Node.js 签名示例

```js
import crypto from "crypto";

function sign(apiSecret, method, pathAndQuery, body) {
  const timestamp = Math.floor(Date.now() / 1000).toString();
  const signature = crypto
    .createHmac("sha256", apiSecret)
    .update(method.trim().toUpperCase())
    .update("\n")
    .update(pathAndQuery.trim())
    .update("\n")
    .update(timestamp)
    .update("\n")
    .update(body)
    .digest("hex");

  return {
    "X-Gateway-Timestamp": timestamp,
    "X-Gateway-Signature": `sha256=${signature}`,
  };
}
```

<a id="create-hosted-payment"></a>
## 创建托管支付

当你希望客户通过托管收银台页面付款时使用。

```http
POST /api/v1/payment/create
```

请求体：

```json
{
  "order_id": "ORDER-1001",
  "amount": "25.00",
  "currency": "USD",
  "description": "Order #1001",
  "user_id": "customer_42",
  "settlement_policy": "single",
  "required_memo": "ORDER-1001",
  "success_url": "https://merchant.example.com/success",
  "cancel_url": "https://merchant.example.com/cancel"
}
```

`settlement_policy` 可选，接受 `single` 或 `aggregate`。`required_memo` 可选；提供后，最终确定的充值必须携带规范化后的 memo/tag/payment id 才能匹配该支付。

示例：

```bash
BODY='{"order_id":"ORDER-1001","amount":"25.00","currency":"USD","description":"Order #1001","user_id":"customer_42","success_url":"https://merchant.example.com/success","cancel_url":"https://merchant.example.com/cancel"}'
TS="$(date +%s)"
SIG="$(printf "POST\n/api/v1/payment/create\n%s\n%s" "$TS" "$BODY" | openssl dgst -sha256 -hmac "$API_SECRET" -hex | awk '{print $2}')"

curl -X POST "$BASE_URL/api/v1/payment/create" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -H "X-API-Secret: $API_SECRET" \
  -H "X-Gateway-Timestamp: $TS" \
  -H "X-Gateway-Signature: sha256=$SIG" \
  -H "Idempotency-Key: ORDER-1001-create-v1" \
  --data "$BODY"
```

响应结构：

```json
{
  "result": "ok",
  "data": {
    "payment_id": "uuid",
    "track_id": "uuid",
    "session_token": "token",
    "checkout_url": "https://gateway.example.com/checkout/token",
    "status": "pending",
    "expires_at": "2026-06-06T10:00:00Z",
    "order_id": "ORDER-1001",
    "link_type": "fixed",
    "amount": "25.00",
    "currency": "USD",
    "chain_id": 1,
    "symbol": "USDT",
    "token": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
    "decimals": 6,
    "expected_amount_raw": "25000000",
    "deposit_address": "0xDepositAddress"
  }
}
```

创建请求请发送 `Idempotency-Key`。如果旧客户端代码仍使用 `X-Idempotency-Key` 头，请迁移为 `Idempotency-Key`；不要同时发送两者。首次创建返回 `201`；使用相同 key 与相同 payload 重试会以 `200` 返回缓存的创建响应。

幂等冲突示例：使用相同 key 但不同 payload 会返回 `409`：

```json
{
  "result": "error",
  "message": "idempotency key was already used with a different request"
}
```

客户在 `checkout_url` 完成支付。

<a id="create-a-reusable-payment-link"></a>
## 创建可复用支付链接

当商户希望拥有一个由产品定义支撑的可复用 URL 时使用：

```http
POST /api/v1/payment/link/create
```

请求体就是支付链接产品本身。`chain_id`、`symbol` 与可选 `token` 构成可选的默认资产。若省略，托管收银台会展示所有可用资产；例如 `chain_id=99999998`、`symbol=USDT` 并给出 TRON USDT 代币地址时，收银台会以 TRON/USDT 选中状态开始，但仍允许付款人切换资产。

```json
{
  "name": "Order 1001",
  "description": "Product purchase",
  "link_type": "fixed",
  "amount": "25.00",
  "currency": "USD",
  "chain_id": 99999998,
  "symbol": "USDT",
  "token": "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
}
```

支付 Webhook 会在 `product` 下包含原始产品定义。它是会话快照，因此之后对可复用链接的编辑不会改变历史支付事件。通过 `/api/v1/payment/create` 创建的一次性发票没有可复用产品资源，因此会省略该字段。

<a id="create-static-deposit-address"></a>
## 创建静态充值地址

当你的系统希望为每个用户与资产提供永久充值地址时使用。

```http
POST /api/v1/payment/static-address
```

请求体：

```json
{
  "user_id": "customer_42",
  "product_id": "checkout",
  "chain_id": 1,
  "symbol": "USDT",
  "token": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
  "label": "Main wallet"
}
```

响应：

```json
{
  "result": "ok",
  "data": {
    "wallet_id": "uuid",
    "user_id": "customer_42",
    "product_id": "static:1:USDT:token:0xdac17f958d2ee523a2206206994597c13d831ec7:product:checkout",
    "chain": "ethereum",
    "chain_id": 1,
    "symbol": "USDT",
    "token": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
    "address": "0x...",
    "label": "Main wallet",
    "created_at": "2026-06-26T12:00:00Z"
  }
}
```

静态地址在已认证的域/产品/用户/资产范围内是确定性的。资产范围由 `chain_id`、`symbol` 与可选 `token` 构成；对同一域/产品/用户/资产的重复调用会返回既有地址。不支持的链或资产会在创建钱包之前返回 V1 错误信封。

发往该地址的充值由区块链监听器检测，并可根据匹配流程产生交易/支付 Webhook。

<a id="create-wallet-provider-wallet"></a>
## 创建钱包提供方钱包

当你的产品需要 PayHarbor 充当钱包提供方时使用。该端点会为商户用户与产品范围创建或返回一个确定性的多链钱包。

```http
POST /api/v1/wallet/create
```

请求体：

```json
{
  "user_id": "customer_42",
  "product_id": "wallet"
}
```

响应：

```json
{
  "result": "ok",
  "data": {
    "wallet_id": "uuid",
    "user_id": "customer_42",
    "product_id": "wallet",
    "addresses": {
      "ethereum": "0x...",
      "bitcoin": "bc1...",
      "solana": "So...",
      "tron": "T..."
    },
    "created_at": "2026-06-26T12:00:00Z"
  }
}
```

读取钱包数据：

```http
GET /api/v1/wallet/info?wallet_id=<uuid>
GET /api/v1/wallet/info?user_id=customer_42&product_id=wallet
GET /api/v1/wallet/addresses?wallet_id=<uuid>
GET /api/v1/wallet/list?user_id=customer_42
GET /api/v1/wallet/balance?wallet_id=<uuid>
```

钱包提供方的充值按地址匹配，并记账到该钱包的账本。钱包维度余额使用 `GET /api/v1/wallet/balance`，域聚合余额使用 `GET /api/v1/common/balance`。

<a id="query-payment"></a>
## 查询支付

```http
GET /api/v1/payment/info?order_id=ORDER-1001
GET /api/v1/payment/info?track_id=<session_token>
```

支付信息/历史状态：

- `pending`
- `awaiting_payment`
- `paid`
- `expired`
- `canceled`
- `failed`
- `underpaid`
- `overpaid`
- `partial_paid`

普通发票的 `link_type` 为 `fixed`；对付款人在自己钱包中选择加密货币金额的支付链接会话，`link_type` 为 `donation`。

收银台轮询使用：

```http
GET /checkout/{token}/status.json
```

收银台状态响应示例：

```json
{
  "success": true,
  "status": "confirming",
  "paid": false,
  "payment_id": "uuid",
  "tx_hash": "0xhash",
  "success_path": "/checkout/token/return/success",
  "cancel_path": "/checkout/token/cancel",
  "payable": true,
  "terminal": false,
  "payment_outcome": "partial_unsupported",
  "payment_outcome_reason": "partial deposits are not automatically aggregated for checkout settlement",
  "matched_amount_raw": "12500000",
  "shortfall_amount_raw": "12500000",
  "excess_amount_raw": ""
}
```

已文档化的收银台状态值：

- `active`
- `pending`
- `confirming`
- `paid`
- `expired`
- `canceled`
- `failed`
- `underpaid`
- `overpaid`
- `partial_paid`

```json
["active", "pending", "confirming", "paid", "expired", "canceled", "failed", "underpaid", "overpaid", "partial_paid"]
```

对于终态或不应再接受新支付尝试的会话，`payable` 为 false。对于 `paid`、`expired`、`canceled`、`failed`、`underpaid`、`overpaid` 以及非聚合的 `partial_paid` 等面向付款人的最终状态，`terminal` 为 true。聚合型 `partial_aggregating` 会话在支付窗口过期或累计最终确定金额达到终态结果之前保持可支付。

当最终确定的充值产生异常结果时会出现支付结果元数据：

- `payment_outcome`：`exact`、`aggregate_complete`、`partial_aggregating`、`underpaid`、`overpaid`、`partial_unsupported`、`expired_after_deposit`、`wrong_asset`、`wrong_chain`、`missing_memo` 或 `wrong_memo`。
- `payment_outcome_reason`：供商户/操作员跟进的安全说明。
- `matched_amount_raw`：链上收到的原始整数金额。
- `shortfall_amount_raw`：`underpaid` 与 `partial_paid` 的原始整数缺口。
- `excess_amount_raw`：`overpaid` 的原始整数超额。

精确的单笔充值匹配与策略启用的聚合匹配可以变为 `paid`。对启用了聚合的会话，最终确定的充值按原始金额、链、代币身份、精度、地址与要求的 memo/tag 累计；`partial_aggregating` 在支付窗口过期、累计最终确定金额达到预期原始金额或会话变为超额支付之前保持可支付。对非聚合会话，偏低的零散充值按既有收银台策略保持 `partial_unsupported`。任何高于预期的金额都会变为 `overpaid`。错误资产、错误链、缺少 memo/tag、错误 memo/tag 以及过期后才最终确定的充值不会变为 `paid`。

终态收银台示例：

```json
{
  "success": true,
  "status": "paid",
  "paid": true,
  "payment_id": "uuid",
  "tx_hash": "0xhash",
  "success_path": "/checkout/token/return/success",
  "cancel_path": "/checkout/token/cancel",
  "payable": false,
  "terminal": true
}
```

检测到的充值会停在确认门禁之后，直到支付变为 `paid`。如果最终确定的充值随后被重组（reorg）作废，网关会冲正账本分录、把交易标记为 `reorged`，并在配置了回调设置时发出更正 Webhook。

<a id="create-payout-request"></a>
## 创建出账请求

用于从商户储备钱包发起提现请求。请求不会立即广播；需要管理员批准。

```http
POST /api/v1/payout/create
Idempotency-Key: payout-ORDER-1001-v1
```

`Idempotency-Key` 可选但建议提供。首次创建返回 `201`。使用相同 key 与相同 payload 会以 `200` 返回相同出账响应；使用不同 payload 则返回 `409`。

出站安全策略在出账请求持久化之前执行。当平台操作员配置了紧急冻结、地址白名单、单笔原始金额上限与滚动速度上限时，请求可能以 `400` 与 `outbound policy rejects transfer: ...` 消息被拒绝。策略拒绝会记录审计日志，且不会创建账本 hold。

请求体：

```json
{
  "chain": "ethereum",
  "symbol": "USDT",
  "to_address": "0xRecipient...",
  "amount": "10.50",
  "note": "User withdrawal"
}
```

响应：

```json
{
  "result": "ok",
  "data": {
    "payout_id": "uuid",
    "chain": "ethereum",
    "symbol": "USDT",
    "to_address": "0xRecipient...",
    "amount_raw": "10500000",
    "status": "pending",
    "idempotency_key": "payout-ORDER-1001-v1",
    "correlation_id": "req_...",
    "created_at": "2026-06-28T12:00:00Z"
  }
}
```

出账状态：

- `pending`：等待管理员批准。
- `processing`：已批准并广播；等待终局性证据与账本结算。
- `finalized`：已观察到终局性证据，账本借方已结算。
- `approved`：旧版兼容状态；新的终态处理使用 `finalized`。
- `rejected`：被管理员拒绝。
- `failed`：广播前失败，或广播结果不确定后需要操作员对账。

当域配置了 Webhook 时，出账生命周期事件会作为 Webhook 发送：

- `payout.requested.v1`
- `payout.broadcast.v1`
- `payout.finalized.v1`
- `payout.rejected.v1`
- `payout.failed.v1`

轮询仍然支持：

```http
GET /api/v1/payout/info?payout_id=<uuid>
GET /api/v1/payout/history
```

<a id="create-refund-request"></a>
## 创建退款请求

用于对已支付支付单发起退款。链上转账之前需要管理员批准。

```http
POST /api/v1/refund/create
Idempotency-Key: refund-ORDER-1001-v1
```

`Idempotency-Key` 可选但建议提供。首次创建返回 `201`。使用相同 key 与相同 payload 会以 `200` 返回相同退款响应；使用不同 payload 则返回 `409`。

退款请求在创建退款 hold 之前也会按已配置的出站策略检查。在原始发送地址已知时，管理员批准阶段会再次执行目标地址白名单检查。

针对同一支付单的并发退款请求会在单个数据库事务内串行化，因此该支付单的累计退款上限由原子校验保障，竞态请求无法突破。这是**按支付单**串行校验退款上限，并不保证整个数据库完全不会发生锁竞争：若钱包的账本资产暂时被其他操作持有，退款创建会返回 `503`，调用方可稍后以同一 `Idempotency-Key` 重试。

请求体：

```json
{
  "payment_id": "payment_uuid",
  "amount_raw": "25000000",
  "reason": "Customer requested refund"
}
```

替代标识：

```json
{
  "order_id": "ORDER-1001",
  "reason": "Customer requested refund"
}
```

退款状态：

- `pending`
- `processing`：已批准，广播/终局性进行中。
- `succeeded`：已观察到终局性证据，账本退款借方已结算。
- `rejected`
- `failed`

创建退款请求会在账本中预留可退金额。被拒绝的退款或广播前失败的退款会释放该预留。广播成功会记录 `tx_hash`、来源钱包、目标地址、资产元数据与 `broadcasted_at`；只有观察到终局性证据后才会排队终态成功。

当域配置了 Webhook 时，退款生命周期事件会作为 Webhook 发送：

- `refund.requested.v1`
- `refund.broadcast.v1`
- `refund.succeeded.v1`
- `refund.rejected.v1`
- `refund.failed.v1`

轮询仍然支持：

```http
GET /api/v1/refund/info?refund_id=<uuid>
GET /api/v1/refund/history
```

<a id="transaction-rescan"></a>
## 交易重扫

当链上交易已存在但未被处理，或需要重放时使用。

```http
POST /api/v1/transaction/rescan
```

请求体：

```json
{
  "chain": "ethereum",
  "tx_hash": "0x..."
}
```

网关会重新拉取交易，并重放钱包匹配、支付匹配、账本记账与 Webhook 处理。

<a id="webhooks"></a>
## Webhook

网关为每个域使用一个回调 URL。商户集成应按 JSON 体中的 `event_type` 或 `X-Gateway-Event` 头进行路由。

Webhook 出站经过加固：网关从不跟随 HTTP 重定向（`3xx` 响应视为投递失败，因此签名 payload 绝不会被重放到重定向目标），忽略环境中的 `HTTP_PROXY`/`HTTPS_PROXY`/`ALL_PROXY` 设置，并且每次新建连接都会在拨号前重新解析并校验回调目标，因此即便 URL 早先通过校验，解析到私有或其他非公开地址的主机名仍会被拒绝。非生产环境下，本地开发接收端（如 `http://127.0.0.1:3000/...` 等私有/回环地址）仍可使用；生产环境只投递到公开的 HTTPS 目标。

对于 NATS 投递，请以 `notification_mode: "nats"`、`nats_url` 与可选的 `nats_subject`（默认 `gateway.events`）创建或更新域。相同的 JSON 事件体通过 JetStream 发布到该主题；Webhook HMAC 头只在 `webhook` 模式下使用。

<a id="jetstream-provisioning-and-delivery-durability"></a>
### JetStream 预置与投递持久性

NATS 账号所有者必须在启用该域之前预置 JetStream stream。网关只负责发布：它不会创建或更新商户拥有的 stream 或 consumer。stream 主题（或 stream 通配符）必须匹配域配置的 `nats_subject`；否则发布会失败，投递仍保持可重试。

请使用以下生产基线：

- `FileStorage`，使已确认的消息在服务器重启后仍存在。
- `LimitsPolicy` 保留策略，建议 `MaxAge` 为 7 天且绝不短于 24 小时。大小与消息数量上限也必须足够高，避免早于该运营窗口淘汰事件。
- `DuplicateWindow` 为 24 小时。网关把 `Nats-Msg-Id` 设为 `delivery_id`，在较旧/直接调用没有 delivery ID 时依次回退到 `event_id` 与投递幂等键。
- 带显式确认的命名 durable consumer。consumer 应仅在自身处理提交后确认，并应按 `delivery_id` 保持幂等，因为重复窗口之外的重试可能再次投递。

HTTP 与 NATS 通知共用同一个 Postgres `webhook_deliveries` 队列/outbox 边界。HTTP 投递只在收到 2xx 响应后关闭。NATS 投递只在同步 JetStream 发布返回非 nil `PubAck` 后关闭；重复的 `PubAck` 也算成功，因为 JetStream 已持久化该消息 ID。连接、发布、超时、错误确认与确认缺失都会让该行进入正常的重试/死信流程。收到 `PubAck` 后，该行被标记为已投递，网关在普通重试中不会再发布它；此后由 JetStream 负责保留。

这是至少一次（at-least-once）边界。若 JetStream 已持久化消息但确认丢失，网关会用同一 `Nats-Msg-Id` 重试；24 小时重复窗口会消除该歧义。消费者仍必须对超出该窗口的重放与重试保持幂等。

带版本的资金事件目录维护在 `docs/money-event-catalog.md`。它映射当前发出的事件名、旧版下划线别名与规范点分事件名，但不替换下面的示例。

规范点分事件示例包括 `deposit.detected.v1`、`payment.succeeded.v1`、`payment.failed.v1`、`refund.succeeded.v1` 与 `webhook.delivery.failed.v1`。既有的发出名（如 `native_transfer`、`payment_succeeded`、`payment_failed`）在目录中标记为旧版或当前别名时仍作为兼容别名保留。

请求头：

```text
X-Gateway-Event: payment_succeeded
X-Gateway-Event-Id: <event_id>
X-Gateway-Event-Version: v1
X-Gateway-Delivery-Id: <delivery_id>
X-Gateway-Resource-Type: payment
X-Gateway-Resource-Id: <payment_id>
X-Gateway-Sequence: <per-resource-sequence>
X-Gateway-Idempotency-Key: <idempotency_key>
X-Gateway-Timestamp: <unix_seconds>
X-Gateway-Signature: sha256=<hmac_sha256_hex(timestamp + raw_body)>
```

请用域的 `webhook_secret`（而不是 API secret）校验 Webhook。

<a id="webhook-verification-example"></a>
### Webhook 校验示例

```js
import crypto from "crypto";

function verifyWebhook(webhookSecret, timestamp, rawBody, receivedSignature) {
  const sig = receivedSignature.replace(/^sha256=/, "");
  const expected = crypto
    .createHmac("sha256", webhookSecret)
    .update(timestamp)
    .update(rawBody)
    .digest("hex");

  return crypto.timingSafeEqual(Buffer.from(sig), Buffer.from(expected));
}
```

出现以下情况时拒绝 Webhook：

- 时间戳过旧或过于超前。
- 签名无效。
- `event_id` 已被处理。

<a id="delivery-replay-and-deduplication"></a>
### 投递、重放与去重

Webhook 投递是至少一次。重试与操作员重放可能多次投递同一事件。重放会保留原始 event id、event type、event version、商户/域范围与 payload 幂等字段，因此消费者可以安全去重。

对单个 `domain_id` + `resource_type` + `resource_id`，投递包含单调 `sequence`。当该资源较早的 pending、failed 或 processing 投递仍处于活动状态时，网关不会认领更晚的活动投递。消费者仍应保存 sequence，并对同一资源先处理较小的 sequence 值，因为 HTTP 投递是至少一次且客户端恢复可能重试。

请把以下字段用作消费方去重键：

- `X-Gateway-Event-Id` 头与 `event_id` payload 字段。
- `X-Gateway-Idempotency-Key` 头与 `idempotency_key` payload 字段。
- `X-Gateway-Event` / `event_type`。
- `X-Gateway-Event-Version` / `event_version`。
- `merchant_id` 与 `domain_id` 范围。

`delivery_id` 仅用于尝试/重放审计。一次重放可以拥有新的 `delivery_id`，同时保留原始业务 `event_id` 与幂等键。

在履约订单、入账账户或触发不可逆下游工作之前，先持久化去重决定。重放应被视为同一事件的恢复，而不是新的业务事件。

<a id="transaction-webhook-payload"></a>
### 交易 Webhook Payload

事件示例：`native_transfer`

```json
{
  "event_id": "1-0xhash-log:0:native_transfer",
  "event_type": "native_transfer",
  "transaction_id": "uuid",
  "merchant_id": "uuid",
  "domain_id": "uuid",
  "product_id": "static:1:ETH",
  "user_id": "customer_42",
  "wallet_id": "uuid",
  "chain_id": 1,
  "hash": "0xhash",
  "log_index": "log:0",
  "block_number": "123456",
  "block_hash": "0xblock",
  "token": null,
  "symbol": "ETH",
  "decimals": 18,
  "from": "0xSender",
  "to": "0xDepositAddress",
  "amount_raw": "1000000000000000000",
  "status": "confirmed",
  "created_at": "2026-06-06T10:00:00Z"
}
```

交易 Webhook 的 `event_id` 是 `<transaction_unique_hash>:<event_type>`，因此同一交易后续的 `transaction.reorged.v1` 更正会拥有不同的幂等键。重组更正包含 `original_event_id`、`original_resource_id` 与 `correction_reason`，使消费者能把更正关联到先前的交易事件而不删除历史。当重组同时更正支付生命周期时，产生的 `payment.failed.v1` payload 会包含这些关联字段，并在能从保留的支付结果推导时引用先前的支付生命周期事件。

既有的下划线 Webhook 事件名作为兼容别名保留，直到带版本的事件目录迁移明确将其退役。

完整的带版本资金生命周期目录记录在 `docs/money-event-catalog.md`。

<a id="payment-webhook-payload"></a>
### 支付 Webhook Payload

事件示例：

- `payment_succeeded`
- `payment_failed`
- `payment_expired`
- `payment.underpaid.v1`
- `payment.overpaid.v1`
- `payment.partial_paid.v1`

```json
{
  "event_id": "payment_uuid:payment_succeeded",
  "event_type": "payment_succeeded",
  "payment_id": "uuid",
  "session_token": "token",
  "order_id": "ORDER-1001",
  "status": "paid",
  "merchant_id": "uuid",
  "domain_id": "uuid",
  "product_id": "ORDER-1001",
  "user_id": "customer_42",
  "wallet_id": "uuid",
  "amount": "25.00",
  "currency": "USD",
  "link_type": "fixed",
  "chain_id": 1,
  "symbol": "USDT",
  "token": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
  "decimals": 6,
  "expected_amount_raw": "25000000",
  "deposit_address": "0xDepositAddress",
  "payment_outcome": "exact",
  "payment_outcome_reason": "deposit amount exactly matches expected amount",
  "matched_amount_raw": "25000000",
  "shortfall_amount_raw": "",
  "excess_amount_raw": "",
  "product": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "link_token": "b7d9f4b1c6a8e0d2f3a5b6c7d8e9f0a1b2c3",
    "name": "Order 1001",
    "description": "Product purchase",
    "link_type": "fixed",
    "amount": "25.00",
    "currency": "USD",
    "language": "tr",
    "default_asset": {
      "chain_id": 99999998,
      "symbol": "USDT",
      "token": "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
    }
  },
  "tx_hash": "0xhash",
  "tx_unique_hash": "1-0xhash-log:0",
  "created_at": "2026-06-06T09:55:00Z",
  "paid_at": "2026-06-06T10:00:00Z"
}
```

<a id="lifecycle-webhook-payload"></a>
### 生命周期 Webhook Payload

事件示例：

- `payout.finalized.v1`
- `refund.succeeded.v1`
- `sweep.succeeded.v1`

```json
{
  "event_id": "entity_uuid:payout.finalized.v1",
  "event_type": "payout.finalized.v1",
  "event_version": "v1",
  "occurred_at": "2026-06-26T10:05:00Z",
  "entity_type": "payout",
  "entity_id": "uuid",
  "resource_type": "payout",
  "resource_id": "uuid",
  "resource_status": "finalized",
  "idempotency_key": "entity_uuid:payout.finalized.v1",
  "correlation_id": "payout:uuid",
  "merchant_id": "uuid",
  "domain_id": "uuid",
  "wallet_id": "uuid",
  "chain": "ethereum",
  "symbol": "USDT",
  "token": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
  "decimals": 6,
  "amount_raw": "10500000",
  "to_address": "0xRecipient",
  "status": "finalized",
  "tx_hash": "0xhash",
  "broadcasted_at": "2026-06-26T10:03:00Z",
  "finalized_at": "2026-06-26T10:05:00Z",
  "created_at": "2026-06-26T10:00:00Z",
  "updated_at": "2026-06-26T10:05:00Z"
}
```

<a id="recommended-merchant-side-flow"></a>
## 推荐的商户侧流程

<a id="hosted-checkout-flow"></a>
### 托管收银台流程

1. 商户后端用 `POST /api/v1/payment/create` 创建支付。
2. 商户把客户重定向到 `checkout_url`。
3. 客户选择链/资产并付款。
4. PayHarbor 检测区块链交易。
5. PayHarbor 把支付标记为 `paid`。
6. PayHarbor 向域回调 URL 发送 `payment_succeeded` Webhook。
7. 商户校验 Webhook 签名。
8. 商户按 `order_id` 幂等地履约。

<a id="static-address-flow"></a>
### 静态地址流程

1. 商户后端用 `POST /api/v1/payment/static-address` 创建/复用地址。
2. 商户把地址展示给客户。
3. PayHarbor 检测发往该地址的充值。
4. PayHarbor 向域回调 URL 发送交易 Webhook 事件。
5. 商户校验签名，并按 `event_id` 为用户入账。

<a id="payout-flow"></a>
### 出账流程

1. 商户后端用 `POST /api/v1/payout/create` 创建出账请求。
2. 管理员在管理面板批准或拒绝。
3. 已广播的出账在观察到终局性证据之前保持 `processing`。
4. 商户轮询 `GET /api/v1/payout/info`。
5. 商户把 `finalized`、`rejected` 与广播前的 `failed` 视为终态。广播后的不确定状态仍可由操作员恢复。

<a id="refund-flow"></a>
### 退款流程

1. 商户后端用 `POST /api/v1/refund/create` 创建退款请求。
2. 管理员在管理面板批准或拒绝。
3. 已广播的退款在观察到终局性证据之前保持 `processing`。
4. 商户轮询 `GET /api/v1/refund/info`。
5. 商户把 `succeeded`、`rejected` 与广播前的 `failed` 视为终态。广播后的不确定状态仍可由操作员恢复。

<a id="idempotency-rules"></a>
## 幂等规则

商户系统应存储并去重：

- 支付 Webhook：`event_id`
- 交易 Webhook：`event_id`
- 有序 Webhook 处理：`domain_id`、`resource_type`、`resource_id` 与 `sequence`。
- 重放安全的 Webhook 审计：`delivery_id`，存在时加上 `original_delivery_id`。
- 支付创建：使用 `Idempotency-Key` 头，或依赖 `order_id` 回退。
- 出账/退款创建：使用 `Idempotency-Key` 实现重放安全的请求创建，并保存返回的 `payout_id` / `refund_id`。

绝不要仅凭前端返回 URL 履约订单。始终使用 Webhook 校验或服务端支付状态查询。

<a id="contract-evidence---epic-1"></a>
## 契约证据 - Epic 1

合作方 API 契约证据在 `docs/epic-1-integration-evidence.md`。它列出了覆盖的端点、认证模式、幂等行为、静态钱包的域/产品/用户/资产范围、收银台状态语义，以及商户/域与租户/域隔离的已知限制。已知生产限制在下方概述，并在证据文件中展开。

<a id="error-handling"></a>
## 错误处理

常见 API 错误：

```json
{
  "result": "error",
  "message": "invalid request signature"
}
```

HMAC 失败示例：如果 `X-Gateway-Signature` 是基于不同的方法、路径/查询、时间戳或原始请求体计算的，API 会返回相同的 V1 错误信封，消息为 `invalid request signature`。

时间戳与重放失败示例：

```json
{
  "result": "error",
  "message": "timestamp expired"
}
```

```json
{
  "result": "error",
  "message": "request signature replayed"
}
```

同一个 V1 信封用于校验失败、幂等冲突、不支持的资产、过期或不可用会话以及授权失败。错误消息是类别级的，不得暴露 API secret、原始签名、堆栈跟踪，也不得暴露资源是否属于其他租户/域。

Webhook 投递行为：

- PayHarbor 会重试失败的 Webhook 投递。
- PayHarbor 可能以相同 event id/type/version 重放失败或已死信的 Webhook。
- PayHarbor 为活动投递通道保留资源级顺序。
- 商户端点在成功持久化事件后应返回任意 `2xx` 状态。
- 商户端点应保持幂等，因为重试可能发生。

<a id="security-checklist"></a>
## 安全检查清单

- `api_secret` 只保留在服务端。
- `webhook_secret` 只保留在服务端。
- 使用原始请求体字节校验请求/Webhook 签名。
- 拒绝过旧的时间戳。
- 对 Webhook `event_id` 去重。
- 不要处理未签名的回调。
- 不要在前端代码中暴露 API secret。

<a id="known-production-limitations"></a>
## 已知生产限制

Epic 1 加固了合作方接入、托管收银台状态、静态地址范围、幂等与集成证据。它是受控发布证据，不是生产托管或交易所级就绪的声明。

生产托管仍受门禁约束。

Provider 健康指标与就绪检查会暴露 stale/unhealthy RPC 信号以及健康感知的故障转移决策，但它们不是完整的 archive/quorum 交易所基础设施。

生产迁移纪律基于 GORM 并通过 `services/dbmigrations` 版本化；原始 SQL 迁移文件不是本仓库的事实来源。生产就绪要求 `GATEWAY_DB_MIGRATION_VERSION` 与最新工件一致，并通过 `services/database.VerifySchema`。

资金链路可观测性与发布证据记录在 `docs/money-path-observability-runbook.md` 与 `docs/controlled-launch-readiness.md`。

在外部签名器边界、对账任务、账本派生余额、带版本的 GORM 迁移、可观测性/SLO、备份/恢复、合规控制、archive/quorum provider 策略与受控发布门禁全部完成之前，不要把本网关视为生产托管/交易所级就绪。
