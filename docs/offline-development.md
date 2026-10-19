# Offline Development Environment

The offline environment runs the gateway **without any chain listener and without any
periodic money/event worker**, plus an isolated PostgreSQL, on a private Docker network.
It exists so local development, HTTP work, migrations, and test-suite work never touch
public RPC endpoints, real balances, or real funds.

It is **not** a production environment, and `/api/v1/common/readiness` deliberately
reports it as not ready.

## Isolation model

| Property | Implementation |
| --- | --- |
| No chain traffic, no money workers | `GATEWAY_OFFLINE=true` with `APP_ENV=development`. Valid only for explicit development; any other `APP_ENV` fails closed before the database is opened. |
| No application/database egress | `gateway` and `postgres` are attached **only** to `gateway-offline-internal` (`internal: true`). Verified on this host: neither container can resolve or reach external hosts (public-IP and DNS probes). The relay container is the only member of the non-internal network and does have egress (it is an inert TCP forwarder holding no credentials). |
| Loopback-only published ports | PostgreSQL on `127.0.0.1:55432`, HTTP on `127.0.0.1:3001`. Both are published by the `loopback` relay service. On this host (WSL2, Docker 29.6.1) a container attached only to an `internal: true` network could not have working published mappings — measured, not assumed — so the relay exists to provide them; this may differ on other hosts and Docker versions. |
| Persisted data | Named volume `gateway-offline-pgdata`. `docker compose down` keeps it; only `down -v` deletes it. |
| Secrets | `scripts/generate-offline-env.sh` creates `.env` once, mode `0600`, never overwrites, never prints values, and configures no OIDC/RPC/custody credentials. |

The `loopback` relay is an inert `alpine/socat` TCP forwarder. It carries no money-path
logic and no secrets; the gateway and database still have no egress themselves.

## Prerequisites

- Docker Engine with Compose v2 (`docker compose version`).
- The pinned Wallet Core submodule, needed by the image build:
  `git submodule update --init --recursive third_party/trustwallet/wallet-core`
- `postgres:16-alpine` (already local) and `alpine/socat:1.8.1.3` (pulled at first `up`).

## Quick start

Run everything from the repository root.

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

### Where the development API key comes from

`.env` intentionally holds no API key, and `scripts/generate-offline-env.sh` creates no
merchants or domains. To get a key:

1. register a merchant through the portal at `http://127.0.0.1:3001/merchant/register`;
2. create a domain from the dashboard (`/merchant/dashboard`, `POST /merchant/domains` —
   it asks for a domain URL, webhook URL, and webhook secret);
3. copy the domain **API Key** shown in the dashboard's domain list. New domains receive
   the default scopes, which include `read`.

Interpretation: an unauthenticated `401` is expected and is **not** the offline assertion.
The offline acceptance signal is the authenticated `503` whose payload contains
`offline.mode` with `ok=false`. A key created for a one-off verification run (for example
under `/tmp`) is throwaway evidence — never treat it as environment configuration.

Log lines that confirm offline mode:

```text
Offline mode: GATEWAY_OFFLINE=true with APP_ENV=development; chain listeners and periodic money/event workers stay disabled
Offline mode: chain listeners are NOT started (no RPC dialing, no chain state reads, no listener registration)
Offline mode: worker supervisor started with 0 periodic tasks
```

## Command reference

