# Payment Gateway and Wallet Provider Readiness Report

> **Historical assessment (2026-06-26), not current acceptance or security certification.**
> The findings below record the state of the codebase at that date and are kept for engineering
> history; they are not a current security audit, an approval to operate, or a statement that the
> listed gaps are fixed. For the current engineering status and limitations, see the root README.

Date: 2026-06-26

Scope: the current `gateway` codebase; payment gateway, wallet provider, custody, chain indexing, ledger, webhook, payout/refund and exchange wallet-tracking expectations at Binance scale.

## Final decision

Short answer: no, the codebase does not today satisfy a "flawless" payment gateway and wallet provider requirement.

A more accurate classification:

| Area | Current state | Decision |
| --- | --- | --- |
| Merchant/dealer payment gateway MVP | A real, workable foundation exists | May be suitable for a controlled beta |
| Production-grade payment gateway | Many core pieces exist, but critical operational gaps remain | Incomplete |
| Wallet provider / custodial wallet product | Deposit wallet and ledger foundation exist, custody security is missing | Not production-ready |
| Exchange wallet tracking at Binance scale | Single-process listener and limited backfill model | Not ready |
| Enterprise custody | No KMS/HSM/MPC signer | Not ready |

This repository is not an empty or fake project. Payment sessions, static addresses, multi-chain HD wallets, ledger, webhook, withdrawal and refund flows are built with real code. But the security, resilience, reconciliation, reorg accounting, signer architecture, durable job system and operational control layers required to call it "flawless" or "exchange-grade" are not complete yet.

## Main surfaces reviewed

- Payment session and checkout: `api/handlers/payment.go`, `repositories/payment_repo.go`, `models/payment_session.go`
- V1 API auth, payout, refund, wallet endpoints: `api/handlers/v1api.go`
- Wallet generation and address ownership: `repositories/wallet_repo.go`, `models/wallet.go`
- Chain signer and Trust Wallet Core integration: `blockchain/basechain.go`, `blockchain/walletcore/*`, `blockchain/chains/*_transfer.go`
- Deposit listener and finality: `workers/listeners/*`, `main.go`, `repositories/transaction_repo.go`
- Ledger: `models/ledger_entry.go`, `repositories/ledger_repo.go`
- Webhook: `services/webhook/notifier.go`, `repositories/webhook_delivery_repo.go`
- Withdrawal/refund: `repositories/withdrawal_request_repo.go`, `repositories/refund_repo.go`, `api/handlers/dealer.go`
- Operational documents: `docs/product-readiness-audit.md`, `docs/microservices-architecture.md`, `ROADMAP.md`

## Strengths

1. A real payment session model exists.
   - `models/payment_session.go` stores payment states, selected chain/token, expected raw amount, deposit address, tx hash, confirmation fields and webhook attempt fields.
   - `api/handlers/payment.go` implements payment create, checkout URL, expiry and idempotency flows.

2. API auth and request-signing foundation exists.
   - `api/handlers/v1api.go` resolves the domain via `X-API-Key` or `Authorization: Bearer`.
   - Mutating endpoints verify `X-API-Secret`, the timestamp and the `X-Gateway-Signature` HMAC.
   - `repositories/idempotency_repo.go` returns a conflict when the same idempotency key is reused with a different payload.

3. Multi-chain deterministic wallet generation is real.
   - `repositories/wallet_repo.go` returns a unique wallet per merchant/domain/product/user.
   - HD index allocation uses an advisory lock inside a transaction.
   - Bitcoin, Ethereum, Avalanche, BNB Chain, Base, Arbitrum, Unichain, TRON, Solana and Chiliz addresses are stored in the model.

4. Trust Wallet Core integration exists.
   - `blockchain/walletcore/provider_trustwalletcore.go` uses `TWHDWallet`, `TWAnyAddress` and `TWAnySignerSign`.
   - The fallback provider cannot generate wallet addresses and is unsuitable for production; that is good, because it never silently produces a wrong address.

