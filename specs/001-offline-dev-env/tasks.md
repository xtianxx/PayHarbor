---

description: "ENV-02 implementation tasks for the offline development environment"
---

# Tasks: Offline Development Environment

**Input**: Design documents from `/specs/001-offline-dev-env/`

**Prerequisites**: `plan.md`, `spec.md`

**Status legend**

- `[x]` done **and verified in this phase** (command/evidence recorded in the phase report)
- `[x]*` historical marker from ENV-02: authored while the verification command still required the native Wallet Core library. Every such item was executed in ENV-03 and re-marked `[x]`; no item uses this marker anymore.
- `[ ]` not done in this phase

## Phase 1: Setup (Shared Infrastructure)

- [x] **T001** Spec Kit initialized in-repo: `specify init --here --force --non-interactive --integration opencode` (adds `.specify/`, `.opencode/`)
- [x] **T002** Pinned Wallet Core submodule initialized (`third_party/trustwallet/wallet-core` @ `4edc4a142b7869201ee5b24ea92f939c617387c6`)
- [x] **T003** [P] Vendored build scripts inspected (`tools/install-sys-dependencies-linux`, `install-dependencies`, `install-rust-dependencies`, `download-dependencies`, `generate-files`, `rust-bindgen`) and CMake artifact forms confirmed (standalone Linux builds a **static** `libTrustWalletCore.a`; `libwallet_core_rs.a`, `libTrezorCrypto.a`, `libprotobuf.a` are static too; public headers never include protobuf headers)
- [x] **T004** [P] Runtime asset requirements confirmed (`views/` templates and `static/` are read from disk; `views/assets/*.css` are committed) and no startup goroutines exist outside `main.go`

## Phase 2: Foundational (Blocking Prerequisites)

- [x] **T005** `helpers/offline.go`: `GATEWAY_OFFLINE` truthiness, explicit `APP_ENV=development` requirement, `OfflineModePolicyError()` fail-closed, single `OfflineModeEnabled()` source of truth
- [x] **T006** `main.go`: extracted `gatewayPeriodicWorkerTasks(notifier)` and added the `offline` parameter to `buildGatewayWorkerSupervisor` (default task set unchanged)
- [x] **T007** `main.go` seam: fail-closed policy gate before `NewApp`, listeners skipped, address backfill/preload skipped, HTTP + migrations + wallet initialization untouched
- [x] **T008** `api/handlers/v1_readiness.go`: explicit `offline.mode` check with `ok=false` so readiness can never be reported green in offline mode

## Phase 3: User Story 1 — Chain-free local runtime switch (P1)

- [x] **T101** `helpers/offline_test.go` policy matrix — executed: `go test ./helpers -run Offline -count=1` (17 subtests pass)
- [x] **T102** `main_offline_seam_test.go`: offline supervisor registers zero tasks; default supervisor registers exactly the nine names; `gatewayPeriodicWorkerTasks` covers them all. (Marked `[x]*` in ENV-02; executed in ENV-03 as part of `go test ./...` — see E314.)
- [x] **T103** `api/handlers/v1_readiness_offline_test.go`: offline marker present/not-ready when enabled, absent when disabled or failing closed. (Marked `[x]*` in ENV-02; executed in ENV-03 as part of `go test ./...` — see E314.)
- [x] **T104** Startup logging that states listeners and workers are disabled and never claims listeners started (source reviewed; runtime observation in ENV-03)
- [x] **T105** Existing `main_worker_supervisor_contract_test.go` tokens preserved unchanged (source review in ENV-02); `go vet .` and the contract test executed in ENV-03 during `go vet ./...` + `go test ./...` — see E314.
- [x] **T106** Offline-mode behaviour matrix documented in `docs/offline-development.md` (disabled vs still-active surfaces)

## Phase 4: User Story 2 — Isolated container environment and safe secret bootstrap (P2)

