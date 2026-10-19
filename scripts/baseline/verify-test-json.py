#!/usr/bin/env python3
"""Fail-closed gate over `go test -json` evidence produced by run-tests.sh.

Inputs
  --json PATH             raw `go test -json` stream (JSON Lines)
  --exit-code PATH        file containing the real `go test` exit code
  --vet-exit-code PATH    optional file with the real `go vet` exit code
  --build-exit-code PATH  optional file with the real `go build` exit code
  --must-run PATH         lines: `pkg <import-path> <TestName>`
  --allow-skip PATH       optional, same format: the ONLY skips tolerated
  --summary PATH          output summary JSON path

Exit codes
  0  pass
  2  gate violation
  3  evidence error (missing/unreadable file, non-object JSON line, empty
     stream, empty must-run list)

Fail-closed rules
  R1  every JSONL line must parse to a JSON object; the stream and the must-run
      list must both be non-empty.
  R2  recorded build/vet/test exit codes must be 0.
  R3  every test with a `run` event must reach a terminal `pass|fail|skip`
      event for the same (package, test) — this catches truncated streams.
  R4  every terminal event must have a preceding `run` event.
  R5  no test-terminal `fail`; no package-terminal `fail`.
  R6  every must-run test must have BOTH a `run` and a terminal `pass`.
  R7  every must-run package must have a package-level terminal `pass`
      (package completion event).
  R8  no test `skip` unless the exact (package, test) pair is in the
      allow-skip file; exemptions are reported in the summary.
"""

import argparse
import json
import sys

EXIT_PASS = 0
EXIT_VIOLATION = 2
EXIT_EVIDENCE = 3

TERMINAL = ("pass", "fail", "skip")


class EvidenceError(Exception):
    pass


def read_pairs(path, required):
    pairs = []
    if path is None:
        if required:
            raise EvidenceError("must-run list path is missing")
        return pairs
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            tokens = line.split()
            if not tokens or tokens[0].startswith("#"):
                continue
            if tokens[0] == "pkg":
                tokens = tokens[1:]
            if len(tokens) < 2:
                raise EvidenceError(f"{path}: malformed line: {line.rstrip()}")
            pairs.append((tokens[0], tokens[1]))
    if required and not pairs:
        raise EvidenceError(f"{path}: must-run list is empty")
    return pairs


def load_events(path):
    events = []
    with open(path, encoding="utf-8") as handle:
        for number, line in enumerate(handle, start=1):
            line = line.strip()
            if not line:
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError as exc:
                raise EvidenceError(f"{path}:{number}: {exc}") from exc
            if not isinstance(event, dict):
                raise EvidenceError(f"{path}:{number}: JSON line is not an object")
            events.append(event)
    if not events:
        raise EvidenceError(f"{path}: test stream is empty")
    return events


