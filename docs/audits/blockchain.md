# Blockchain Architecture Audit

> **Historical assessment as of 2026-06-28, not current acceptance or security certification.**
> The findings below record the state of the codebase at that date, including items that have since
> changed (for example how a sweep broadcast is released). They are kept as engineering history and
> must not be read as the current implementation or as a statement that the gaps are fixed. For the
> current engineering status and limitations, see the root README.

Date: 2026-06-28

Role: Senior Blockchain Architect

Scope:
- The Go code in the repository was reviewed.
- Generated/proto/sample code under `third_party/trustwallet/wallet-core/samples/go/**` was not treated as platform implementation; only the behavior the application uses through its `blockchain/walletcore` wrapper was considered.
- This report does not propose source-code changes and does not assume a feature exists that is absent from the code.

## 1. HD Wallet

Current implementation:
- The HD wallet source is the `MNEMONIC_PHRASE` environment variable. `BaseChain.GetMnemonicForPath` performs the signer policy check, then reads and validates the mnemonic from the environment.
- `BaseChain.GetDerivedWallet` derives the address/private key with the Trust Wallet Core wrapper and returns `Address`, `PrivateKey`, `MnemonicPhrase`, `KeyReference`, `DerivationPath` and `SignerMode` inside `WalletDetails`.
- `HDAccountID` is stored at domain level and `HDAddressId` at wallet level. The merchant reserve wallet is created with `HDAddressId = 0`.
- In production mode the software signer is blocked; external signer modes are recognized but no adapter is active and `Authorize` returns an error.

Files:
- `blockchain/basechain.go`
- `blockchain/walletcore/provider.go`
- `blockchain/walletcore/provider_trustwalletcore.go`
- `blockchain/walletcore/provider_fallback.go`
- `repositories/domain_repo.go`
- `repositories/wallet_repo.go`
- `services/signer/policy.go`

Functions:
- `BaseChain.GetMnemonicForPath`
- `BaseChain.GetDerivedWallet`
- `walletcore.DeriveWallet`
- `trustWalletCoreProvider.DeriveWallet`
- `DomainRepo.getNextDomainHDIndex`
- `WalletRepo.getNextHDIndex`
- `WalletRepo.Create`
- `WalletRepo.CreateReserveWallet`
- `signer.Authorize`

Gaps:
- The external signer/KMS/HSM/MPC adapter contract exists, but a real provider implementation and chain-specific external signing do not yet exist.
- There is no xpub/public-only derivation.
- There is no mnemonic rotation, seed versioning or key custody migration flow.
- Derivation metadata is not modeled as a central, versioned path registry.

Risk:
- The mnemonic and private key enter process memory. `WalletDetails` carries the private key and mnemonic.
- If the software signer is disabled in production, the current derivation/signing path may not work; the external signer modes provide no real custody alternative because there is no adapter.
- Managing old addresses deterministically becomes difficult in case of seed/path changes or migration.

Recommendations:
- Keep the software signer for development/test only and add real external signer adapter implementations for production.
- Derive addresses from xpub/public derivation where possible; the private key should be produced only at the signing boundary, or never enter the process at all.
- Store the path scheme, seed/key version and signer mode in a durable, auditable wallet derivation registry.

## 2. Address Derivation

Current implementation:
- EVM and EVM-compatible chains use the `m/44'/60'/{hdAccountId}'/0/{hdWalletId}` path. Ethereum, Base, Arbitrum, Unichain, Avalanche, BNB Chain, Chiliz and Chiliz Spicy derive addresses with the same coin type `60`.
- Bitcoin uses the `m/86'/0'/0'/{hdAccountId}/{hdWalletId}` path. On the Trust Wallet Core side the Bitcoin address is produced with Taproot derivation.
- TRON and TRON testnet use the `m/44'/195'/0'/{hdAccountId}/{hdWalletId}` path.
- Solana uses the `m/44'/501'/{hdAccountId}'/{hdWalletId}'` path.
- The wallet record keeps a column-based address per chain. TRON and TRONTestnet map to the same `TronAddress` field.

