# Feature Specification: Offline Development Environment

**Feature Branch**: `001-offline-dev-env`

**Created**: 2026-10-12

**Status**: Approved for bounded implementation (ENV-02)

**Input**: ENV-01 inventory conclusions. Operator-approved scope: an explicit `GATEWAY_OFFLINE=true` switch for `APP_ENV=development` plus a containerized local environment that never reaches real funds, reusable for the follow-up native build phase (ENV-03).

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Chain-free local runtime switch (Priority: P1)

A developer wants to run the gateway locally, exercise HTTP surfaces, migrations, and wallet code initialization, without any chain listener polling public RPC endpoints and without periodic money/event workers (sweep, outbound, deposit facts, finality, webhook retry, reconciliation, transfer finalization, session expiry, provider health) executing against live networks.

The runtime is chain-free only when the operator explicitly asks for it with `GATEWAY_OFFLINE=true` and the process is unambiguously a development process (`APP_ENV=development`). Any other combination fails closed instead of silently degrading or bypassing production gates.

**Why this priority**: Without this, a local run of this repository starts twelve chain listeners against compiled-in mainnet endpoints and auto-sweeps any finalized deposit. That is the single largest correctness and funds-safety risk identified in ENV-01.

**Independent Test**: Unit-test the offline policy matrix and assert that an offline worker supervisor registers zero periodic tasks while the default supervisor still registers all of them (`go test ./helpers ./ -run 'Offline|Supervisor'`). Runtime proof (starting the binary without chain RPC configured) happens in ENV-03 after the native library build.

**Acceptance Scenarios**:

1. **Given** `APP_ENV=development` and `GATEWAY_OFFLINE=true`, **When** the gateway starts, **Then** no chain listener is started, no periodic money/event worker is registered, no address-index preload or address backfill runs, and startup logs state that offline mode is active and listeners were not started.
2. **Given** `APP_ENV` is unset, empty, `production`, `staging`, or any value other than `development`, **When** `GATEWAY_OFFLINE=true`, **Then** the process aborts before opening a database connection.
3. **Given** `GATEWAY_OFFLINE` is unset, empty, or falsey, **When** the gateway starts in development, **Then** behavior is byte-for-byte the pre-change behavior (listeners, all periodic workers, address index preload).
4. **Given** offline mode is active, **When** `/api/v1/common/readiness` is queried, **Then** the response is not ready (HTTP 503) and contains an explicit `offline.mode` check reporting that listeners and money workers are disabled and the environment is not production ready.
5. **Given** offline mode is active, **When** the gateway boots, **Then** database migration hooks and Trust Wallet Core initialization paths are untouched (no build tags, no fallback provider selection).

---

### User Story 2 - Isolated container environment and safe secret bootstrap (Priority: P2)

An operator wants one command path that builds the gateway natively (real Trust Wallet Core, never the `walletcorefallback` tag), runs it and PostgreSQL on a private Docker network with no egress, publishes only loopback ports, and bootstraps a `.env` with freshly generated development-only secrets without ever overwriting an existing file or printing secrets.

**Why this priority**: Provides the reproducible, funds-isolated environment the offline switch is meant for, and unblocks ENV-03 (native build, migration, startup, full test suite).

**Independent Test**: Static and executable checks that run without compiling Wallet Core: `docker compose config`, shell syntax checks, and running the env generator twice in a scratch directory to prove create-once, `0600`, and no plaintext secret output.

**Acceptance Scenarios**:

1. **Given** a scratch directory, **When** the env generator runs twice, **Then** the first run creates `.env` with mode `0600` and the second run leaves it byte-identical and prints no secret values.
2. **Given** the compose file, **When** `docker compose config` runs, **Then** resources are project-namespaced, PostgreSQL publishes only `127.0.0.1:55432`, HTTP publishes only `127.0.0.1:3001`, the runtime network is `internal: true`, and the data volume is persistent and named.
3. **Given** the Dockerfile, **When** it is inspected, **Then** the Wallet Core build uses clang/CMake against the pinned submodule revision with no `-tags walletcorefallback` usage anywhere, and the Go toolchain is pinned to the version required by `go.mod`.

---

### User Story 3 - Documented local workflow (Priority: P3)

A developer wants start, stop, log, and test commands plus an explicit list of offline-mode functional limitations and the deferred native-build work, so nobody mistakes an offline environment for a production-ready gateway.

**Independent Test**: Documentation review plus the commands it lists being present in the repository (`scripts/generate-offline-env.sh`, compose file, Dockerfile).

**Acceptance Scenarios**:

1. **Given** the offline docs, **When** followed, **Then** every referenced file exists and every command is copy-pasteable without editing.
2. **Given** offline mode, **When** the developer reads the limitations section, **Then** it states that no chain events are observed, no balance/address RPC checks work, money lifecycle workers are inert, and readiness reports not-ready.

### Edge Cases