- [x] **T201** `Dockerfile`: three stages (`walletcore` → `gobuild` → `runtime`), pinned Go 1.25.4 with checksum verification, `GOTOOLCHAIN=local`, `CGO_ENABLED=1`, no build tags, build-time binary smoke check + `ldd` check, non-root runtime user
- [x] **T202** `.dockerignore`: VCS state, `.env*`, node modules, tmp/cache, previous Wallet Core build outputs (source stays in context)
- [x] **T203** `deploy/compose/offline/docker-compose.yml`: project `gateway-offline`, named volume + networks, `internal: true` network for app + database only, loopback ports `127.0.0.1:55432` / `127.0.0.1:3001`, no RPC/OIDC/custody configuration
- [x] **T204** `scripts/generate-offline-env.sh`: create-once (`O_EXCL`), mode `0600`, random mnemonic/master/JWT/admin/database values, BIP39 derived from the pinned submodule wordlist and validated against the submodule's official vectors, no secret output, `--verify` self-test
- [x] **T205** `.gitignore` review: no new ignore rules needed; `git check-ignore -v .env` confirms the generated file is ignored
- [x] **T206** Isolation design corrected after measurement: the app and database cannot publish their own ports while being internal-only, so a dedicated inert `loopback` relay service (pinned `alpine/socat:1.8.1.3`) publishes both loopback ports; measured evidence recorded in `plan.md`

## Phase 5: User Story 3 — Documented local workflow (P3)

- [x] **T301** `docs/offline-development.md`: quick start, start/stop/logs/test/shell command table, disabled vs active surfaces, limitations, safety rules, ENV-03 handoff

## Phase 6: Verification (this phase)

- [x] **T401** `sh -n` for `scripts/generate-offline-env.sh`, `scripts/build_wallet_core.sh`, `scripts/regression.sh` — all OK
- [x] **T402** `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml config` exits 0; structure shows loopback-only ports, `internal: true` network, named volume
- [x] **T403** Env generator executed for real (scratch path and repo path): mode `0600`, second run refuses and leaves the file byte-identical, zero secret values in stdout/stderr, generated mnemonic BIP39-valid and not a public vector
- [x] **T404** `scripts/generate-offline-env.sh --verify` — PASS against 24/24 official BIP39 english vectors shipped by the pinned submodule
- [x] **T405** `GOTOOLCHAIN=local CGO_ENABLED=1 go test ./helpers/... -count=1` (56 pass) and `go vet ./helpers/...` (clean)
- [x] **T406** Network-isolation measurements: internal-only network blocks egress but cannot publish ports; `enable_ip_masquerade=false` still allowed egress on this host; relay design verified end-to-end (host → `127.0.0.1:55432` → relay → internal-only PostgreSQL)
- [x] **T407** `docker build` was deferred in ENV-02 and **executed in ENV-03** (see below); the static lint alone was never treated as native-build success.

## ENV-03 Execution — native build, migration, offline runtime, test suite

Legend: `[x]` executed with recorded evidence · `[ ]` not done · evidence lives in
`/tmp/opencode/gateway-env03/` (logs + report; no secret values).

### Build

- [x] **E301** `BUILD_JOBS` (default 4) replaces `$(nproc)` for the final native compile; also sets `CMAKE_BUILD_PARALLEL_LEVEL` and `CARGO_BUILD_JOBS`. Measured peak host memory **11.4 GiB of 15.9 GiB** (minimum free 213 MiB) — the previous 16-way default was unsafe on this host.
- [x] **E302** Build-tool gap (root cause 1, single attempt): `unzip` was missing in the `walletcore` stage, needed by `tools/download-dependencies` for the nlohmann/json archive. Added to the apt list with a comment. Audited the rest of the native path: `jq`/`zip`/`bc`/`doxygen` belong to coverage/wasm/iOS scripts only, `perl` is already in the base image.
- [x] **E303** Full image build: attempt 1 failed at the unzip step (742 s, exit 1); attempt 2 exit 0 in **1915 s (~32 min)**, log shows `[100%] Built target TrustWalletCore` plus `libTrustWalletCore.a` and `libwallet_core_rs.a` in the slim artifact tree. No `walletcorefallback` anywhere. Runtime image `gateway-offline:local` (sha256:ac317d34…, refreshed to sha256:fcbd42aa6865 after the sidecar regeneration).
- [x] **E304** Reproducible test entry: `--target gotest` image (`gateway-offline:test`, sha256:cc91a059… → refreshed sha256:8b058987a297) runs `go build` / `go vet` / `go test` with no host toolchain.

### Assets