Files:
- `blockchain/basechain.go`
- `blockchain/chains/ethereum.go`
- `blockchain/chains/evm_compatible.go`
- `blockchain/chains/bitcoin.go`
- `blockchain/chains/tron.go`
- `blockchain/chains/solana.go`
- `repositories/wallet_repo.go`
- `workers/indexer/address_index.go`

Functions:
- `BaseChain.GetDerivedPath`
- `EthereumChain.CreateHDWallet`
- `EVMCompatibleChain.CreateHDWallet`
- `BitcoinChain.CreateHDWallet`
- `TronChain.CreateHDWallet`
- `SolanaChain.CreateHDWallet`
- `WalletRepo.Create`
- `WalletRepo.EnsureAllAddresses`
- `WalletAddressForChainID`
- `AddressIndex.Load`

Gaps:
- There is no central path registry that makes chain/path/version information mandatory.
- The Bitcoin path is used differently from the BIP86 account/change/index layout; `hdAccountId` is placed in the change segment.
- There is no separate address column or derivation namespace for TRON mainnet/testnet.
- The address derivation fallback build cannot derive addresses; `DeriveWallet` returns an error in the `walletcorefallback` build.

Risk:
- Bitcoin path semantics may be incompatible with standard wallet tooling.
- Because TRON mainnet and testnet use the same address field, testnet/mainnet operational confusion is possible.
- Reusing the same address on multiple EVM chains is a privacy and operational isolation risk.
- Adding a new chain requires multi-point changes across the wallet schema, lookup, repo and factory.

Recommendations:
- Store explicit `derivation_scheme_version`, `purpose`, `coin_type`, `account`, `change` and `index` metadata per chain.
- Separate TRON mainnet/testnet addresses in a chain-id-based normalized address table.
- Re-evaluate Bitcoin path compatibility against the BIP86 standard; if a change is required, do it with a versioned migration.

## 3. Gap Limit

Current implementation:
- No BIP44/BIP86-style gap-limit discovery or seed-restore address scanning implementation exists in the code.
- Address generation is DB-driven: as domain and wallet records are created, `HDAddressId` is allocated, then the chain addresses are derived and written to the wallet table.
- A preload mechanism exists for address lookup; the default `-1` loads all ownership records and makes the index authoritative.

Files:
- `repositories/wallet_repo.go`
- `repositories/domain_repo.go`
- `workers/indexer/address_index.go`

Functions:
- `WalletRepo.getNextHDIndex`
- `WalletRepo.Create`
- `WalletRepo.EnsureAllAddresses`
- `DomainRepo.getNextDomainHDIndex`
- `AddressIndex.Load`
- `addressIndexPreloadLimit`

Gaps:
- There is no unused-address-range scanning.
- There is no address discovery on the chain after a seed restore or DB loss.
- There is no gap limit value, scan cursor or per-chain discovery state.

Risk:
- If a wallet row is absent from the DB, the address is not monitored even if it was derived from the seed.
- Backfill derives addresses only for existing wallet rows; it does not find external or lost index ranges.
- When `ADDRESS_INDEX_PRELOAD_LIMIT` is unset/default `-1`, the complete and authoritative in-memory index is loaded. If `0` or a finite limit is used, the index is not considered authoritative and listeners stay fail-closed instead of producing false negatives or one DB query per event.

Recommendations:
- Add a per-domain/per-chain gap-limit scanner if the product requires it.
- If there is no gap scanner, document it as an explicit invariant: "only addresses already created in the DB are monitored".
- Keep address discovery state and last scanned indexes in a durable table.

## 4. Watcher

