#!/usr/bin/env python3
"""Build a synthetic DSN env file (0600) for the baseline test container.

Input : a 0600 env file produced by scripts/generate-offline-env.sh, containing
        POSTGRES_USER / POSTGRES_PASSWORD / POSTGRES_DB (synthetic values).
Output: a 0600 file exporting the four DSN variables the repository's Go tests
        consult, all pointing at the same in-network gateway_test database:
          OUTBOX_TEST_DATABASE_URL, MONEY_OUTBOX_TEST_DATABASE_URL,
          TEST_DATABASE_URL, TXRESCAN_TEST_DATABASE_URL
        (OUTBOX_TEST_DATABASE_URL has the highest precedence in the test helpers;
        writing all four prevents any host DSN from leaking into the run.)
"""
import argparse
import os
import re
import sys

KEYS = ("POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB")
DSN_VARS = (
    "OUTBOX_TEST_DATABASE_URL",
    "MONEY_OUTBOX_TEST_DATABASE_URL",
    "TEST_DATABASE_URL",
    "TXRESCAN_TEST_DATABASE_URL",
)


def parse_env(path):
    values = {}
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, _, raw = line.partition("=")
            raw = raw.strip().strip('"').strip("'")
            values[key.strip()] = raw
    return values


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--env", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--host", default="postgres")
    parser.add_argument("--port", default="5432")
    parser.add_argument("--db", default="gateway_test")
    args = parser.parse_args()

    src = parse_env(args.env)
    missing = [key for key in KEYS if not src.get(key)]
    if missing:
        print(f"missing keys in {args.env}: {', '.join(missing)}", file=sys.stderr)
        return 1
    if not re.fullmatch(r"[A-Za-z0-9_.-]+", args.db):
        print("unsafe database name", file=sys.stderr)
        return 1

    dsn = (
        f"host={args.host} port={args.port} user={src['POSTGRES_USER']} "
        f"password={src['POSTGRES_PASSWORD']} dbname={args.db} sslmode=disable"
    )
    lines = ["# synthetic DSN file for the A3 baseline test container", *[f"{k}={dsn}" for k in DSN_VARS]]
    descriptor = os.open(args.out, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
        handle.write("\n".join(lines) + "\n")
    os.chmod(args.out, 0o600)
    print(f"wrote {args.out} mode 0600 with {len(DSN_VARS)} DSN variables (values not shown)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