- [x] **E305** `npm ci` exit 0; `npm run build:css` exit 0 produced a 248-byte diff in the committed `views/assets/tailwind.css`. Verified **pure generation**: every added class is referenced by the 19 templates (1–10 files each) and the removed `xl:grid-cols-[minmax(0,480px)_1fr]` has zero references, so the committed artifact was stale. No HTML, Tailwind config, or input CSS was touched.
- [x] **E306** Regenerated `views/assets/tailwind.css.fiber.br` at brotli **quality 11** (matched the committed sidecars' size profile: home.css 3534 ↔ 3534, checkout.js 2939 ↔ 2938). All 29 sidecars verified round-trip-equal; `TestFiberBrotliSidecarsMatchSources` passes.

### Runtime

- [x] **E307** `docker compose up -d` exit 0; startup migration ran automatically (`Migration:BEGIN/END`, 39 tables, `uuid-ossp` present). `chain_states` is **0**, which is expected in offline mode: those rows are inserted by the listener path, which offline mode skips.
- [x] **E308** Offline contract in logs: four `Offline mode:` lines, zero listener-start lines, `worker supervisor started with 0 periodic tasks`.
- [x] **E309** HTTP: `/` → 200; `/assets/tailwind.css` → 200 byte-identical to the repository file; readiness → **503** with `offline.mode ok=false` ("offline development mode is active"), 46/59 checks ok, using a dev-only API key provisioned for verification.
- [x] **E310** Isolation: gateway runs as uid 10001 (`gateway`); public-IP egress probe `http://1.1.1.1` fails (`Couldn't connect to server`), DNS fails, database reports `Network unreachable`, and the relay control reaches the same IP (probe methodology validated). Host binds only `127.0.0.1:3001` and `127.0.0.1:55432` (0 non-loopback); the gateway and database publish nothing themselves.
- [x] **E311** `sweep_jobs=0`, `outbound_transactions=0`, `transactions=0`.
- [x] **E312** Persistence: a marker row plus schema/merchant/domain counts survived `compose stop` → `compose start` (containers exited cleanly, restarted with the same IDs); services left running.
- [x] **E313** Evidence bundle under `/tmp/opencode/gateway-env03/` (41 files, secret scan clean; the provisioned API key is a mode-600 file).

### Tests

- [x] **E314** In the test image against the isolated `gateway_test` database (first pass): `go build ./...` exit 0 (48 s), `go vet ./...` exit 0 (47 s), `go test -p=1 -count=1 ./...` → **42 packages ok, 1 package failing** (`core/repositories`; resolved by E401 — see D005 for the final all-green run).
- [x] **E315** Diagnosis recorded during the first full-suite pass: the two failures are test-side assertions comparing nanosecond `time.Now()` values with PostgreSQL round-tripped microsecond values (`WebhookSentAt`, `DeliveredAt`), unrelated to ENV-02/03 code, and invisible in CI because `.github/workflows/regression.yml` starts no PostgreSQL service so every Postgres-gated test skips. At that point the tests were left unmodified; **E401 later applied the minimal fix** (microsecond-exact seeds, strict assertions preserved) and both tests now pass.
- [x] **E316** `scripts/regression.sh` cannot run on the host (it installs system packages and rebuilds Wallet Core there). Its native path is exactly `scripts/build_wallet_core.sh` + `go test -p=1 -count=1 ./...` + `go vet ./...`; the build ran in the image's `walletcore` stage and test/vet ran in the `gotest` image, so no expensive work was repeated on the host.

### Follow-ups

- [x] **E401** Applied the minimal test-side fix for the two microsecond-precision assertions: the seeds now use `time.Now().Truncate(time.Microsecond)` (with a comment explaining PostgreSQL's microsecond storage) in `repositories/payment_repo_test.go` and `repositories/webhook_delivery_repo_test.go`. The strict equality assertions were kept intact, and both tests now PASS in the test image.
- [x] **E402** Documented and tooled the asset workflow: `scripts/brotli-sidecars/main.go` regenerates/verifies `.fiber.br` sidecars at quality 11 using the module's existing brotli dependency (`--verify` audits all 29 sidecars; verified mismatches=0), and `docs/developer-guide.md` now lists the regeneration step plus the PR-checklist item. Note recorded in both the tool header and docs: another brotli implementation can produce different bytes for the same source, so regenerate only assets you changed (`TestFiberBrotliSidecarsMatchSources` checks decode equality).

## ENV-04 Execution — documentation and status corrections

Docs/status only: no runtime code change, no native rebuild, no re-run of the full suite
(none needed — see below). D007 remains the supervisor's verdict.

- [x] **F401** `docs/offline-development.md` quick start now shows the real behaviour: an unauthenticated readiness call returns **401 by design**, and the offline acceptance signal is the **authenticated 503** carrying `offline.mode ok=false`. Added a "Where the development API key comes from" section (merchant portal registration → domain creation in the dashboard → copy the domain API key; default scopes include `read`), with a non-leaking pattern (`read -rs GATEWAY_DEV_API_KEY`, value never echoed or in history), and an explicit note that a one-off verification key under `/tmp` is throwaway evidence, not configuration. Every "always 503" phrasing was rewritten to the authenticated-caller wording (limitations section and the verified-behaviour table).
- [x] **F402** Removed the incorrect advice that `docker compose config --no-interpolate` protects `env_file` secrets: measured that it still prints them. The docs now recommend `config --quiet` (exit status only) or a fully filtered projection, and the command-reference row uses `--quiet`.
- [x] **F403** Scoped the egress claims: README now says "no outbound traffic from the application and database containers" (the relay has egress but no credentials), and the compose header explains that the internal-network port-publishing limitation is **host-measured on this WSL2/Docker 29.6.1 setup**, not a general Docker guarantee.
- [x] **F404** Status corrections: D005 now records the final all-green result with the log path; E315 notes it was the diagnosis at the time and that E401 fixed it; T102/T103/T105 moved from the ENV-02 `[x]*` marker to `[x]` because they executed in ENV-03 (historical wording kept in parentheses, legend updated).
- [x] **F405** `specs/001-offline-dev-env/plan.md` synced with the delivered work: four build stages including `gotest`, the sidecar tool added to the source tree, and the dependency statement clarified (no new Go/npm dependencies; the compose stack adds one pinned auxiliary `alpine/socat` relay image).
- [x] **F406** Verification of the documented commands: unauthenticated readiness `401`, authenticated readiness `503`, `docker compose config --quiet` exit 0 with no output, `--no-interpolate` shown to leak env values (evidence for removing the advice). No `.env` modification, no merchant/domain creation, no commit.

**Documentation-only changes need no image rebuild**: `docs/`, `README.md`, `specs/` and the
compose comments are not part of the runtime image (`docs/` is not copied; no code reads it
from disk), and no asset under `views/` or `static/` changed in ENV-04, so the running stack
still matches the image.

## Deferred task status

- [x] **D001** Native build executed and verified (`Built target TrustWalletCore`, static archives present)
- [x] **D002** `go build ./...` (exit 0) with `CGO_ENABLED=1` and no build tags; the offline seam tests compiled and ran inside `go test ./...`
- [x] **D003** Compose stack started; startup migration verified (39 tables, `uuid-ossp`); `chain_states=0` documented as expected in offline mode
- [x] **D004** Offline startup smoke verified end to end (HTTP 200, readiness 503 with `offline.mode`, zero listener logs, zero `sweep_jobs`/`outbound_transactions`)
- [x] **D005** Full suite executed. First pass: 42 packages ok, 1 package failing on two pre-existing test-side precision assertions; after the E401 fix the **final** pass is all green — `go build ./...`, `go vet ./...`, `go test -p=1 -count=1 ./...` all exit 0 with **43 packages ok, 0 failures** (`/tmp/opencode/gateway-env03/logs/final-go-test.log`, 295 s). No regression from this work.
- [x] **D006** Runtime image verified: healthy, non-root uid 10001, egress blocked, loopback-only ports, persistence across restart
- [x] **D007** Independent acceptance by the supervisor (2026-10-12): reviewed the Git diff, native build logs and final 43-package passing test log; verified all 13 selected source/asset files match the tested image. Independently observed portal HTTP 200, unauthenticated readiness 401, authenticated readiness 503 with `offline.mode=false`, uid 10001, internal-only application networking, blocked public-IP egress with a successful relay control, loopback-only ports, and zero sweep/outbound/transaction rows. Compose validation and `git diff --check` pass; ENV-04 documentation corrections accepted. No commit, push, or merge performed.

Acceptance scope: the local offline development environment only. Money-path HTTP endpoints remain registered; mainnet connectivity and production custody are not certified. The relay has outbound access; the application and database do not. Full-suite evidence is reused from the matching test image rather than repeating the expensive native build.

## WEBHOOK-SIM-02 — local single-order delivery operation (test operation, not a feature)

Scope: one existing order on the local offline stack only
(`plink-725a6106-7cc5-4277-875e-31a58e906f86-f8e70d5d-f133-477b-ab1f-39436eac39ea`,
payment `567bec9e-61d0-4b4d-b160-1f7e0085203d`, transaction `2e803f2d-6886-4a10-9d98-de8371e54232`,
hash `manual-4f870c8f-f678-4072-b5e8-6f0753471817`, existing deposit delivery
`6cd37f1e-66df-4198-8688-4959426043a6`, payment outbox row `123cde77-1e3a-457e-9565-91ed32112c0b`).

**02A — read-only diagnosis (done, no writes, no HTTP, no worker).** Both notifications of this order
are queue-resident and unsent. `deposit_confirmed` already has a `webhook_deliveries` row (pending,
attempts 0, target `http://webhook-test:8080/webhook`); `payment.succeeded.v1` has **no** delivery row
and sits in `money_event_outboxes` (pending, attempts 0, unlocked) because the outbox→delivery relay is
a periodic worker that offline mode does not start. There is no by-id delivery entry point:
`DeliveryBoundary.DeliverOne` exists and is per-row but requires a lease
(`WebhookDeliveryRepo.ClaimDue(limit)` has no id filter), the relay's `relayOne` is unexported, and
`EnqueueReplay` targets an existing *delivery* row. Conclusion: a lease-respecting targeted run needs a
tool, not a `DeliverDue(limit=1)` call.

**02B — tool prepared, dry-run only (no production change).** Temporary runner
`/tmp/opencode/webhook-sim-02b/main.go` (mode 0600, mounted read-only into the existing native test
image, run on `gateway-offline-internal`): `--self-test` 17/17 guards; `--dry-run` (default) validates
read-only and prints the plan; `--execute` is required for any write or HTTP, with a 55 s budget, at
most two raw events, one attempt each. Phases: A) one short transaction — table lock
(`SHARE ROW EXCLUSIVE`, `lock_timeout 3s`), re-validate that the unfinished set is exactly this order's
two events, then the real `MoneyEventRelay.RunOnce` with transaction-scoped repositories (no HTTP,
normal outbox relay records committed) behind a queue adapter that aborts on any claimed id outside the
exact allowlist; B) one short transaction — lock, re-validate the eligible delivery set, take real
leases with `ClaimDue(2, 2m)`, assert id/event/target/lease, commit; C) no locks — `DeliverOne` per
leased row (signed POST to the internal receiver, `MarkAttempt`, source projection), no timers, no
retries, already-succeeded rows skipped, failed/processing rows never force-recovered.