Current implementation:
- At startup a listener is created for every chain and subscribed to the dispatcher bus. The bus subscriber accepts only confirmed, positive, platform-owned `To` transfers for a registered asset; it eliminates movement between custody addresses of the same merchant and only then writes a chain fact record.
- The EVM listener scans blocks with HTTP JSON-RPC polling; it produces native tx, ERC-20 log and optional `trace_block` internal transfers.
- The Bitcoin listener scans confirmed blocks and vouts through the Blockstream/mempool.space REST API.
- The TRON listener scans blocks and transaction info through the gRPC fullnode; it processes TRC-20 logs when transaction info is present.
- The Solana listener scans finalized slots/blocks. It produces system transfers; for SPL transfers it strictly resolves token-account owner/mint/program/decimals information from pre/post token balance metadata. Other program call candidates are eliminated before DB persistence in the zero-amount filter.
- Listeners update the `ChainState.LastProcessedBlock` and `LastConfirmedBlock` fields.

Files:
- `main.go`
- `workers/listeners/evm/listener.go`
- `workers/listeners/bitcoin/bitcoin.go`
- `workers/listeners/tron/tron.go`
- `workers/listeners/solana/listener.go`
- `blockchain/basechain.go`

Functions:
- `startChainInfrastructure`
- `handleChainIndexerEvent`
- `recordChainFactObservation`
- `evm.RpcListener.catchUp`
- `evm.RpcListener.processBlock`
- `bitcoin.RpcListener.catchUp`
- `bitcoin.RpcListener.processBlock`
- `tron.RpcListener.catchUp`
- `tron.RpcListener.processBlock`
- `solana.RpcListener.catchUp`
- `solana.RpcListener.processSlot`
- `BaseChain.StartWorkers`
- `BaseChain.Work`

Gaps:
- There is no mempool watcher.
- There is no active reorg rollback/replay at listener level.
- Listeners scan candidate events in a block broadly on the RPC side; the wallet ownership filter is applied at the DB persistence boundary with a complete in-memory address index. If the index is not authoritative, the listener infrastructure stays fail-closed.
- The EVM WebSocket watcher is not used in the startup path. A WS listener exists under `workers/listeners/chiliz`, but `startChainInfrastructure` selects the EVM listener in the default path.

Risk:
- Full block scanning increases RPC and CPU cost on high-volume chains; unrelated candidate events are not written to the DB after the central filter.
- If `LastProcessedBlock` becomes corrupt or reorged there is no automatic rollback.
- Internal transfers may be skipped on EVM providers without `trace_block`; a capability/fetch error is logged, and with `REQUIRE_EVM_TRACE=true` the listener does not advance its checkpoint.
- When there is no starting state, the listener starts near the last safe head by default; past blocks are not scanned automatically.

Recommendations:
- Design a per-chain replay window and reorg-aware checkpoints for the watcher.
- Add address-interest filtering or a staged raw event pipeline.
- Make the provider capability check and trace requirement explicit per chain/asset for EVM.

## 5. Mempool

Current implementation:
- No mempool subscription, pending tx watcher or txpool tracking implementation exists in the code.
- The Bitcoin balance query computes the balance string including `mempool_stats`.
- EVM transfers use `PendingNonceAt` as the nonce source.
- In sweep failure classification the string `"mempool"` is treated as broadcast uncertainty.

Files:
- `blockchain/chains/bitcoin.go`
- `blockchain/chains/evm_transfer.go`
- `main.go`
- `repositories/withdrawal_request_repo.go`

Functions:
- `BitcoinChain.getBalance`
- `evmSendNativeWithClient`
- `evmSendERC20WithClient`
- `sweepFailureBroadcastUncertain`

Gaps:
- No pending deposit detection.
- No pending outbound tx replacement/speed-up/cancel.
- No Bitcoin mempool UTXO tracking; transfer/sweep uses confirmed UTXOs only.
- No EVM txpool reconciliation.

Risk:
- The user gets no pending deposit visibility.
- On outbound broadcast uncertainty the system does not actively verify the tx's mempool state.
- Nonce gap or stuck-transaction situations fall to operator/reconciliation work.

Recommendations:
- Add a chain-based pending tx pipeline if mempool monitoring is required.
- Add a tx replacement policy, fee bump and txpool lookup for EVM.
- If not using unconfirmed UTXOs is a deliberate policy for Bitcoin, document it.