def read_int(path):
    with open(path, encoding="utf-8") as handle:
        return int(handle.read().strip())


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--json", required=True)
    parser.add_argument("--exit-code", required=True)
    parser.add_argument("--vet-exit-code", default=None)
    parser.add_argument("--build-exit-code", default=None)
    parser.add_argument("--must-run", required=True)
    parser.add_argument("--allow-skip", default=None)
    parser.add_argument("--summary", required=True)
    args = parser.parse_args()

    try:
        test_exit = read_int(args.exit_code)
        vet_exit = read_int(args.vet_exit_code) if args.vet_exit_code else None
        build_exit = read_int(args.build_exit_code) if args.build_exit_code else None
        events = load_events(args.json)
        must_run = read_pairs(args.must_run, required=True)
        allow_skip = set(read_pairs(args.allow_skip, required=False))
    except (OSError, ValueError, EvidenceError) as exc:
        print(f"evidence error: {exc}", file=sys.stderr)
        return EXIT_EVIDENCE

    runs, terminal, package_terminal = set(), {}, {}
    for event in events:
        package = event.get("Package")
        if not package:
            continue
        action = event.get("Action", "")
        test = event.get("Test")
        if test:
            if action == "run":
                runs.add((package, test))
            elif action in TERMINAL:
                terminal[(package, test)] = action
        elif action in TERMINAL:
            package_terminal[package] = action

    violations = []
    if build_exit is not None and build_exit != 0:
        violations.append(f"recorded go build exit code {build_exit} != 0")
    if vet_exit is not None and vet_exit != 0:
        violations.append(f"recorded go vet exit code {vet_exit} != 0")
    if test_exit != 0:
        violations.append(f"recorded go test exit code {test_exit} != 0")

    for (package, test) in sorted(runs):
        if (package, test) not in terminal:
            violations.append(f"test reached no terminal event (truncated stream?): {package} {test}")
    for (package, test) in sorted(terminal):
        if (package, test) not in runs:
            violations.append(f"terminal event without run event: {package} {test}")
        if terminal[(package, test)] == "fail":
            violations.append(f"failed test: {package} {test}")
    for package, action in sorted(package_terminal.items()):
        if action == "fail":
            violations.append(f"package failed: {package}")

    exemptions = sorted(pair for pair, action in terminal.items() if action == "skip" and pair in allow_skip)
    for (package, test), action in sorted(terminal.items()):
        if action == "skip" and (package, test) not in allow_skip:
            violations.append(f"unexpected skip: {package} {test}")

    must_packages = sorted({package for package, _ in must_run})
    missing_run = []
    missing_pass = []
    for package, test in must_run:
        if (package, test) not in runs:
            missing_run.append((package, test))
            violations.append(f"must-run test has no run event: {package} {test}")
        if terminal.get((package, test)) != "pass":
            missing_pass.append((package, test))
            violations.append(f"must-run test did not pass: {package} {test}")
    for package in must_packages:
        if package_terminal.get(package) != "pass":
            violations.append(f"must-run package has no terminal pass (package completion missing): {package}")

    result = "pass" if not violations else "violation"
    summary = {
        "result": result,
        "test_exit_code": test_exit,
        "vet_exit_code": vet_exit,
        "build_exit_code": build_exit,
        "events": len(events),
        "tests_run": len(runs),
        "tests_terminal": len(terminal),
        "tests_passed": sum(1 for a in terminal.values() if a == "pass"),
        "tests_failed": sum(1 for a in terminal.values() if a == "fail"),
        "tests_skipped": sum(1 for a in terminal.values() if a == "skip"),
        "packages_with_terminal": sorted(package_terminal),
        "skip_exemptions": [f"{p} {t}" for p, t in exemptions],
        "must_run_total": len(must_run),
        "must_run_missing_run": [f"{p} {t}" for p, t in missing_run],
        "must_run_missing_pass": [f"{p} {t}" for p, t in missing_pass],
        "violations": violations,
    }
    try:
        with open(args.summary, "w", encoding="utf-8") as handle:
            json.dump(summary, handle, indent=2, sort_keys=True)
            handle.write("\n")
    except OSError as exc:
        print(f"evidence error: cannot write summary: {exc}", file=sys.stderr)
        return EXIT_EVIDENCE

    print(
        f"gate={result} run={len(runs)} terminal={len(terminal)} passed={summary['tests_passed']} "
        f"failed={summary['tests_failed']} skipped={summary['tests_skipped']} "
        f"exemptions={len(exemptions)} must_run={len(must_run)} "
        f"missing_run={len(missing_run)} missing_pass={len(missing_pass)} "
        f"build_exit={build_exit} vet_exit={vet_exit} test_exit={test_exit}"
    )
    for violation in violations:
        print(f"  violation: {violation}", file=sys.stderr)
    for pair in exemptions:
        print(f"  exempted skip (reported, not passed): {pair[0]} {pair[1]}")
    return EXIT_PASS if not violations else EXIT_VIOLATION


if __name__ == "__main__":
    sys.exit(main())