| Task | Command (from the repository root) |
| --- | --- |
| Start (rebuild image) | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml up -d --build` |
| Start (no rebuild) | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml up -d` |
| Stop, keep data | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml down` |
| Stop containers only | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml stop` |
| Logs (gateway) | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml logs -f gateway` |
| Logs (database) | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml logs -f postgres` |
| Status | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml ps` |
| Shell into the database | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml exec postgres psql -U postgres -d gateway` |
| Migrations | Automatic: the gateway migrates on startup whenever `APP_ENV` is not `production` (`ALLOW_AUTOMIGRATE_IN_PRODUCTION` defaults to false). The `-migrate` / `-seed` / `-install` flags do **not** make the process one-shot — they only force the migration/seed step and the HTTP server still starts and serves afterwards, so never use `compose run --rm gateway -install` expecting it to exit. |
| Run the Go test suite (no host toolchain) | Build the test image: `docker build --build-arg BUILD_JOBS=4 --target gotest -t gateway-offline:test .`. Create the isolated test database once: `docker compose … exec postgres psql -U postgres -c 'CREATE DATABASE gateway_test'`. Then run: `docker run --rm --network gateway-offline-internal -e TEST_DATABASE_URL="host=postgres port=5432 user=postgres password=<db password> dbname=gateway_test sslmode=disable" -e OUTBOX_TEST_DATABASE_URL="<same dsn>" -e MONEY_OUTBOX_TEST_DATABASE_URL="<same dsn>" -e TXRESCAN_TEST_DATABASE_URL="<same dsn>" gateway-offline:test go test -p=1 -count=1 ./...` — without those DSNs the Postgres-gated tests skip. |
| Offline policy unit tests (host, no native library needed) | `GOTOOLCHAIN=local CGO_ENABLED=1 go test ./helpers -run Offline -count=1` |
| Shell syntax checks | `sh -n scripts/generate-offline-env.sh && sh -n scripts/build_wallet_core.sh` |
| Compose validation | `docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml config --quiet` (exit status only, prints nothing) |

`docker compose config` renders full `env_file` values, and `--no-interpolate` does not
hide them (measured on this host). Validate with `config --quiet`, or filter the output
down to a non-sensitive projection, whenever the rendered result is shared.

## What offline mode disables, and what still works

Disabled (skipped entirely, not stubbed):

- chain listener construction/start for all twelve registered chains (no RPC dialing, no chain-state reads);
- periodic workers: `webhook-retry`, `session-expiry`, `transaction-finality`, `deposit-facts`,
  `outbound-transactions`, `sweep-jobs`, `reconciliation`, `transfer-finalization`, `provider-health`;
- address backfill and full address-index preload (the in-memory index object stays wired so HTTP paths cannot panic).

Still active (unchanged):

- HTTP server, routes, middleware, merchant/admin/checkout surfaces **including money-path endpoints**: the offline switch removes background loops, it does not reject API calls. What keeps those calls from touching real value is the environment, not the switch: the containers have no egress at all, `SIGNER_MODE=software` only works outside production, the mnemonic is a fresh development-only phrase, and no chain RPC endpoint is configured;
- database connection and startup migrations;
- Trust Wallet Core initialization paths (the native build is still required; the
  `walletcorefallback` tag is never used here and `GATEWAY_OFFLINE` cannot waive it);
- production safety gates: the software-signer production block, scale-mode gate, and readiness checks.

Money movement (deposits, sweeps, payouts, refunds, reconciliation) is **outside this
environment's acceptance scope**. It is not exercised, and with no egress and no RPC
endpoint it cannot reach a live network.

## Functional limitations

- `/api/v1/common/readiness` requires an authenticated caller: unauthenticated requests get `401`, and an authenticated request whose domain has the `read` scope always includes `offline.mode: ok=false` and returns 503 by design.
- No chain events are observed, so deposits, finality, payment matching, sweeps, refunds, and payouts never progress.
- Endpoints that need live chain data (balances, address validation against a network, live-balance admin views) fail or return errors; there is no RPC endpoint configured.
- Wallet/address derivation uses the development mnemonic created by the env generator; those addresses are disposable and must never receive real funds.
- HTTP client IPs are the relay's address, not the caller's (the relay is a plain TCP forwarder).
- Only `linux/amd64` is supported by this environment.

## Safety rules