## 6. Confirmations

Current implementation:
- The confirmation requirement can be overridden by env; default values are Bitcoin 3, Solana 1, TRON/TRONTestnet 20, other chains 12.
- The EVM watcher works 12 blocks behind, Bitcoin 6 blocks behind, TRON 2 blocks behind; Solana uses finalized commitment.
- The deposit service computes confirmations from chain state and updates `fact.Finalized = confirmations >= required`.
- The transaction finality worker pulls pending transactions into pending/confirmed state with `MarkFinality`.

Files:
- `main.go`
- `services/deposits/service.go`
- `repositories/transaction_repo.go`
- `repositories/deposit_repo.go`
- `repositories/chain_fact_repo.go`
- `workers/listeners/evm/listener.go`
- `workers/listeners/bitcoin/bitcoin.go`
- `workers/listeners/tron/tron.go`
- `workers/listeners/solana/listener.go`

Functions:
- `chainConfirmationRequirement`
- `transactionConfirmations`
- `finalizePendingTransactions`
- `deposits.Service.factWithFinality`
- `confirmationsForBlock`
- `TransactionRepo.MarkFinality`
- `depositStatusForFinality`
- `ChainFactRepo.Record`
- `ChainFactRepo.RecordOrUpdate`

Gaps:
- There is no chain-specific finality semantics, fork choice or probabilistic finality model; only block height difference is used.
- `ChainFactRepo.Record` does not update confirmations/finality for a duplicate event; only `RecordOrUpdate` does that.
- Confirmation policy and listener safe lag values are not kept in the same central config.

Risk:
- Finality and watcher safe lag settings are disconnected from each other, creating a chain-based operational error risk.
- Confirmation metadata of duplicate chain facts is not updated by the normal listener flow; the deposit service compensates from chain state, but the chain_fact record itself can remain stale.
- The TRON watcher produces events 2 blocks behind while the finality default is 20; this may be correct, but if the policy is not documented explicitly, early event/finality expectations become confusing.

Recommendations:
- Create a per-chain finality policy file/registry.
- Make chain fact confirmation update behavior consistent across listener and rescan paths.
- Model the finality calculation together with block hash/fork state.

## 7. Reorg

Current implementation:
- The transaction repository contains code to catch block identity changes and canonical block conflicts.
- On a conflict the transaction is marked reorged, a ledger reversal is posted, payment/deposit/chain_fact are marked reorged, the related pending/processing/failed sweep jobs are dead-lettered and a reconciliation job is opened.
- Canonical/reorged status fields are used in the block table.

Files:
- `repositories/transaction_repo.go`
- `repositories/chain_fact_repo.go`
- `repositories/deposit_repo.go`
- `models/block.go`
- `main.go`
- `services/deposits/service.go`

Functions:
- `TransactionRepo.Create`
- `observeCanonicalBlockWithDB`
- `markBlockHashConflictsWithDB`
- `markParentHashConflictsWithDB`
- `markBlockRangeTransactionsReorgedWithDB`
- `markTransactionsReorgedWithDB`
- `ChainFactRepo.MarkReorgedByTransactionWithDB`
- `DepositRepo.MarkReorgedByTransactionWithDB`
- `transactionParamFromChainFact`
- `chainFactMetadata`

Gaps:
- No canonical chain reorg detector and rollback at listener level.
- No mechanism to rewind ChainState to the reorg point and replay.
- On the deposit service side `transactionParamFromChainFact` tries to read the parent hash, but `chainFactMetadata` only parses the `from` and `to` fields; the `parent_hash` metadata is not carried into the transaction.
- Reorg detection depends on seeing a new conflicting transaction/fact; there is no independent worker that validates only the block header chain.

Risk:
- Parent hash conflict detection can effectively be disabled on the chain_fact -> transaction path.
- If a reorg removes a deposit transaction but the system never sees a new conflicting event, the old record can remain.
- Even though reconciliation is opened on the sweep/payments side after a reorg, operations may require manual resolution.