5. Deposit detection and finality layer exists.
   - Chain listeners scan blocks/slots.
   - `main.go` persists the transaction, matches it to a wallet, computes finality and, on a payment match, runs the ledger and webhook flows.
   - `models/transactions.go` carries confirmation, finalized and reorg fields.

6. Ledger foundation exists.
   - Deposit pending, deposit available, withdrawal hold, withdrawal debit and refund debit entry types exist.
   - `repositories/ledger_repo.go` writes many movements with a two-row accounting approach.
   - Available balance is checked while taking a withdrawal hold.

7. Webhook foundation exists.
   - Payloads are HMAC-signed.
   - Event id, timestamp and event type headers are sent.
   - Delivery attempt and dead-letter-like status fields exist.

8. Payout/refund workflow exists.
   - Payout requests are created through the API, a ledger hold is written, and transfer plus ledger finalization run after admin approval.
   - Refund requests are restricted to paid payments and the refundable amount is checked.

## Payment gateway needs matrix

| Need | Current state | Adequacy | Missing |
| --- | --- | --- | --- |
| Merchant onboarding | Admin/dealer domain, API key, webhook secret exist | Medium | Role separation, key rotation policy, audit hardening |
| API authentication | API key + HMAC signature exist | Good foundation | Standard error contract, key scope, replay telemetry, IP allowlist |
| Payment create | Session, checkout URL, expiry, idempotency exist | Good | Contract tests and API versioning must be strengthened |
| Hosted checkout | Asset selection, QR, websocket status exist | Medium-good | UX/security audit, full mobile/regression tests |
| Quote and pricing | Quote snapshot exists | Medium | Multi-source oracle, staleness/circuit breaker, extreme volatility policy |
| Deposit address | Session wallet and static wallet exist | Good foundation | Invoice-address policy must explicitly cover reuse risks |
| Payment matching | Chain/symbol/token/amount matching with underpayment tolerance | Medium | Underpaid/overpaid/partial-paid lifecycle missing |
| Confirmation gate | Transaction finality fields and worker exist | Medium | Canonical rollback window and block hash continuity missing |
| Ledger posting | Pending/available and reorg reversal entries exist | Medium | On-chain reconciliation and complete rollback workflow missing |
| Webhook | HMAC, retry, delivery log exist | Medium | Event versioning, exponential backoff, merchant diagnostics, replay idempotency SLA |
| Refund | Request and admin approval exist | Medium | Automated policy, refund webhooks and a net lifecycle after chain finality must be strengthened |
| Payout | Hold + admin approval + transfer exist | Medium | Address whitelist, velocity limits, nonce/UTXO reservation, maker-checker policy |
| Rescan | Transaction replay endpoint exists | Good support tool | Must be connected to full block/range replay and reconciliation |
| Observability | Health/readiness and logs exist | Weak-medium | Metrics, tracing, lag, failed job and balance drift alerts missing |

Conclusion: as a merchant payment gateway the MVP level is strong. The biggest production risks are finality/reorg, ledger reconciliation, webhook reliability and payout security.

## Wallet provider needs matrix

| Need | Current state | Adequacy | Missing |
| --- | --- | --- | --- |
| Per-user multi-chain wallet | Deterministic wallet per merchant/domain/product/user exists | Good foundation | Partition/index strategy for millions of users missing |
| Address ownership | Unique address fields exist in the wallet model | Good foundation | Address table normalization and chain-independent lookup must scale |
| HD derivation | Performed with Trust Wallet Core | Good | Key derivation design compatible with an external signer missing |
| Private key handling | Mnemonic/private key derivation inside the process | Critically weak | No KMS/HSM/MPC |
| Deposit balance | Ledger produces balances per wallet/domain | Medium | Liability proof, asset reconciliation, snapshot/close period missing |
| Sweep | Durable `sweep_jobs`, retry/dead-letter, tx-hash guard and reconciliation foundation exist | Medium | Multi-replica resource ownership, replacement evidence and operator recovery UI missing |
| Withdrawal | Admin-approved transfer exists | Medium | Nonce/UTXO lock, address whitelist, risk engine, approval matrix missing |
| Token support | ERC-20, TRC-20, SPL transfer paths exist | Medium | Token registry and chain-specific edge-case tests must expand |
| Custody policy | A reserve-wallet concept exists | Weak | Hot/warm/cold tier, daily limit, freeze, signer audit, segregation missing |
| Exchange user wallet | Static deposit wallet endpoint exists | Medium | Address lifecycle and full audit trail at exchange scale missing |
| Compliance | Not visible in the core product flow | Weak | No AML, sanctions, KYT, travel rule or case management |
| DR/backup | Not explicit in the code | Weak | No seed/key backup, restore drill, signer quorum or runbook |

