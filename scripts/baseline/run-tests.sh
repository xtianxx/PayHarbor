#!/bin/sh
# A3 baseline test runner (runs INSIDE the baseline test container).
#
# Records the real exit codes of `go build`, `go vet` and `go test` plus their
# stderr before anything else is interpreted; the fail-closed gate
# (scripts/baseline/verify-test-json.py) re-checks the JSON stream and the saved
# exit codes on the host.
#
# Exit codes: 0 all three commands succeeded; otherwise the first non-zero exit
#             code in the order build, vet, test is returned; 70 = environment
#             error (missing synthetic DSN variables or missing source dir);
#             64 = bad invocation (unknown argument / missing value).
set -u

EVIDENCE="/evidence"
SRC="${P0_SRC:-/src}"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --evidence)
      [ "$#" -ge 2 ] || { echo "missing value for --evidence" >&2; exit 64; }
      EVIDENCE="$2"; shift 2 ;;
    --src)
      [ "$#" -ge 2 ] || { echo "missing value for --src" >&2; exit 64; }
      SRC="$2"; shift 2 ;;
    *)
      echo "unknown argument: $1" >&2; exit 64 ;;
  esac
done

export GOPROXY=off
export GOTOOLCHAIN=local

missing=""
for name in TEST_DATABASE_URL OUTBOX_TEST_DATABASE_URL MONEY_OUTBOX_TEST_DATABASE_URL TXRESCAN_TEST_DATABASE_URL; do
  eval "value=\${$name:-}"
  [ -n "$value" ] || missing="$missing $name"
done
if [ -n "$missing" ]; then
  echo "environment error: missing DSN variables:$missing (inject via P0_DSN_FILE, never from the host)" >&2
  exit 70
fi

if [ ! -d "$SRC" ]; then
  echo "environment error: source dir $SRC not found" >&2
  exit 70
fi

mkdir -p "$EVIDENCE"
cd "$SRC"

go version >"$EVIDENCE/go-version.txt" 2>&1

go build ./... >"$EVIDENCE/build.txt" 2>"$EVIDENCE/build.stderr"
build_rc=$?

go vet ./... >"$EVIDENCE/vet.txt" 2>"$EVIDENCE/vet.stderr"
vet_rc=$?

go test -json -p=1 -count=1 -timeout 30m ./... >"$EVIDENCE/test.jsonl" 2>"$EVIDENCE/test.stderr"
test_rc=$?

printf '%s\n' "$build_rc" >"$EVIDENCE/build-exit-code"
printf '%s\n' "$vet_rc" >"$EVIDENCE/vet-exit-code"
printf '%s\n' "$test_rc" >"$EVIDENCE/exit-code"

echo "baseline test runner: go build exit=$build_rc ; go vet exit=$vet_rc ; go test exit=$test_rc"
echo "evidence written to $EVIDENCE (build/vet/test outputs, exit codes, stderr)"
echo "note: database connection failures surface as ordinary go test failures; they are not remapped"

if [ "$build_rc" -ne 0 ]; then exit "$build_rc"; fi
if [ "$vet_rc" -ne 0 ]; then exit "$vet_rc"; fi
exit "$test_rc"