Recommendations:
- `chainFactMetadata` should also parse the parent hash and carry it into the transaction param.
- Add a small rolling replay window and canonical parent hash check to listeners.
- Make ChainState rollback and the deterministic replay flow idempotent at repository level.

## 8. Sweeping

Current implementation:
- A durable `SweepJob` is created for a finalized deposit. The reserve wallet is used per merchant with the `_reserve_` domain and `HDAddressId = 0`.
- The sweep worker claims due jobs, creates a ledger sweep hold, re-derives the user wallet, transfers to the reserve wallet address, marks the job succeeded on success and posts the ledger sweep release.
- For token deposits a gas prefund is attempted; then the deposit amount is sent to the reserve address with `WithdrawToken`.
- For native TRON deposits a full native sweep is used with `SweepTo`. On other native chains only the transaction amount is sent with `Withdraw`.
- At chain level there are full native `SweepTo` functions for EVM/BTC/Solana and `SweepERC20To` for EVM/TRON/Solana tokens; the auto-sweep deposit path does not use them for every chain.

Files:
- `main.go`
- `repositories/sweep_job_repo.go`
- `repositories/ledger_repo.go`
- `services/deposits/service.go`
- `blockchain/chains/evm_transfer.go`
- `blockchain/chains/bitcoin_transfer.go`
- `blockchain/chains/tron_transfer.go`
- `blockchain/chains/solana_transfer.go`

Functions:
- `enqueueSweepJob`
- `executeAutoSweepDeposit`
- `executeAutoSweepDepositWithJob`
- `processSweepJobs`
- `scheduleMissingFinalizedSweepJobs`
- `SweepJobRepo.EnqueueForTransaction`
- `SweepJobRepo.ClaimDue`
- `SweepJobRepo.MarkSucceeded`
- `SweepJobRepo.MarkFailed`
- `SweepJobRepo.MarkBroadcastUncertain`
- `deposits.Service.enqueueFinalizedSweepJob`
- `evmSweepNativeTo`
- `evmSweepERC20To`
- `BitcoinChain.SweepTo`
- `TronChain.SweepTo`
- `TronChain.SweepERC20To`
- `SolanaChain.SweepTo`
- `SolanaChain.SweepERC20To`

Gaps:
- No sweep tx finality tracking; the ledger release is posted immediately after a successful broadcast.
- EVM/BTC/Solana native deposit auto sweep does not perform a full balance sweep; it transfers only the deposit amount.
- Token auto sweep is limited to the deposit amount; no full token consolidation is used.
- There is no chain-based confirmation/replacement workflow for a stuck sweep tx after broadcast.

Risk:
- If a sweep tx is broadcast and then fails/reorgs on chain, the ledger release may already have been written before on-chain reality.
- Dust, change or extra balance can remain on user deposit addresses.
- After a gas prefund only 5 seconds are waited; prefund finality/availability is not guaranteed.
- Broadcast uncertainty falls to dead-letter and reconciliation; there is no automatic on-chain confirmation.

Recommendations:
- Bring sweep txs into the normal transaction/finality pipeline too; post the ledger release after finality.
- Make the native and token sweep policy explicit per chain: amount sweep or full consolidation.
- Add a tx hash lookup, replacement and retry strategy for broadcast uncertainty.

## 9. Gas Management

Current implementation:
- EVM transfers use legacy transaction mode and take the gas price with `SuggestGasPrice`. Native gas limit is fixed at 21000, ERC-20 gas limit at 65000.
- The EVM gas policy checks only gas price positivity and the optional `EVM_MAX_GAS_PRICE_WEI` cap.
- The native balance threshold/prefund for EVM token sweep is determined by env.
- The Bitcoin fee rate is env-based: `BITCOIN_FEE_RATE_SAT_PER_VBYTE`; there are min/max caps.
- The TRON TRC-20 fee limit is env-based; the native sweep estimates a bandwidth fee and uses a fallback fee.
- The Solana transfer fee is a fixed env value; default 5000 lamports.

