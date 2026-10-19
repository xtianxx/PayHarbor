# Implementation Plan: Offline Development Environment

**Branch**: `001-offline-dev-env` | **Date**: 2026-10-12 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/001-offline-dev-env/spec.md`

## Summary

Add an explicit, fail-closed `GATEWAY_OFFLINE=true` runtime switch (valid only with `APP_ENV=development`) that skips chain listeners and every periodic chain/money/webhook worker while keeping HTTP, database migration, and Trust Wallet Core initialization untouched. Ship a Docker-based environment that compiles real Wallet Core natively, runs app + PostgreSQL on an internal-only network with loopback-published ports, and bootstraps a `0600` `.env` once, without printing secrets.

## Technical Context

**Language/Version**: Go 1.25.4 (required by `go.mod`, unchanged); C++/Rust via Trust Wallet Core pinned submodule `4edc4a142b7869201ee5b24ea92f939c617387c6`.

**Primary Dependencies**: Fiber v3, GORM/PostgreSQL, Trust Wallet Core (CGo, static `libTrustWalletCore.a` in the standalone Linux CMake branch), Tailwind CSS (build artifact already committed). **No new Go or npm dependencies**; the delivered compose stack adds one pinned auxiliary image, `alpine/socat:1.8.1.3`, used only as an inert loopback TCP relay.

**Storage**: PostgreSQL 16 (`postgres:16-alpine`), loopback port `127.0.0.1:55432`, named volume, no host PostgreSQL required.

**Testing**: `go test ./helpers` (policy matrix) and `go test .` / `go test ./api/handlers` (supervisor seam, readiness marker) — the packages that need the native library were executed in ENV-03 inside the `gotest` image, together with `go build ./...`, `go vet ./...`, and the full `go test -p=1 -count=1 ./...` against an isolated `gateway_test` database.

**Target Platform**: Linux (Ubuntu 24.04 container base; developer host is WSL2 Ubuntu 24.04).

**Project Type**: Single Go module + container tooling.

**Constraints**: no egress for running containers; no public RPC/OIDC/custody credentials configured; no `walletcorefallback`; production/readiness gates untouched; offline mode must not silently change default behavior; do not start services or run migrations in this phase.

**Scale/Scope**: one new helper file + three seam edits + three test files; four new environment files (`Dockerfile`, `.dockerignore`, compose, env generator); two docs (offline dev guide, Spec Kit artifacts).

## Constitution Check

*GATE: verified before implementation.*

| Principle (source) | Status | Evidence |
| --- | --- | --- |
| Money paths respect module boundaries (`docs/module-boundaries.md`) | PASS | Offline mode only *omits* worker registration in `main.go`; no domain/business file is touched, no new money path is introduced. |
| Production safety gates must not be weakened (README / signer policy) | PASS | Offline requires `APP_ENV=development`; production signer/readiness/scale-mode logic untouched; readiness reports *not ready* in offline mode. |
| Native Trust Wallet Core is mandatory (`README`, `SECURITY.md`) | PASS | `walletcorefallback` is never used; the existing fallback guard stays; the Docker build compiles the pinned submodule. |
| No new Go/npm dependencies / no UI changes | PASS | Only stdlib additions (`os`, `strings`, `fmt`, `errors`), the module's existing brotli dependency in `scripts/brotli-sidecars`, and shell/compose/docs. The only new image is the pinned `alpine/socat` loopback relay; no template or visual source changed. |
| Idempotent, fail-closed configuration (`docs/production-migration-discipline.md` spirit) | PASS | Offline policy validated before DB init; `.env` generator is create-once and never prints secrets. |
| Observability of degraded modes (`docs/money-path-observability-runbook.md` spirit) | PASS | Startup logs state listeners/workers are disabled; `/api/v1/common/readiness` carries an explicit `offline.mode` check. |

## Project Structure

### Documentation (this feature)

```text
specs/001-offline-dev-env/
├── spec.md      # Feature specification
├── plan.md      # This file
└── tasks.md     # Task list with phase statuses
```

### Source Code (repository root)

```text
helpers/
└── offline.go                                  # NEW: offline env policy (requested/valid/enabled)
api/handlers/
├── v1_readiness.go                             # EDIT: offline.mode readiness check (fail-closed marker)
└── v1_readiness_offline_test.go                # NEW: readiness marker unit test
main.go                                         # EDIT: offline policy gate, listener skip, worker-set seam
main_offline_seam_test.go                       # NEW: supervisor task-set regression (offline vs default)
Dockerfile                                      # NEW: wallet-core stage -> Go stage -> runtime stage
.dockerignore                                   # NEW: build context filters
deploy/compose/offline/docker-compose.yml       # NEW: project-namespaced, internal network, loopback ports
scripts/generate-offline-env.sh                 # NEW: create-once 0600 .env generator (random dev secrets)
docs/offline-development.md                     # NEW: start/stop/logs/test + limitations + ENV-03 handoff
.gitignore                                      # EDIT (only if a new artifact needs ignoring)
```

**Structure Decision**: Seam stays inside `main.go` (the existing composition root) and one new helper package file; worker-task construction is extracted into a named function so the offline task set is testable through `Supervisor.TaskNames()` instead of source-text assertions. Container tooling lives at the repository root/build-adjacent paths, mirroring the existing `scripts/` and `deploy/` conventions.

### Key technical decisions

1. **Fail-closed policy placement**: validate `GATEWAY_OFFLINE` immediately after `.env` load, before `walletcore` fallback guard and before `NewApp()`, so an invalid combination cannot migrate, connect, or start anything.
2. **Worker seam shape**: `buildGatewayWorkerSupervisor(notifier, offline)` + `gatewayPeriodicWorkerTasks(notifier)`; offline returns a supervisor with zero tasks (still startable/stoppable) and logs the skipped set. Existing source-token contract test stays valid because the `supervisedWorker(...)` registration lines remain in `main.go`.
3. **Address index**: keep constructing the in-memory index and its wallet observer (prevents nil paths in HTTP code), skip `Load()` and `backfillMissingAddresses` in offline mode.
4. **Readiness marker**: append `offline.mode` with `ok=false` at the top of `v1RunReadinessChecks` (before early returns) so aggregate readiness is 503 and no "all green" misreport is possible.
5. **Docker build graph (as delivered)**: four stages — `walletcore` builds Trust Wallet Core with clang/CMake (vendored `tools/*` steps, minus the coverage-only apt/lcov step; `BUILD_JOBS`, default 4, bounds clang/protobuf/cargo parallelism) and emits a slim artifact tree (headers + `*.a` in their original `-L` locations + the `tw` Go module); `gobuild` pins Go 1.25.4 with checksum verification, runs `go mod download`, builds with `CGO_ENABLED=1` and no tags, then smoke-checks the binary; `gotest` reuses the `gobuild` image so `go build` / `go vet` / `go test` run inside the image instead of installing a toolchain on the host; `runtime` copies only the binary plus `views/` and `static/` (no `docs/`: the Swagger artifacts are compiled into the `docs` Go package, and no code reads them from disk) and runs as a non-root user.
6. **Secret bootstrap**: POSIX shell + stdlib `python3`; mnemonic generated from 16 bytes of `/dev/urandom` with BIP39 checksum and the wordlist extracted from the pinned submodule (`trezor-crypto/include/TrezorCrypto/bip39_english.h`), validated against the submodule's official `bip39_vectors.json`; no public mnemonic is ever used as a real value.
7. **Port publishing vs. egress isolation (measured, not assumed)**: on this host (WSL2, Docker 29.6.1) a container attached only to an `internal: true` network did not get a reachable `-p 127.0.0.1:…` mapping even though the service was healthy, and a bridge network with `com.docker.network.bridge.enable_ip_masquerade=false` still allowed outbound egress (WSL2 host NAT). Both are host-measured results, not general Docker guarantees. Therefore: `gateway` and `postgres` stay attached **only** to the internal network (application/database egress blocked, verified), and a single inert `loopback` relay service (`alpine/socat:1.8.1.3`, no secrets, no money-path logic, does have egress of its own) joins both networks to publish `127.0.0.1:55432` and `127.0.0.1:3001`. End-to-end verified: host → `127.0.0.1:55432` → relay → internal-only PostgreSQL.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
| --- | --- | --- |
| Extra boolean seam through `buildGatewayWorkerSupervisor` | The worker set is the only place where offline mode can guarantee "no periodic money work"; an env read hidden inside the builder would be untestable and would make default behavior implicit. | Reading `GATEWAY_OFFLINE` inside the builder: not observable in tests, easy to regress, hides mode from the call site. |
| Multi-stage Dockerfile (four stages) | Trust Wallet Core must compile natively (CGo/CMake/clang); the test entry must run without host tooling; and the runtime image should stay small and non-root. | Single-stage image: ships compilers and a multi-GB build tree, and the Docker build must stay within the approved "no host system modification" constraint. |

## Phase Plan (status)

- **Phase 0 (research, done in ENV-01)**: inventory, vendored wallet-core script inspection, artifact/link-form verification, wordlist availability.
- **Phase 1 (design, done)**: this plan + `tasks.md`.
- **Phase 2 (implementation, done in ENV-02)**: offline policy helper, `main.go` seam, readiness marker, tests, container/script/docs.
- **Phase 3 (verification, done in ENV-02)**: targeted `go test` for packages buildable without the native library, shell syntax checks, `docker compose config`, env-generator behavioral check.
- **Phase 4 (executed in ENV-03)**: native Wallet Core build, `go build ./...` / `go vet ./...` / `go test ./...` in the `gotest` image, startup migration against the containerized PostgreSQL, offline listener-absence runtime proof, egress/port/persistence verification. Full evidence in `/tmp/opencode/gateway-env03/report/ENV-03-report.md`.
- **Phase 5 (ENV-04)**: documentation and status corrections only — no runtime code change, no image rebuild.
