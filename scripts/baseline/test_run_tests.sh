#!/bin/sh
# Unit tests for scripts/baseline/run-tests.sh using a stub `go` on PATH.
# No external dependencies; temp fixtures are created under /tmp/opencode.
set -u

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
RUNNER="$ROOT/scripts/baseline/run-tests.sh"
TMP_ROOT="${TMP_ROOT:-/tmp/opencode}"
mkdir -p "$TMP_ROOT"
TMP="$(mktemp -d "$TMP_ROOT/gateway-runner-test-XXXXXX")"
trap 'rm -rf "$TMP"' EXIT INT TERM

mkdir -p "$TMP/bin" "$TMP/src" "$TMP/ev"
cat > "$TMP/bin/go" <<'STUB'
#!/bin/sh
case "$1" in
  version) echo "go version go-fake"; exit 0 ;;
  build)   exit "${STUB_BUILD_RC:-0}" ;;
  vet)     exit "${STUB_VET_RC:-0}" ;;
  test)    echo '{"Time":"t","Action":"run","Package":"x","Test":"T"}'; exit "${STUB_TEST_RC:-0}" ;;
  *)       exit 0 ;;
esac
STUB
chmod +x "$TMP/bin/go"

failures=0
check() {
  name="$1"; expected="$2"; actual="$3"
  if [ "$expected" = "$actual" ]; then
    echo "ok   $name (exit=$actual)"
  else
    echo "FAIL $name expected=$expected actual=$actual"
    failures=$((failures + 1))
  fi
}

export PATH="$TMP/bin:$PATH"
export TEST_DATABASE_URL="host=fake dbname=x" OUTBOX_TEST_DATABASE_URL="host=fake dbname=x" \
       MONEY_OUTBOX_TEST_DATABASE_URL="host=fake dbname=x" TXRESCAN_TEST_DATABASE_URL="host=fake dbname=x"

# 1) all three succeed
STUB_BUILD_RC=0 STUB_VET_RC=0 STUB_TEST_RC=0 sh "$RUNNER" --evidence "$TMP/ev" --src "$TMP/src" >/dev/null 2>&1
check "all green exit" 0 "$?"
check "recorded test exit" 0 "$(cat "$TMP/ev/exit-code")"
check "recorded build exit" 0 "$(cat "$TMP/ev/build-exit-code")"
check "recorded vet exit" 0 "$(cat "$TMP/ev/vet-exit-code")"

# 2) go test failure is not masked
STUB_BUILD_RC=0 STUB_VET_RC=0 STUB_TEST_RC=3 sh "$RUNNER" --evidence "$TMP/ev" --src "$TMP/src" >/dev/null 2>&1
check "test failure exit" 3 "$?"
check "test failure recorded" 3 "$(cat "$TMP/ev/exit-code")"

# 3) go vet failure is not masked
STUB_BUILD_RC=0 STUB_VET_RC=5 STUB_TEST_RC=0 sh "$RUNNER" --evidence "$TMP/ev" --src "$TMP/src" >/dev/null 2>&1
check "vet failure exit" 5 "$?"
check "vet failure recorded" 5 "$(cat "$TMP/ev/vet-exit-code")"

# 4) go build failure is reported first, evidence still complete
STUB_BUILD_RC=4 STUB_VET_RC=0 STUB_TEST_RC=0 sh "$RUNNER" --evidence "$TMP/ev" --src "$TMP/src" >/dev/null 2>&1
check "build failure exit" 4 "$?"
check "build failure recorded" 4 "$(cat "$TMP/ev/build-exit-code")"
if [ -s "$TMP/ev/test.jsonl" ]; then
  echo "ok   test evidence written despite build failure"
else
  echo "FAIL test evidence missing after build failure"
  failures=$((failures + 1))
fi

# 5) missing DSN variables -> environment error 70
env -u TEST_DATABASE_URL -u OUTBOX_TEST_DATABASE_URL -u MONEY_OUTBOX_TEST_DATABASE_URL -u TXRESCAN_TEST_DATABASE_URL \
  sh "$RUNNER" --evidence "$TMP/ev2" --src "$TMP/src" >/dev/null 2>&1
check "missing DSN exit" 70 "$?"

# 6) bad invocation -> 64
sh "$RUNNER" --evidence >/dev/null 2>&1
check "missing value exit" 64 "$?"
sh "$RUNNER" --bogus x >/dev/null 2>&1
check "unknown arg exit" 64 "$?"

if [ "$failures" -ne 0 ]; then
  echo "runner stub tests: FAIL ($failures)"
  exit 1
fi
echo "runner stub tests: PASS"
