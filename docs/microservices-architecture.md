# Gateway Microservice Architecture

> **Status: proposed architecture.** This document describes a target structure for moving the
> current Gateway codebase to a microservice architecture for exchange integration. It is **not**
> a description of deployed microservices: the repository is a modular monolith, and the phase
> list below is a proposal, not a record of completed work. See the root README for the current
> engineering status and limitations.

The goal is to generate a wallet for every user, monitor deposits, execute withdrawals, keep the
ledger balance correct and provide reliable API/event contracts to the exchange.

## Core Objective

Gateway acts as the custody/payment infrastructure for an exchange core:

- Generates a chain/asset-specific deposit address for every exchange user, or returns the existing address.
- Monitors on-chain deposits and credits the ledger balance after finality.
- Accepts withdrawal requests idempotently, runs them through risk/approval/hold, signs and broadcasts them.
- Supports merchant/payment-session usage while ledger and wallet ownership stay centralized.
- Reports status to the exchange through API and event/webhook contracts.

## Service Boundaries

| Service | Responsibility | Data Owned |
|---|---|---|
| API Gateway / BFF | Public REST API, auth, API key validation, request schema, idempotency envelope, rate limit | API request log, idempotency keys |
| Identity & Admin | Admin users, TOTP, operator roles, merchant/exchange tenant access | admins, roles, sessions, audit logs |
| Merchant / Tenant Service | Merchant/domain/API key/webhook settings | merchants, domains, api_keys |
| Wallet Service | User and payment-session wallet generation, address ownership, HD index management | wallets, hd_accounts, address_indexes |
| Signer Service | Transaction signing through KMS/HSM/Vault; mnemonic or private key never lives in the app DB | key references, signing policies |
| Chain Indexer Service | Per-chain block scanning, tx/log detection, chain state, reorg detection | chain_states, raw_chain_events |
| Deposit Service | Matching indexer events to wallets, confirmation gate, payment/deposit state | transactions, deposit_attempts |
| Ledger Service | Double-entry ledger, available/pending/hold balances, balance API | ledger_entries, ledger_accounts |
| Payment Session Service | Invoice/checkout/payment-link sessions, quote snapshot, payment status | payment_sessions, price_quotes |
| Withdrawal Service | Withdrawal request, hold, approval, nonce, broadcast, finalization | withdrawal_requests, withdrawal_txs |
| Webhook Service | Webhook delivery, retry, replay, signing, per-attempt log | webhook_deliveries |
| Pricing Service | Fiat/crypto quote, staleness guard, quote snapshot | price_quotes, price_sources |
| Reconciliation Service | Rescan, missed-tx detection, ledger-chain reconciliation reports | reconciliation_jobs, mismatch_reports |

## Critical Data Ownership Rules

- Only the Wallet Service generates and owns wallet addresses.
- Only the Ledger Service changes ledger balances.
- The Chain Indexer never writes business status; it only produces raw chain events.
- The Deposit Service sends an idempotent credit command to the Ledger Service after a confirmed deposit event.
- The Withdrawal Service cannot sign or broadcast without a hold from the Ledger Service.
- The Signer Service never exposes private keys or mnemonics through HTTP responses, logs or the app DB.
- The Webhook Service revalidates the webhook URL immediately before delivery.
- The Payment Session Service snapshots the price at session time; past sessions are never recalculated from a later oracle price.

## Event Contracts

Kafka, NATS JetStream, RabbitMQ or a Postgres outbox can be used for physical separation. A Postgres outbox is sufficient for the first phase.

| Event | Producer | Consumer | Idempotency Key |
|---|---|---|---|
| `wallet.created.v1` | Wallet Service | Exchange, Deposit Service | `wallet_id` |
| `chain.tx.detected.v1` | Chain Indexer | Deposit Service | `chain_id:tx_hash:log_index` |
| `chain.tx.confirmed.v1` | Chain Indexer | Deposit Service | `chain_id:tx_hash:log_index:confirmed_block` |
| `deposit.detected.v1` | Deposit Service | Webhook, Exchange | `transaction_id` |
| `deposit.finalized.v1` | Deposit Service | Ledger, Webhook, Exchange | `transaction_id:finalized` |
| `ledger.entry.posted.v1` | Ledger Service | Exchange, Reporting | `ledger_entry_id` |
| `payment.paid.v1` | Payment Session Service | Webhook, Exchange | `payment_session_id:paid` |
| `withdrawal.requested.v1` | Withdrawal Service | Risk/Admin | `withdrawal_id` |
| `withdrawal.approved.v1` | Withdrawal Service | Signer Service | `withdrawal_id:approved` |
| `withdrawal.broadcast.v1` | Withdrawal Service | Webhook, Exchange | `withdrawal_id:tx_hash` |
| `withdrawal.finalized.v1` | Withdrawal Service | Ledger, Webhook, Exchange | `withdrawal_id:finalized` |
| `webhook.delivery.failed.v1` | Webhook Service | Ops/Replay | `delivery_id:attempt` |

## Exchange Integration APIs

Gateway should expose a single stable API to the exchange; the number of internal services must not affect the exchange.

### Wallet

- `POST /api/v1/users/{user_id}/wallets`
  - Generates an asset/chain-specific deposit wallet for the user or returns the existing wallet.
  - An idempotency key is mandatory.
- `GET /api/v1/users/{user_id}/wallets`
  - Lists the user's active deposit addresses.

### Deposit

- `GET /api/v1/users/{user_id}/deposits`
  - Returns pending, confirmed and finalized deposit history.
- `GET /api/v1/deposits/{deposit_id}`
  - Returns the status of a single deposit.

### Balance