Files:
- `blockchain/chains/evm_transfer.go`
- `blockchain/chains/bitcoin_transfer.go`
- `blockchain/chains/tron_transfer.go`
- `blockchain/chains/solana.go`
- `blockchain/chains/solana_transfer.go`
- `services/chainresource/policy.go`

Functions:
- `evmSendNativeWithClient`
- `evmSendERC20WithClient`
- `evmSignNativeWithTrustWallet`
- `evmSignERC20WithTrustWallet`
- `evmPrefundGas`
- `BitcoinFeeRateSatPerVByte`
- `btcEstimateFee`
- `TronTRC20FeeLimitSUN`
- `TronNativeSweepFeeSUN`
- `tronEstimateBandwidthFeeSUN`
- `SolanaTransferFeeLamports`
- `solanaTransferFeeLamports`
- `PrefundGas`

Gaps:
- No EVM EIP-1559 `maxFeePerGas` / `maxPriorityFeePerGas`.
- No EVM gas estimation (`EstimateGas`).
- No fee bump/speed-up/cancel.
- No Bitcoin dynamic fee estimation and RBF policy.
- No Solana priority fee/compute budget/rent dynamic handling.

Risk:
- A fixed gas limit or static fee can fail or become excessively expensive on congested networks.
- Legacy EVM tx mode is not optimal under some chain conditions.
- The 65000 gas limit may not be sufficient for every token transfer.

Recommendations:
- Add an EIP-1559-capable gas oracle and `EstimateGas` for EVM.
- Add a mempool fee estimator and RBF strategy for Bitcoin.
- Compute priority fee, compute budget and ATA/rent cost dynamically for Solana.

## 10. Nonce

Current implementation:
- EVM nonce reservation is done with maps inside the process-local `chainresource.Manager`. The base nonce is taken with `PendingNonceAt`.
- A nonce is marked consumed even when the broadcast fails.
- Bitcoin UTXO reservation is done with a process-local map; on broadcast failure it is marked consumed with a fallback tx id.
- TRON and Solana sequence leases are modeled as a single active sequence, process-local.

Files:
- `services/chainresource/manager.go`
- `blockchain/chains/chain_resource_policy.go`
- `blockchain/chains/evm_transfer.go`
- `blockchain/chains/bitcoin_transfer.go`
- `blockchain/chains/tron_transfer.go`
- `blockchain/chains/solana.go`
- `blockchain/chains/solana_transfer.go`

Functions:
- `chainresource.Manager.ReserveNonce`
- `NonceReservation.Release`
- `NonceReservation.Consume`
- `Manager.ReserveUTXOs`
- `UTXOReservation.Consume`
- `Manager.AcquireSequence`
- `SequenceLease.Consume`
- `chainResourceSequenceLease`
- `evmSendNativeWithClient`
- `evmSendERC20WithClient`
- `BitcoinChain.sendTo`
- `BitcoinChain.SweepTo`
- `TronChain.sendTRX`
- `TronChain.sendTRC20`
- `SolanaChain.sendLamportsWithClient`

Gaps:
- No durable nonce/UTXO/sequence lock.
- No multi-instance coordination.
- No nonce gap closing through txpool/on-chain reconciliation.
- No EVM replacement transaction lifecycle.

Risk:
- If more than one app instance runs, the same nonce or UTXO can be used in parallel.
- Reservation state is lost on process restart; retrying txs whose broadcast outcome is uncertain becomes complicated.
- Marking a nonce/UTXO/sequence consumed on a broadcast error prevents duplicates inside the process, but if the tx was never broadcast at all, a nonce gap or a locked-UTXO perception can occur.

Recommendations:
- Make nonce/UTXO/sequence reservation durable with DB-based leases and a state machine.
- Add a chain lookup + retry/replacement workflow for broadcast uncertainty.
- If multi-instance operation is targeted, use an advisory lock or row-level lock.