- `GATEWAY_OFFLINE=1|true|yes|on` vs `0|false|no|off|unset|garbage`: only documented truthy values enable the request; garbage stays falsey (default behavior preserved).
- `APP_ENV=" Development "` (case/whitespace) is treated as development; `development-local` is not.
- Offline requested while the process was started with the `walletcorefallback` build tag: the existing fallback guard still applies first and is never waived by offline mode.
- An already-present `.env` (any content, including one with real credentials) is never modified by the generator.
- Compose teardown must never delete the named data volume unless the operator explicitly passes `-v`.
- Missing submodule: the Docker build must fail with an actionable message rather than falling back to the fallback provider.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The runtime MUST read an explicit offline switch from `GATEWAY_OFFLINE` (truthy: `1`, `true`, `yes`, `on`, case-insensitive).
- **FR-002**: Offline mode MUST be rejected unless `APP_ENV` trims/case-insensitively equals `development`; rejection MUST abort startup before database initialization.
- **FR-003**: Offline mode MUST NOT weaken or bypass production readiness checks, the software-signer production block, the scale-mode gate, or the Trust Wallet Core native-build requirement.
- **FR-004**: When offline mode is active, chain listener construction and start MUST be skipped entirely (no RPC dialing, no chain state reads), and startup logs MUST NOT claim listeners started.
- **FR-005**: When offline mode is active, all periodic chain/money/webhook workers MUST be omitted from the worker supervisor; the supervisor MUST still exist and stop cleanly.
- **FR-006**: When offline mode is active, address backfill and full address-index preload MUST be skipped; the in-memory address index object MUST remain wired so HTTP paths cannot nil-dereference.
- **FR-007**: When offline mode is active, HTTP server startup, route registration, database connection, and startup migrations MUST behave exactly as before.
- **FR-008**: When offline mode is active, `/api/v1/common/readiness` MUST include an explicit offline marker with `ok=false` so the aggregate readiness result is not ready.
- **FR-009**: Default (switch unset) and every non-offline configuration MUST preserve existing behavior, including all twelve chain listeners and nine periodic workers.
- **FR-010**: The environment MUST ship a container build that compiles real Trust Wallet Core from the pinned submodule revision and builds the Go binary with CGO enabled and no `walletcorefallback` tag.
- **FR-011**: The container environment MUST run the application and PostgreSQL on a Docker network with `internal: true`, publish PostgreSQL only on `127.0.0.1:55432` and HTTP only on `127.0.0.1:3001`, and use a named persistent volume for database data.
- **FR-012**: The environment MUST NOT require or configure public chain RPC endpoints, external OIDC providers, or custody-signer credentials; no Anvil or public RPC is used in this phase.
- **FR-013**: A secret bootstrap entry point MUST create `.env` only when absent, with mode `0600`, containing freshly generated random development values (mnemonic, master key, portal JWT secret, admin password, database password) and MUST never print those values or reuse a publicly known mnemonic.
- **FR-014**: Documentation MUST list start/stop/log/test commands, offline limitations, and MUST state that stopping the stack does not delete the data volume.
- **FR-015**: The change MUST NOT add third-party Go/npm dependencies, MUST NOT alter UI rendering, MUST NOT change business/domain logic, and MUST NOT upgrade or downgrade business dependency versions.

### Key Entities

- **Offline policy**: derived from `GATEWAY_OFFLINE` + `APP_ENV`; the single source of truth for skipping listeners/workers/index work.
- **Worker supervisor task set**: the nine periodic tasks; offline mode yields an empty set.
- **Generated `.env`**: development-only configuration file, mode `0600`, never overwritten.
- **Container environment**: `Dockerfile` build graph (wallet-core stage → Go stage → runtime stage), compose project resources, internal network, named volume.

## Success Criteria *(mandatory)*

- **SC-001**: Offline policy unit tests cover the request/validity matrix (development, unset, other values, garbage values, case/whitespace) and all pass.
- **SC-002**: A regression test proves the offline supervisor registers zero tasks while the default supervisor registers the same nine task names as before.
- **SC-003**: A regression test proves `/api/v1/common/readiness` reports the offline marker (not-ready) when offline mode is active.
- **SC-004**: `docker compose config` for the new compose file exits 0 and shows only loopback-published ports and an `internal: true` network.
- **SC-005**: Running the env generator twice yields one created file, mode `0600`, unchanged second run, and zero secret values on stdout/stderr.
- **SC-006**: Every shell script added is syntax-checked (`sh -n`) with no errors.
- **SC-007**: No file outside the documented scope is modified; the existing source-token supervisor contract test still passes unchanged.

## Assumptions

- ENV-03 (not this phase) executes the native build, migrations, startup smoke, and the full `go test ./...` suite; this phase only prepares and statically verifies that path.
- The pinned Trust Wallet Core submodule revision is already initialized in the working tree (`4edc4a142b7869201ee5b24ea92f939c617387c6`).
- Docker builds may access the network; the running containers may not.
- `postgres:16-alpine` is already available locally, so the environment adds no image pulls beyond Ubuntu/Go build bases.
- Postgres 16 is sufficient for the schema; nothing in this phase runs migrations.
