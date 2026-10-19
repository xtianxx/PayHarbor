#!/usr/bin/env sh
# Generates the development-only .env for the ENV-02 offline environment.
#
# Guarantees:
#   * creates the file only when it does not exist (O_EXCL) and never overwrites;
#   * mode 0600 from creation;
#   * every secret is freshly generated from OS entropy;
#   * no secret value is ever printed to stdout or stderr;
#   * the mnemonic is a real BIP39 phrase derived from the wordlist that ships
#     with the pinned Trust Wallet Core submodule - no publicly known mnemonic
#     is ever used as a real value, and no wordlist is invented locally;
#   * no external OIDC, chain RPC, or custody signer credentials are configured.
#
# Usage:
#   scripts/generate-offline-env.sh                  # create ./.env if missing
#   scripts/generate-offline-env.sh --path ./x.env    # create an explicit path
#   scripts/generate-offline-env.sh --verify          # self-test BIP39 only
#
set -eu

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
WORDLIST="$REPO_ROOT/third_party/trustwallet/wallet-core/trezor-crypto/include/TrezorCrypto/bip39_english.h"

ENV_PATH="$REPO_ROOT/.env"
MODE="generate"

usage() {
  cat <<'USAGE'
Usage: scripts/generate-offline-env.sh [--path <file>] [--verify]

  --path <file>  Target .env path (default: <repo>/.env). The file is only
                 created when it is absent; an existing file is never touched.
  --verify       Validate the embedded BIP39 implementation against the official
                 test vector and exit without writing anything.
  --help         Show this help.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --path)
      if [ $# -lt 2 ]; then
        echo "--path requires a value" >&2
        exit 64
      fi
      ENV_PATH="$2"
      shift 2
      ;;
    --verify)
      MODE="verify"
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 64
      ;;
  esac
done

if [ ! -f "$WORDLIST" ]; then
  echo "BIP39 wordlist not found: $WORDLIST" >&2
  echo "Initialize the pinned submodule first:" >&2
  echo "  git submodule update --init --recursive third_party/trustwallet/wallet-core" >&2
  exit 1
fi

if [ "$MODE" = "verify" ]; then
  VECTORS="$REPO_ROOT/third_party/trustwallet/wallet-core/tests/common/HDWallet/bip39_vectors.json"
  if [ ! -f "$VECTORS" ]; then
    echo "official BIP39 vector file not found: $VECTORS" >&2
    echo "Initialize the pinned submodule first:" >&2
    echo "  git submodule update --init --recursive third_party/trustwallet/wallet-core" >&2
    exit 1
  fi
  python3 - "$WORDLIST" "$VECTORS" <<'PY'
import hashlib
import json
import re
import sys

wordlist_path, vectors_path = sys.argv[1], sys.argv[2]

with open(wordlist_path, encoding="utf-8") as handle:
    raw = handle.read()

words = re.findall(r'"([a-z]+)"', raw)
if len(words) != 2048:
    raise SystemExit(f"wordlist {wordlist_path} has {len(words)} words, expected 2048")
if words[0] != "abandon" or words[-1] != "zoo":
    raise SystemExit("wordlist does not look like the BIP39 English list")

def entropy_to_mnemonic(entropy: bytes) -> str:
    digest = hashlib.sha256(entropy).digest()
    checksum_bits = len(entropy) * 8 // 32
    bits = "".join(f"{byte:08b}" for byte in entropy) + "".join(f"{byte:08b}" for byte in digest)[:checksum_bits]
    return " ".join(words[int(bits[index:index + 11], 2)] for index in range(0, len(bits), 11))

def mnemonic_is_valid(mnemonic: str) -> bool:
    parts = mnemonic.split()
    if len(parts) not in (12, 15, 18, 21, 24):
        return False
    try:
        indices = [words.index(part) for part in parts]
    except ValueError:
        return False
    bits = "".join(f"{index:011b}" for index in indices)
    entropy_bits = len(bits) * 32 // 33
    entropy = bytes(int(bits[index:index + 8], 2) for index in range(0, entropy_bits, 8))
    digest = hashlib.sha256(entropy).digest()
    expected = "".join(f"{byte:08b}" for byte in digest)[: len(bits) - entropy_bits]
    return bits[entropy_bits:] == expected

with open(vectors_path, encoding="utf-8") as handle:
    vectors = json.load(handle)["english"]

checked = 0
for entropy_hex, mnemonic, _seed_hex, _xprv in vectors:
    if entropy_to_mnemonic(bytes.fromhex(entropy_hex)) != mnemonic:
        raise SystemExit(f"BIP39 derivation mismatch for entropy {entropy_hex}")
    if not mnemonic_is_valid(mnemonic):
        raise SystemExit(f"BIP39 checksum validation rejected official vector {entropy_hex}")
    checked += 1

print(f"BIP39 self-test: PASS ({checked}/{len(vectors)} official english vectors from the pinned submodule, 2048-word list)")
PY
  exit 0
fi

if [ -e "$ENV_PATH" ]; then
  echo "refusing to overwrite existing file: $ENV_PATH" >&2
  echo "delete it yourself if you really want a fresh development environment" >&2
  exit 1