Conclusion: the codebase has laid a foundation for a "custodial wallet provider", but critical security and operational pieces are missing to be a wallet provider holding customer funds at serious volume.

## Trust Wallet Core and transfer verification

The answer to "are wallets and transfers on all blockchains done through Trust Wallet?" is: wallet generation largely through Trust Wallet Core; transfer signing not through Trust Wallet Core for every chain.

| Chain group | Wallet derivation | Transfer signing | Decision |
| --- | --- | --- | --- |
| Bitcoin | Trust Wallet Core | P2WPKH Trust Wallet Core, manual fallback for the current Taproot sweep | Mixed |
| Ethereum | Trust Wallet Core | Trust Wallet Core native/ERC-20 signing | Yes |
| Avalanche | Trust Wallet Core | EVM Trust Wallet Core path | Yes |
| BNB Chain | Trust Wallet Core | EVM Trust Wallet Core path | Yes |
| Base | Trust Wallet Core | EVM Trust Wallet Core path | Yes |
| Arbitrum | Trust Wallet Core | EVM Trust Wallet Core path | Yes |
| Unichain | Trust Wallet Core | EVM Trust Wallet Core path | Yes |
| Chiliz | Trust Wallet Core | EVM Trust Wallet Core path | Yes |
| TRON | Trust Wallet Core | TRON SDK path | No, derivation only via Trust Wallet Core |
| Solana | Trust Wallet Core | Solana SDK path | No, derivation only via Trust Wallet Core |

This alone is not a fatal bug. Signing through a chain SDK is acceptable. What is fatal for production custody is signing with a mnemonic/private key inside the process. The `SIGNER_MODE=kms/hsm/mpc` options are not a real external signer integration today; they are placeholders that return an "integration required" error.

## Wallet tracking assessment at Binance scale

Minimum expectations for exchange wallet tracking at Binance scale:

- A durable, shardable, horizontally scalable indexer for every chain.
- Deterministic backfill from genesis/configured start block on first setup.
- Block hash parent hash tracking for every block.
- Reorg detection, transaction reversal and ledger reversal.
- RPC node quorum or provider failover quality.
- Partitioned lookup for very large address sets.
- At-least-once event delivery and an idempotent consumer model.
- Alerts for block lag, queue lag, failed tx, stale node and missed blocks.
- UTXO reservation and coin selection on UTXO chains.
- Nonce manager, replacement transaction and stuck-tx recovery on EVM chains.
- A registry and migration policy for token metadata and contract event decoding.
- End-of-day reconciliation between on-chain balance and internal ledger.

Only some of these exist in the current code:

- Listeners exist and can now start from the past with explicit start block/slot env settings. Without that setting the first-start behavior still converges to safe/latest; therefore it does not count as an automatic full-history backfill.
- Per-poll limits are low: EVM 5 blocks, Bitcoin 2 blocks, Solana 8 slots, TRON 10 blocks. Catching up after a long downtime is weak.
- For reorg correction there is same-height block hash conflict detection, a transaction `reorged` status, ledger reversal and a correction webhook foundation.
- `chain_state` keeps the last processed/confirmed block, but block hash continuity and rollback accounting are not production-grade.
- Auto-sweep has been moved to a durable job table and webhook retry backoff has been strengthened. The remaining gaps are the chain event bus, range replay and the fact that some worker flows still run inside the monolith/process.
- Address matching works in practice, but a normalized address index/partition strategy is mandatory for millions of addresses.