## 11. Recovery

Current implementation:
- The manual tx rescan service can re-read events by transaction hash for EVM, Bitcoin, Solana, TRON/TRONTestnet, record chain facts and run deposit processing.
- Startup backfill derives missing chain addresses for existing wallet rows and fills the lookup table.
- The sweep worker re-enqueues missing finalized sweep jobs.
- A stale processing sweep job is taken to dead-letter/reconciliation as broadcast uncertainty.
- The ledger invariant reconciliation and reserve balance reconciliation workers run.

Files:
- `services/txrescan/service.go`
- `api/handlers/txrescan.go`
- `main.go`
- `repositories/sweep_job_repo.go`
- `services/reconciliation`
- `repositories/wallet_repo.go`

Functions:
- `txrescan.Service.Rescan`
- `txrescan.Service.RescanForMerchant`
- `txrescan.Service.recordRescanFact`
- `txrescan.Service.processDepositFact`
- `backfillMissingAddresses`
- `scheduleMissingFinalizedSweepJobs`
- `processSweepJobs`
- `markSweepBroadcastUncertainAndReconcile`
- `startReconciliationWorker`
- `runLedgerInvariantReconciliation`
- `runReserveBalanceReconciliation`

Gaps:
- No automatic gap-limit recovery.
- No automatic historical rescan/backfill worker; the listener flows forward from the current state.
- No automatic rollback/replay after a reorg.
- No chain discovery outside DB address reconstruction from a mnemonic restore.

Risk:
- A lost or skipped block/event may not be caught without an operator running a manual rescan.
- In seed/DB restore scenarios only the wallet rows in the DB are backfilled.
- Broadcast uncertainty and reorg resolutions may require manual inspection through reconciliation.

Recommendations:
- Make the recovery playbook and operator tooling explicit.
- Add a ChainState replay window and a manual range rescan endpoint.
- Design a gap-limit scanner or an explicit "DB authoritative" recovery model for seed restore.

## 12. Blockchain Abstraction

Current implementation:
- The `blockchain.Chain` interface gathers wallet derivation, transfer, sweep, prefund, address validation, worker lifecycle and batch balance functions in one interface.
- `BaseChain` provides the shared RPC/env/ranker, worker lifecycle and derivation helpers; chain action methods default to `not implemented`.
- `ChainFactory` provides a chain name -> implementation map and alias support.
- EVM-compatible chains perform transfer/sweep through shared helpers.

Files:
- `blockchain/basechain.go`
- `blockchain/factory.go`
- `application/configuration/chains.go`
- `blockchain/chains/evm_compatible.go`
- `main.go`
- `services/txrescan/service.go`
- `repositories/wallet_repo.go`

Functions:
- `Chain` interface
- `BaseChain.RPCs`
- `BaseChain.CreateHDWallet`
- `ChainFactory.RegisterChain`
- `ChainFactory.GetChain`
- `ChainFactory.GetChainByID`
- `ChainFactory.CreateHDWallets`
- `NewChainFactory`
- `startChainInfrastructure`
- `txrescan.Service.rescan`

Gaps:
- Wallet derivation, transfer, watcher and balance capabilities are not split into separate interfaces.
- No chain capability registry.
- Chain-specific behavior leaks outside the abstraction: the TRON native sweep distinction in `main.go`, the chain switch in `txrescan`, the chain column mapping in the wallet repo.

Risk:
- Adding a new chain creates a high blast radius.
- Because the interface is very wide, functions a chain does not support can remain as runtime errors or no-ops.
- Chain-family knowledge is scattered in the code, which can cause policy inconsistency.

Recommendations:
- Split capability interfaces such as `WalletDeriver`, `Broadcaster`, `Watcher`, `FeeManager`, `BalanceProvider`, `SweepProvider`.
- Make the chain config/capability registry the single source of truth.
- Catch unsupported operations with compile-time or startup validation.

## 13. Node Abstraction