fi

python3 - "$WORDLIST" "$ENV_PATH" <<'PY'
import hashlib
import os
import re
import secrets
import sys

wordlist_path, env_path = sys.argv[1], sys.argv[2]

with open(wordlist_path, encoding="utf-8") as handle:
    raw = handle.read()
words = re.findall(r'"([a-z]+)"', raw)
if len(words) != 2048 or words[0] != "abandon" or words[-1] != "zoo":
    raise SystemExit(f"refusing to generate: unexpected wordlist at {wordlist_path}")

def entropy_to_mnemonic(entropy: bytes) -> str:
    digest = hashlib.sha256(entropy).digest()
    checksum_bits = len(entropy) * 8 // 32
    bits = "".join(f"{byte:08b}" for byte in entropy) + "".join(f"{byte:08b}" for byte in digest)[:checksum_bits]
    return " ".join(words[int(bits[index:index + 11], 2)] for index in range(0, len(bits), 11))

def mnemonic_is_valid(mnemonic: str) -> bool:
    parts = mnemonic.split()
    if len(parts) not in (12, 15, 18, 21, 24):
        return False
    try:
        indices = [words.index(part) for part in parts]
    except ValueError:
        return False
    bits = "".join(f"{index:011b}" for index in indices)
    entropy_bits = len(bits) * 32 // 33
    entropy = bytes(int(bits[index:index + 8], 2) for index in range(0, entropy_bits, 8))
    digest = hashlib.sha256(entropy).digest()
    expected = "".join(f"{byte:08b}" for byte in digest)[: len(bits) - entropy_bits]
    return bits[entropy_bits:] == expected

mnemonic = entropy_to_mnemonic(secrets.token_bytes(16))
if not mnemonic_is_valid(mnemonic):
    raise SystemExit("generated mnemonic failed its own checksum validation")

master_key = secrets.token_hex(32)
portal_jwt_secret = secrets.token_hex(32)
admin_password = secrets.token_urlsafe(24)
db_password = secrets.token_hex(24)
db_user = "postgres"
db_name = "gateway"

content = f"""# Generated by scripts/generate-offline-env.sh on demand.
#
# DEVELOPMENT ONLY. Every secret below is random and disposable; the mnemonic was
# generated from fresh OS entropy and must never be reused with real funds.
# This file is git-ignored (see .gitignore) and must stay mode 0600.
#
# Chain RPC endpoints, external OIDC, and custody signer credentials are
# intentionally absent: GATEWAY_OFFLINE=true keeps listeners and all periodic
# money/event workers disabled.

APP_ENV=development
GATEWAY_OFFLINE=true
PORT=:3001

GATEWAY_LOG_FORMAT=text
GATEWAY_LOG_LEVEL=info
HTTP_READ_TIMEOUT=15s
HTTP_WRITE_TIMEOUT=30s
HTTP_IDLE_TIMEOUT=60s
CORS_ALLOWED_ORIGINS=http://localhost:3001
ALLOW_PRIVATE_WEBHOOK_URLS=true
ALLOW_AUTOMIGRATE_IN_PRODUCTION=false

# Development software signer only. Production custody must use an external
# signer adapter; this switch can never bypass that rule.
SIGNER_MODE=software
ALLOW_SOFTWARE_SIGNER_IN_PRODUCTION=false
METRICS_BEARER_TOKEN=

# PostgreSQL, isolated on loopback port 55432 (see deploy/compose/offline/docker-compose.yml).
POSTGRES_USER={db_user}
POSTGRES_PASSWORD={db_password}
POSTGRES_DB={db_name}
DATABASE_URL="host=127.0.0.1 port=55432 user={db_user} password={db_password} dbname={db_name} sslmode=disable"

MASTER_KEY={master_key}
PORTAL_JWT_SECRET={portal_jwt_secret}
MNEMONIC_PHRASE="{mnemonic}"

ADMIN_NAME="Gateway Admin"
ADMIN_EMAIL=admin@localhost
ADMIN_PASSWORD={admin_password}

PROVIDER_HEALTH_INTERVAL=1m
PROVIDER_HEALTH_TIMEOUT=8s
"""

descriptor = os.open(env_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
    handle.write(content)
os.chmod(env_path, 0o600)

print(f"created {env_path} mode 0600 with {len(content.splitlines())} lines")
print("generated secrets (values not shown): POSTGRES_PASSWORD, MASTER_KEY, PORTAL_JWT_SECRET, MNEMONIC_PHRASE, ADMIN_PASSWORD")
print(f"mnemonic: BIP39 valid, {len(mnemonic.split())} words, freshly generated")
PY

chmod 600 "$ENV_PATH"

cat <<EOF
Offline .env ready at $ENV_PATH (mode 0600, never overwritten).

Next steps (from the repository root):
  docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml up -d --build
  docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml logs -f gateway
  docker compose --env-file .env -f deploy/compose/offline/docker-compose.yml down   # keeps the data volume

See docs/offline-development.md for the full workflow and the offline limitations.
EOF