Conclusion: in this state it cannot perform exchange wallet tracking at Binance scale. It has a foundation that can be developed into a small/medium-volume merchant gateway or a controlled wallet-provider pilot.

## Critical gaps

### 2026-06-26 Code Fix Update

After this report, the following fixes were applied to the codebase:

- Auto-sweep is no longer a goroutine that disappears. After a finalized deposit, an idempotent job is written to the `sweep_jobs` table; the worker claims it with `FOR UPDATE SKIP LOCKED`, applies exponential backoff on failure, moves it to `dead_letter` after the maximum attempts and stores the successful sweep tx hash.
- Listener first-start behavior was partially fixed. If `CHAIN_<id>_START_BLOCK`, `<CHAIN_NAME>_START_BLOCK`, `START_BLOCK_<CHAIN_NAME>` or `CHAIN_START_BLOCK_DEFAULT` is provided, the listener can start historical scanning from that block/slot.
- Webhook payloads and headers carry event version information: `event_version: "v1"` and `X-Gateway-Event-Version: v1`.
- The transaction/payment webhook retry flow no longer retries immediately on a failed attempt; it uses exponential backoff and `WEBHOOK_MAX_ATTEMPTS` gating.
- A ledger invariant scan was added. If the debit/credit total under the same idempotency key is not zero, a reconciliation job is opened.
- A first protection for reorgs was added. If a different block hash is seen on the same chain and block height, old transactions become `reorged`, are prepared for the `transaction_reorged` webhook, linked ledger entries are reversed with idempotent `reorg_reversal` entries, the linked paid payment is pulled into the `payment_failed` correction webhook, pending sweep jobs are dead-lettered and a reconciliation job is opened.

These fixes improved the durable sweep, webhook hardening, configurable backfill, reorg correction and reconciliation foundation. Some of the P0 items below are no longer "completely missing" but "partially completed". However, a real production signer, canonical parent/child block tracking, a horizontally durable event bus and custody policy still remain open.

### P0 - Must be resolved before holding live customer funds

1. No real production signer.
   - Evidence: `services/signer` defines the external custody adapter contract and the production watch-only boundary, but there is no real provider implementation or chain-specific external signing for `SIGNER_MODE=kms/hsm/mpc`.
   - Risk: Holding custody funds with a mnemonic/process memory is unacceptable.
   - Acceptance criteria: KMS/HSM/MPC signer interface, per-chain signing adapter, audit log, signing without key export, testnet/mainnet shadow test.

2. Reorg-safe indexer partially completed.
   - Current state: If a new block hash is seen on the same chain/block height, old transactions become `reorged`, linked ledger entries are reversed, the linked paid payment is prepared for the `failed` correction webhook and a reconciliation job is opened.
   - Remaining risk: A canonical parent/child block hash chain, a proactive rollback-window scan and fork simulation tests are still missing.
   - Remaining acceptance criteria: Block parent tracking, rollback window processor, deterministic fork simulation, operator reconciliation workflow.

3. Durable backfill and job queue partially completed.
   - Current state: Listeners can now take an explicit start block/slot env setting. Sweep jobs are kept as persistent jobs in the `sweep_jobs` table. Webhook retry backoff was strengthened.
   - Remaining risk: The chain event bus is still inside the monolith/process; full historical range replay, poison queue and an operator replay UI are not fully productized.
   - Remaining acceptance criteria: Durable chain event queue, range replay worker, poison queue, operator replay UI.

4. Ledger reconciliation partially completed.
   - Current state: A ledger invariant checker was added; a reconciliation job is opened for idempotency keys with a broken debit/credit total.
   - Remaining evidence: On-chain balance comparison, reserve proof and drift alerts are still missing.
   - Risk: The balance may look correct while the on-chain funds differ.
   - Remaining acceptance criteria: Daily on-chain reconciliation job, per-asset liability report, on-chain reserve proof, negative balance guard, dashboard/alert.