Guards that refuse: non-development `APP_ENV`, `GATEWAY_OFFLINE` not true, non-local DSN, domain not in
webhook mode, target other than the exact receiver URL, a NATS route, any unfinished row outside the
allowlist, any claimed id outside the allowlist, unleased or state-changed rows.

Not done and not claimed: no worker enabled, no `GATEWAY_OFFLINE` change, no new egress, no order /
ledger / wallet / transaction / domain / secret / notify-URL change, no application source or deploy
change, no second deposit, nothing sent to the receiver during preparation.

**02C — single authorised delivery (executed once, 2026-10-15).** Frozen tool (source sha256
`82e96a41…3571`, binary sha256 `69fde294…60c6`, both re-verified before the run) executed once with
`--mode execute` in the native test image on `gateway-offline-internal`; outer command `timeout 75`,
tool budget 55 s. Exit code **0**. Phase A relayed both scoped outbox rows (`claimed=2 relayed=2
failed=0`), phase B committed real leases on the deposit delivery `6cd37f1e…` and on the payment
delivery `32ca7412…` created by phase A, phase C performed exactly two signed POSTs to
`http://webhook-test:8080/webhook` (`outbound requests=2`) and both attempts were recorded.

Result (read-only verification): outbox `deposit_confirmed` and `payment.succeeded.v1` both
`delivered`, attempts 1, lease released; deliveries both `succeeded`, attempts 1, lease released, exact
target; the payment session's webhook projection is written (`webhook_sent_at` set, attempts 1) and so
is the deposit transaction's; the receiver log contains exactly one `deposit_confirmed` and one
`payment.succeeded.v1` summary, each with a signature header present. **Scope of the claim:** this
proves the gateway produced and delivered the two signed POSTs over the internal network and recorded
the attempts; the receiver is a dev-only sink that does **not** verify the HMAC signature, so this is
not evidence of a production merchant acknowledging a webhook, of real money movement, or of
asynchronous retry behaviour. Log: `/tmp/opencode/logs/webhook-sim-02c-execute.log`.
