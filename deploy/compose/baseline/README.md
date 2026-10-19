# Baseline isolation stack (A3, local non-production)

One page. This stack proves a reproducible native build, a dedicated PostgreSQL
test database, and an offline application smoke — without touching the existing
`deploy/compose/offline/` stack, the repository root `.env`, or any user volume.

## What it is

* `postgres:16-alpine` with a private `gateway_test` database. It has **no
  `env_file`**: only its three PG_* fields arrive, via explicit `--env-file`
  interpolation of the calling shell, so the database container never receives
  the wider synthetic app secrets (mnemonic, admin password, portal JWT).
* `gateway`: the current-source **native** runtime image, `GATEWAY_OFFLINE=true`,
  attached only to an internal network. No ports published.
* `test` (profile `test`): the current-source **native** test image, running
  `scripts/baseline/run-tests.sh` inside the same internal network with
  `GOPROXY=off` and `GOTOOLCHAIN=local`. Its four DSN variables come from the
  controlled `P0_DSN_FILE` and are never inherited from the host.

Isolation guarantees: no top-level `name:`, no explicit `name:` on the volume or
network (the `-p` project prefix namespaces everything), no reference to any
existing volume, and no fallback to the repository root `.env`.

**Calling contract (not verified by Compose):** `P0_ENV_FILE` / `P0_DSN_FILE` /
`P0_EVIDENCE_DIR` must be absolute paths. Compose only performs interpolation; it
does not check absoluteness. Pass them from a shell that never sources the
repository root `.env`.

## Prepare (synthetic credentials only)

```sh
umask 077
RUN="$(mktemp -d /tmp/opencode/gateway-a3-XXXXXX)"
mkdir -p "$RUN/env" "$RUN/evidence"
scripts/generate-offline-env.sh --path "$RUN/env/gateway.env"          # 0600, never overwrites
python3 scripts/baseline/make-dsn-env.py --env "$RUN/env/gateway.env" --out "$RUN/env/dsn.env"
```

## Build (network allowed for dependencies; native CGO, never fallback)

```sh
TAG=a3-$(date +%s)
docker build --build-arg BUILD_JOBS=4 --target gotest  -t "gateway-baseline:${TAG}-test"    .
docker build                           --target runtime -t "gateway-baseline:${TAG}-runtime" .
```

## Run the suite (one full pass; evidence lands in $RUN/evidence)

```sh
export P0_ENV_FILE="$RUN/env/gateway.env" P0_DSN_FILE="$RUN/env/dsn.env" \
       P0_TEST_IMAGE="gateway-baseline:${TAG}-test" P0_RUNTIME_IMAGE="gateway-baseline:${TAG}-runtime" \
       P0_EVIDENCE_DIR="$RUN/evidence" P0_TEST_DB=gateway_test
PROJ="p0b-a3-$(date +%s)"
compose() { docker compose -p "$PROJ" --env-file "$P0_ENV_FILE" -f deploy/compose/baseline/docker-compose.yml "$@"; }

compose up -d postgres --wait
compose --profile test run --rm test scripts/baseline/run-tests.sh --evidence /evidence
python3 scripts/baseline/verify-test-json.py \
  --json "$RUN/evidence/test.jsonl" --exit-code "$RUN/evidence/exit-code" \
  --vet-exit-code "$RUN/evidence/vet-exit-code" --build-exit-code "$RUN/evidence/build-exit-code" \
  --must-run scripts/baseline/must-run.txt --allow-skip scripts/baseline/allow-skip.txt \
  --summary "$RUN/evidence/summary.json"
```

Exit semantics:

* `run-tests.sh`: `0` all green; otherwise the first non-zero of build, vet, test;
  `70` only for missing synthetic DSN variables (environment error); `64` bad
  invocation. Database connection failures are **not** remapped — they surface as
  ordinary test/vet exit codes.
* `verify-test-json.py`: `0` pass, `2` gate violation, `3` evidence error
  (unparsable/non-object JSON, empty stream, empty must-run). The gate also
  rejects tests that reach no terminal event (truncated stream), must-run tests
  without a `run` or without a terminal `pass`, and must-run packages without a
  package-level completion `pass`.
* The single tolerated skip is `core/repositories Test_Merchant`
  (`scripts/baseline/allow-skip.txt`); it is reported, never counted as passed.

## Smoke (offline app; no secrets in output)

```sh
compose up -d gateway --wait
docker exec "${PROJ}-gateway-1" curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3001/
docker exec "${PROJ}-gateway-1" curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3001/docs/integration-guide.md
docker exec "${PROJ}-gateway-1" curl -fsS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3001/docs/money-event-catalog.md
docker exec "${PROJ}-gateway-1" curl -s  -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3001/api/v1/common/readiness   # 401 without credentials
docker logs "${PROJ}-gateway-1" 2>&1 | grep 'Offline mode'
docker exec "${PROJ}-gateway-1" sh -c 'getent hosts example.com'          # must fail
docker exec "${PROJ}-gateway-1" sh -c 'curl --max-time 3 -fsS https://example.com'   # must fail
docker exec "${PROJ}-gateway-1" sh -c 'curl --max-time 3 -fsS http://93.184.216.34'  # DNS-independent probe
docker inspect "${PROJ}-gateway-1" --format '{{json .NetworkSettings.Networks}}'
```

## CI (Regression workflow)

`.github/workflows/regression.yml` runs this baseline as the **required**
conclusion: the `gotest` image is built from the current checkout
(`BUILD_JOBS=2`), a dedicated `postgres:16-alpine` starts on the internal
network, `scripts/baseline/run-tests.sh` records the raw build/vet/test exit
codes, and `scripts/baseline/verify-test-json.py` is the only step deciding
pass/fail. The gate runs with `if: always()` (so evidence survives failures) and
the same applies to the artifact upload and the cleanup step, which stops only
that job's own compose project. Before the heavy native build, a
`docker compose … config --quiet` preflight verifies that every variable the
baseline compose file requires is present (it prints nothing, so no resolved
secrets can reach the log). Uploaded artifacts are an **explicit file
whitelist** — `go-version.txt`, `build.txt`, `build.stderr`, `vet.txt`,
`vet.stderr`, `test.jsonl`, `test.stderr`, the three exit-code files and
`summary.json` — with no recursive wildcard over the run directory, so env files
and credentials can never be uploaded. `fallback-regression` is supplementary
and cannot mask a native failure.

The wiring is pinned by `scripts/baseline/ci_workflow_contract_test.go`, which
runs as part of the ordinary Go suite.

## Local checks for the baseline tooling itself

```sh
python3 scripts/baseline/test_verify_test_json.py     # gate unit tests
sh scripts/baseline/test_run_tests.sh                 # runner stub tests
```

## Stop

```sh
compose stop    # keeps this project's own volume and containers
```

Never `down -v`, `volume rm`, or deletions of pre-existing images/containers
without a separate, explicit resource-destruction authorization.

## Non-goals

No business-logic, schema, HD-path, brand, or licensing changes; no CI changes in
this milestone; no production use — this is a tested local baseline only.