5. Withdrawal security was strengthened, but is not complete at exchange level.
   - Current state: In addition to hold and admin approval, outbound policy setting/whitelist tables, emergency freeze, raw amount limit, rolling velocity limit, high-risk admin role guard and an append-only activity log were added.
   - Remaining gaps: Risk score, nonce/UTXO reservation and an advanced approval matrix are still missing.
   - Risk: Wrong/adversarial payouts, double-spend-like concurrency, stuck transactions.
   - Remaining acceptance criteria: Risk scoring, nonce/UTXO lock, stuck tx replacement.

6. Auto-sweep was moved to a durable job.
   - Current state: `sweep_jobs` status machine, retry, lock, idempotency, dead-letter, bounded failure category, sweep tx hash record and parent-job prefund attempt/error/category state were added.
   - Remaining risk: Gas prefund is not a separate idempotent child job; the admin recovery screen and multi-replica durable resource ownership still need strengthening.
   - Remaining acceptance criteria: Admin recovery screen, durable multi-replica nonce/UTXO/resource locking, operator replay/reconcile workflow.

### P1 - Required for production readiness

1. Webhook event contract versioning.
   - Current state: A `v1` event version was added at payload and header level.
   - Remaining: Version catalog documentation, backwards-compatibility policy and consumer contract tests.

2. Webhook retry quality.
   - Current state: Exponential backoff and max-attempt gating were added.
   - Remaining: Per-merchant rate limit, replay audit and merchant diagnostics.

3. Underpaid/overpaid/partial-paid lifecycle.
   - Amount matching is tolerant today, but underpayment is not strongly modeled as a separate event/status.

4. Price oracle hardening.
   - A quote snapshot exists, but multi-source pricing, staleness guard, volatility freeze and oracle outage policy must reach production level.

5. API contract stability.
   - V1 endpoints exist, but OpenAPI contract tests, a backwards-compatible error envelope and a deprecation policy are mandatory.

6. Observability.
   - Metrics for chain lag, listener health, block processing latency, webhook failure rate, sweep backlog, ledger drift and signer latency are missing.

7. Database migration and constraints.
   - GORM model indexes exist, but production migration files, check constraints, partial indexes and an online migration strategy must be strengthened.

8. Test coverage.
   - Unit tests exist, but chain integration, fork/reorg simulation, webhook retry, withdrawal concurrency, ledger invariant and crash recovery tests must be increased.

### P2 - Scale and enterprise productization

1. Chain indexer microservice separation.
   - `docs/microservices-architecture.md` describes the right target. In practice the chain indexer, wallet, ledger, webhook and payment session services should be separated gradually.

2. Address index normalization.
   - The chain-specific columns in the wallet model are fast for an MVP, but at multi-chain/multi-token/multi-address scale a normalized table such as `wallet_addresses` is more correct.

3. Compliance and risk integrations.
   - AML/KYT provider, sanctions screening, travel rule, suspicious activity workflow, case management.

4. Tenant isolation.
   - Domain/merchant scope exists, but enterprise use needs per-tenant rate limits, quotas, data export, audit isolation and an encryption policy.

5. Reporting.
   - Merchant settlement report, tax/accounting export, liability/reserve report, failed payment/refund/payout report.

## Where the product really stands today

### As a payment gateway

Realistic position: controlled beta / limited production candidate.

Conditions of use:

- Small volume.
- Limited merchants.
- Limited chain/token set.
- Operator supervision.
- Manual reconciliation.
- Low custody balance.
- Testnet and low-amount mainnet pilot.

P0 items must be completed before accepting live high-volume payments.

### As a wallet provider

Realistic position: developer preview / internal pilot.

The most critical gap for a wallet provider is custody security. Wallet generation and deposit detection alone are not enough to be a wallet provider. A wallet provider holds funds, signs transfers, manages charge/dispute/refund/payout, verifies on-chain reserves against the ledger daily and signs without exposing keys to the operator's process memory.

This codebase is moving toward that target, but it is not a production custody product today.

### As exchange wallet tracking at Binance scale

Realistic position: not ready.

An exchange expects a "no missed events" guarantee across millions of addresses and high-volume deposit/withdrawal flows. The current listener architecture is single-process, with low batch limits and insufficient historical scanning/reorg handling. Exchange scale requires a separate indexer service, a durable queue, a block hash ledger, reorg rollback and partitioned address matching.