- Never reuse a mnemonic that ever held real funds. `.env` is git-ignored and must stay mode `0600`.
- Never set `APP_ENV=production`, `ALLOW_AUTOMIGRATE_IN_PRODUCTION`, or `ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION` here.
- `GATEWAY_OFFLINE=true` outside `APP_ENV=development` aborts startup: offline mode must never become a way to bypass production guarantees.
- Do not commit generated `.env` files, database dumps, or RPC/OIDC credentials.

## Verified behaviour (ENV-03, measured on this host)

The Docker build compiles real Trust Wallet Core from the pinned submodule
(`[100%] Built target TrustWalletCore`, `libTrustWalletCore.a` and
`libwallet_core_rs.a` produced) and never uses `walletcorefallback`. Observed facts:

| Check | Result |
| --- | --- |
| Full image build | exit 0, ~32 min with `BUILD_JOBS=4`; peak host memory use ~11.4 GiB of 15.9 GiB (the unbounded default would have been unsafe here) |
| Runtime image | `gateway-offline:local`, 435 MB on disk (~124 MB compressed manifest), runs as uid 10001 (`gateway`), healthy per the built-in healthcheck |
| Startup | `Migration:BEGIN/END`, then offline mode logs, then `App running on :3001`; zero listener-start lines; `worker supervisor started with 0 periodic tasks` |
| HTTP | `/` → 200; `/assets/tailwind.css` → 200 (25 876 bytes, byte-identical to the repository file) |
| Readiness | unauthenticated → 401; authenticated with a `read`-scope dev key → 503 with `offline.mode ok=false` (`offline development mode is active`), 46/59 checks ok |
| Database | PostgreSQL healthy, `uuid-ossp` present, migration created the full schema; `sweep_jobs`, `outbound_transactions`, and `transactions` are all 0 |
| Egress | Gateway: public-IP probe `http://1.1.1.1` fails (`Couldn't connect to server`) and DNS resolution fails; PostgreSQL: `Network unreachable`; control: the relay container **can** reach the same IP, proving the probe detects egress |
| Ports | Only `127.0.0.1:3001` and `127.0.0.1:55432` are bound on the host; the gateway and database publish nothing themselves |
| Test suite | Runs inside the image (`--target gotest`) against the separate `gateway_test` database: `go build ./...` and `go vet ./...` exit 0; `go test -p=1 -count=1 ./...` exit 0 → 43 packages ok (8 packages have no test files) |

## Known limitations

- In development the static middleware runs with compression disabled, so assets are
  served identity-encoded and the committed `.fiber.br` sidecars are not used; they are
  guarded by `TestFiberBrotliSidecarsMatchSources`, which requires every sidecar to
  decompress to its source. Whenever `npm run build:css` changes an asset, regenerate its
  sidecar with `go run ./scripts/brotli-sidecars <asset files...>`
  (`go run ./scripts/brotli-sidecars --verify` audits all of them).
- `chain_states` stays **empty** in offline mode: those rows are created by the chain
  listener path, which offline mode skips. Anything that needs chain state (payment
  confirmation tracking, balance views) reports errors, which is consistent with
  readiness returning 503. Use the non-offline runtime for chain-state-dependent work.
- The relay container is the only member of the non-internal network, so it has egress
  (it is an inert TCP forwarder holding no credentials). The gateway and database do not.
- HTTP request IPs are the relay's address.
- The offline switch does not reject HTTP money-path calls; their protection is the
  absent egress, absence of chain RPC endpoints, the development-only mnemonic, and the
  production signer block — not the switch itself.

## Deferred work

Not part of the verified environment above:

1. exercising money-path HTTP workflows (deposits, sweeps, payouts, refunds,
   reconciliation) — deliberately outside this environment's acceptance scope;
2. running the host-side `scripts/build_wallet_core.sh` (the image builds the same
   source in a container instead);
3. `./scripts/regression.sh` on the host, which re-installs system packages and rebuilds
   Wallet Core; the equivalent is `go build ./... && go vet ./... && go test -p=1 -count=1 ./...`
   inside the test image;
4. production custody, KMS/HSM/Vault signing, and any mainnet connectivity.