- `GET /api/v1/users/{user_id}/balances`
  - Returns only available/pending/hold balances sourced from the Ledger Service.
  - Balances are never computed from the chain-scan table or the raw transaction table.

### Withdrawal

- `POST /api/v1/withdrawals`
  - Opens an idempotent withdrawal request.
  - The request is rejected if the ledger hold fails.
- `GET /api/v1/withdrawals/{withdrawal_id}`
  - Returns request, approval, broadcast and finality status.

### Webhook

- `deposit.detected`
- `deposit.finalized`
- `payment.paid`
- `withdrawal.broadcast`
- `withdrawal.finalized`
- `withdrawal.failed`

Every webhook must carry these headers:

- `X-Gateway-Event`
- `X-Gateway-Event-Id`
- `X-Gateway-Timestamp`
- `X-Gateway-Signature`

## Deposit Flow

1. The exchange requests a wallet for the user.
2. The Wallet Service derives the address from the deterministic HD index and publishes `wallet.created.v1`.
3. The Chain Indexer scans the chain and emits `chain.tx.detected.v1` for a tx/log paying the wallet address.
4. The Deposit Service matches the tx to the wallet, performs the duplicate check and records the deposit as `detected`.
5. Once the required confirmations are reached, `deposit.finalized.v1` is emitted.
6. The Ledger Service posts the pending/available entries as double-entry.
7. The Webhook Service notifies the exchange that the deposit is finalized.

Deposit invariants:

- The same `chain_id + tx_hash + log_index` can never create a second ledger credit.
- Available balance does not increase before finality is complete.
- If a reorg is detected, the deposit becomes `reorged` and a ledger reversal entry is written.
- For payment-link sessions the wallet scope must be unique per session; the same static address must not close different open invoices.

## Withdrawal Flow

1. The exchange opens a request with `POST /withdrawals`.
2. The Withdrawal Service prevents duplicate requests by idempotency key.
3. The Ledger Service takes a hold from the available balance.
4. The withdrawal becomes `approved` if risk/admin policy allows.
5. The Signer Service signs the transaction; the private key never leaves it.
6. The Withdrawal Service manages nonce and broadcast.
7. The Chain Indexer monitors the broadcast tx.
8. On finality, the Ledger Service closes the hold and writes the withdrawal finalized entry.
9. The Webhook Service reports the result to the exchange.

Withdrawal invariants:

- No hold, no signature.
- Available balance can never be negative.
- The same idempotency key cannot create a second withdrawal.
- If a broadcast tx is lost, a reconciliation job re-checks it; a blind retry does not create a new tx.
- Nonce management must have a single owner per chain/account.

## Database Separation Strategy

The first step is not physical database separation but clarifying table ownership.

| Schema | Tables |
|---|---|
| `identity` | admins, roles, admin_sessions, activity_logs |
| `tenant` | merchants, domains, api_keys |
| `wallet` | wallets, hd_accounts, address_indexes |
| `chain` | chain_states, raw_chain_events, reorg_events |
| `deposit` | transactions, deposit_attempts |
| `ledger` | ledger_entries, ledger_accounts |
| `payment` | payment_sessions, price_quotes |
| `withdrawal` | withdrawal_requests, withdrawal_transactions |
| `webhook` | webhook_deliveries, webhook_subscriptions |

Rule: a service never writes directly to another service's tables. Even inside the monolith this rule must be enforced through the repository boundary.

## Migration Plan

### Phase 0: Monolith Hardening

- Environment-fallback admin login is disabled.
- Inactive merchant login/API access is blocked.
- Payment-link wallet reuse is removed.
- The balance endpoint returns only ledger available calculations.
- The webhook URL is revalidated at delivery time.
- Portal JWT, rate limit, confirmation gate, quote snapshot and reorg reversal are completed.

### Phase 1: Modular Monolith

- `internal/wallet`, `internal/deposit`, `internal/ledger`, `internal/withdrawal`, `internal/webhook` package boundaries are created.
- Repository access is separated by service ownership.
- A Postgres outbox table is added.
- All money movement runs through outbox + idempotency.

### Phase 2: First Physical Services

Stateless and low-risk pieces are separated first:

1. Webhook Worker
2. Chain Indexer Worker
3. Pricing Worker
4. Reconciliation Worker

After these services are separated, ledger and wallet ownership may still remain in the monolith.

### Phase 3: Money Ownership Separation

1. The Ledger Service is separated.
2. The Wallet Service is separated.
3. The Signer Service is moved behind KMS/HSM.
4. The Withdrawal Service works with signer and ledger through contracts.

No production cutover may happen before integration tests and staging replay are completed for this phase.

### Phase 4: Exchange Scale

- Per-chain indexer autoscaling.
- Distributed rate limit and queue based on Redis/NATS/Kafka.
- Multi-region read replicas.
- Ledger reconciliation dashboard.
- On-call alerting: indexer lag, webhook failure rate, stuck withdrawals, ledger mismatch.

## Pre-Production Blockers

- Portal mutation JWT must be added to admin/merchant forms.
- The chain confirmation gate must be connected to the payment status flow.
- Reorg reversal ledger entries must be written.
- The withdrawal nonce and hold flow must be covered by integration tests.
- Mnemonic/private key must be managed by KMS/HSM/Vault references instead of env.
- API key rate limits must be per-domain/per-key.
- Price quotes must be stored at session time and protected with a TTL.
- Ledger DB constraints must prevent negative available balance and duplicate posting.

## Short Decision

This codebase should not be split directly into "services". First the money-movement invariants must be clarified inside the monolith, then outbox/event contracts must be added, and only then should services be separated physically. Otherwise a microservice architecture does not reduce the number of errors; it increases the risk of distributed money loss.