Current implementation:
- `BaseChain.RPCs` merges env and default endpoints; if a global `RPCURLRanker` exists it changes the ordering according to provider health results.
- The provider health service probes EVM, Bitcoin, Solana and TRON endpoints, stores stale/inconsistent/unhealthy states as snapshots and uses them in URL ranking.
- Listener and transfer code make their own HTTP/gRPC/ethclient calls directly.

Files:
- `blockchain/basechain.go`
- `services/providerhealth/service.go`
- `repositories/provider_health_repo.go`
- `main.go`
- `workers/listeners/evm/listener.go`
- `workers/listeners/bitcoin/bitcoin.go`
- `workers/listeners/tron/tron.go`
- `workers/listeners/solana/listener.go`
- `blockchain/chains/evm_transfer.go`
- `blockchain/chains/tron_transfer.go`

Functions:
- `BaseChain.RPCs`
- `SetRPCURLRanker`
- `providerhealth.Service.RunOnce`
- `providerhealth.Service.discoverTargets`
- `providerhealth.Service.probe`
- `FinalizeSnapshots`
- `RankURLs`
- `installProviderHealthRanker`
- `runProviderHealthCheck`
- Listener `rpcCall` / `get` / gRPC client functions

Gaps:
- No shared node client interface.
- No method capability model; for example, EVM `trace_block` support is not known at the provider health level.
- No quorum read or cross-provider block hash verification inside the listener flow.
- WebSocket-based subscription is generally not used in the startup path.

Risk:
- Even if provider health ranks URLs, the real capability of each chain method is not separately guaranteed.
- If a provider is missing a method set, event-type loss is possible.
- Direct HTTP/gRPC usage scatters retry, timeout, rate-limit and observability behavior.

Recommendations:
- Create a chain-family-based node client abstraction.
- Add provider capability probes: EVM receipts batch, trace, archive depth, Solana finalized blocks, TRON txinfo.
- Add a provider quorum, or at least a head hash consistency guard, for listeners.

## 14. Multi-chain Design

Current implementation:
- Chain IDs and names are constants in `constants/chains.go`.
- Chain registration happens in `application/configuration/chains.go`: Solana, Ethereum, TRON, TRON testnet, Bitcoin, Avalanche, BNB Chain, Chiliz, Chiliz Spicy, Base, Arbitrum, Unichain.
- EVM-compatible chains share the common `EVMCompatibleChain` and `evm_transfer.go` helpers.
- The wallet model keeps a column per chain; the lookup table provides an additional chain-id/address-based index.
- The asset registry is used on a chain and token basis.

Files:
- `constants/chains.go`
- `application/configuration/chains.go`
- `models/wallet.go`
- `models/wallet_address_lookup.go`
- `repositories/wallet_repo.go`
- `workers/indexer/address_index.go`
- `blockchain/chains/evm_compatible.go`
- `asset`

Functions:
- `AllChainIDs`
- `IsSupportedChainID`
- `ChainName`
- `IsTRONChain`
- `NewChainFactory`
- `WalletRepo.Create`
- `WalletRepo.FindByChainAddress`
- `WalletRepo.EnsureAllAddresses`
- `WalletAddressForChainID`
- `AddressIndex.Load`

Gaps:
- No declarative config for adding a chain; code-level registration and schema mapping are required.
- The wallet address model does not use a normalized `wallet_addresses` table as the primary source; chain columns are still central.
- TRON mainnet/testnet address separation does not exist in the wallet schema.
- Chain-family policies are not in one place.

Risk:
- Adding a new chain requires changes in multiple places such as migration, model, repo, lookup, listener, asset registry and route/admin UI.
- As chain columns grow the wallet model grows and the test load increases.
- Testnet/mainnet separation is not strong enough at column level.

Recommendations:
- Make the normalized `wallet_addresses(wallet_id, chain_id, address, derivation_path, derivation_version)` model the primary source.
- Gather chain registration, finality, derivation, fee, listener and capability settings under a single chain registry.
- Make testnet/mainnet isolation mandatory with chain-id-based address storage and policy.