## Acceptance criteria

To call the payment gateway production-ready:

- P0 signer, reorg, durable queue, ledger reconciliation and withdrawal policy items are complete.
- Testnet E2E on all supported chains: payment create -> deposit -> finality -> ledger -> webhook.
- Mainnet canary: small limits, automatic alerts, rollback runbook.
- Webhook replay and dead-letter operator screen.
- Price oracle staleness and fallback policy.
- API contract tests mandatory in CI.
- `go test ./...`, `go vet ./...`, race/concurrency critical tests mandatory in CI.

To call the wallet provider production-ready:

- KMS/HSM/MPC signer active.
- Private key/mnemonic is not kept inside the application process.
- Deposit, sweep and withdrawal run through durable job state machines.
- Per-wallet/per-chain nonce and UTXO reservation exist.
- Hot/warm/cold wallet policy and emergency freeze exist.
- Daily asset liability/reconciliation report exists.
- Admin actions are tracked with an immutable audit log.
- KYT/AML/sanctions integration exists, or there is a written policy that it is out of product scope.

To be able to call it tracking at Binance scale:

- The chain indexer scales horizontally.
- It performs deterministic backfill from a configured historical start block.
- Reorg simulation tests pass.
- Address lookup is partitioned and benchmarked with millions of addresses.
- Node failover/quorum exists.
- The event bus is durable.
- Block lag and missed block alerts exist.
- Ledger drift zero-tolerance alerts exist.

## Recommended roadmap

### First 2 weeks

1. Design the external signer interface: chain-specific request/response models such as `SignEVM`, `SignBitcoin`, `SignTRON`, `SignSolana`.
2. Move auto-sweep to a durable job table. Status: completed.
3. Add configurable start block to listeners. Status: completed; the range replay worker is still open.
4. Add a ledger invariant checker. Status: base checker completed; comprehensive invariant tests are still open.
5. Freeze the webhook event schema as `v1`. Status: payload/header completed; contract tests and catalog are still open.

### 30 days

1. Reorg tracking: transaction reorg status, ledger reversal and correction webhook foundation completed; block hash chain and rollback window open.
2. Nonce/UTXO reservation: EVM nonce manager, Bitcoin UTXO lock, stuck tx replacement.
3. Sweep state machine: pending, processing, succeeded, failed, dead-letter. Status: foundation completed; prefunding parent-job attempt/error/category state added, separate child job and operator UI open.
4. Reconciliation job: ledger invariant job foundation completed; on-chain reserve/user wallet balance comparison open.
5. Observability: Prometheus metrics and alert rules.

### 60 days

1. KMS/HSM/MPC signer integration live on the first chains.
2. Payout policy engine: limits, whitelist, dual approval, freeze.
3. Event contract and DB boundary for chain indexer service separation.
4. Large wallet set benchmark: address matching, listener lag, DB indexes.
5. API versioning/deprecation and SDK/integration guide hardening.

### 90 days

1. Multi-chain production signer rolled out to all supported chains.
2. Reorg/backfill/failover chaos tests in CI and staging.
3. Merchant settlement and liability reporting.
4. AML/KYT/compliance integrations, or a product scope document.
5. Operational runbook and incident drills for a controlled production launch.

## Conclusion

This codebase has established a solid MVP foundation for a payment gateway. In particular, payment sessions, wallet generation, idempotency, HMAC auth, ledger entries, webhook and payout/refund flows point in the right direction.

After the latest fixes, the durable sweep, webhook retry, event versioning, configurable listener start block, reorg ledger reversal and ledger invariant reconciliation foundation were strengthened. Nevertheless, the product is still not at a "flawless" or "Binance-level wallet tracking/custody" level. The most critical gaps remain in the production signer, canonical rollback-window indexer, durable chain event bus, withdrawal risk policy, advanced fee/nonce/UTXO management and on-chain reserve reconciliation. Live customer funds at high volume must not be accepted and the product must not be marketed as "wallet provider custody" until these items are complete.
