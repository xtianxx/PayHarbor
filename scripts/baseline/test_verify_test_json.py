#!/usr/bin/env python3
"""Unit tests for scripts/baseline/verify-test-json.py (stdlib unittest).

Temp fixtures live under /tmp/opencode via TemporaryDirectory so no stray files
are left around. Run:  python3 scripts/baseline/test_verify_test_json.py
"""

import json
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
GATE = os.path.join(HERE, "verify-test-json.py")
TMP_ROOT = "/tmp/opencode"
os.makedirs(TMP_ROOT, exist_ok=True)


def event(action, package, test=None, elapsed=None):
    payload = {"Time": "2026-01-01T00:00:00Z", "Action": action, "Package": package}
    if test:
        payload["Test"] = test
    if elapsed is not None:
        payload["Elapsed"] = elapsed
    return payload


def complete(package, test, elapsed=0.1):
    """Normal go test -json shape: run, pass, package-level pass."""
    return [
        event("run", package, test),
        event("pass", package, test, elapsed),
        event("pass", package, elapsed=elapsed),
    ]


class GateTest(unittest.TestCase):
    def run_gate(self, events, test_exit=0, vet_exit=0, build_exit=0,
                 must_run="pkg core/repositories TestGood\n", allow_skip=None,
                 raw_lines=None):
        with tempfile.TemporaryDirectory(dir=TMP_ROOT) as tmp:
            json_path = os.path.join(tmp, "test.jsonl")
            if raw_lines is not None:
                with open(json_path, "w", encoding="utf-8") as handle:
                    for line in raw_lines:
                        handle.write(line + "\n")
            else:
                with open(json_path, "w", encoding="utf-8") as handle:
                    for item in events:
                        handle.write(json.dumps(item) + "\n")
            exit_path = os.path.join(tmp, "exit-code")
            with open(exit_path, "w", encoding="utf-8") as handle:
                handle.write(f"{test_exit}\n")
            vet_path = os.path.join(tmp, "vet-exit-code")
            with open(vet_path, "w", encoding="utf-8") as handle:
                handle.write(f"{vet_exit}\n")
            build_path = os.path.join(tmp, "build-exit-code")
            with open(build_path, "w", encoding="utf-8") as handle:
                handle.write(f"{build_exit}\n")
            must_path = os.path.join(tmp, "must-run.txt")
            with open(must_path, "w", encoding="utf-8") as handle:
                handle.write(must_run or "")
            summary_path = os.path.join(tmp, "summary.json")
            cmd = [
                sys.executable, GATE,
                "--json", json_path,
                "--exit-code", exit_path,
                "--vet-exit-code", vet_path,
                "--build-exit-code", build_path,
                "--must-run", must_path,
                "--summary", summary_path,
            ]
            if allow_skip is not None:
                allow_path = os.path.join(tmp, "allow-skip.txt")
                with open(allow_path, "w", encoding="utf-8") as handle:
                    handle.write(allow_skip)
                cmd += ["--allow-skip", allow_path]
            result = subprocess.run(cmd, capture_output=True)
            summary = None
            if os.path.exists(summary_path):
                with open(summary_path, encoding="utf-8") as handle:
                    summary = json.load(handle)
            return result.returncode, summary, result.stdout + result.stderr

    def test_all_pass(self):
        code, summary, _ = self.run_gate(complete("core/repositories", "TestGood"))
        self.assertEqual(code, 0)
        self.assertEqual(summary["result"], "pass")
        self.assertEqual(summary["tests_passed"], 1)
        self.assertEqual(summary["must_run_missing_run"], [])

    def test_unexpected_skip_fails(self):
        events = [
            event("run", "core/repositories", "TestGood"),
            event("skip", "core/repositories", "TestGood", 0.0),
            event("pass", "core/repositories", elapsed=0.1),
        ]
        code, summary, _ = self.run_gate(events)
        self.assertEqual(code, 2)
        self.assertIn("unexpected skip: core/repositories TestGood", " ".join(summary["violations"]))

    def test_allowlisted_skip_is_exempted(self):
        events = complete("core/repositories", "TestGood") + [
            event("run", "core/repositories", "Test_Merchant"),
            event("skip", "core/repositories", "Test_Merchant", 0.0),
        ]
        code, summary, out = self.run_gate(
            events,
            allow_skip="pkg core/repositories Test_Merchant\n",
        )
        self.assertEqual(code, 0)
        self.assertEqual(summary["skip_exemptions"], ["core/repositories Test_Merchant"])
        self.assertIn(b"exempted skip", out)

    def test_failed_test_fails(self):
        events = [
            event("run", "core/repositories", "TestGood"),
            event("fail", "core/repositories", "TestGood", 0.2),
            event("fail", "core/repositories", elapsed=0.2),
        ]
        code, summary, _ = self.run_gate(events)
        self.assertEqual(code, 2)
        self.assertEqual(summary["tests_failed"], 1)

    def test_missing_must_run_fails(self):
        events = complete("core/repositories", "TestOther")
        code, summary, _ = self.run_gate(events)
        self.assertEqual(code, 2)
        self.assertIn("must-run test has no run event: core/repositories TestGood",
                      " ".join(summary["violations"]))

    def test_missing_run_event_fails(self):
        events = [
            event("pass", "core/repositories", "TestGood", 0.1),
            event("pass", "core/repositories", elapsed=0.1),
        ]
        code, summary, _ = self.run_gate(events)
        self.assertEqual(code, 2)
        joined = " ".join(summary["violations"])
        self.assertIn("terminal event without run event: core/repositories TestGood", joined)
        self.assertIn("must-run test has no run event", joined)

    def test_missing_package_completion_fails(self):
        events = [
            event("run", "core/repositories", "TestGood"),
            event("pass", "core/repositories", "TestGood", 0.1),
        ]
        code, summary, _ = self.run_gate(events)
        self.assertEqual(code, 2)
        self.assertIn("must-run package has no terminal pass", " ".join(summary["violations"]))

    def test_truncated_stream_incomplete_test_fails(self):
        events = complete("core/repositories", "TestGood") + [
            event("run", "core/repositories", "TestSecond"),
        ]
        code, summary, _ = self.run_gate(events)
        self.assertEqual(code, 2)
        self.assertIn("test reached no terminal event", " ".join(summary["violations"]))

    def test_empty_must_run_is_evidence_error(self):
        code, summary, _ = self.run_gate(complete("core/repositories", "TestGood"), must_run="")
        self.assertEqual(code, 3)
        self.assertIsNone(summary)

    def test_empty_stream_is_evidence_error(self):
        code, summary, _ = self.run_gate([])
        self.assertEqual(code, 3)
        self.assertIsNone(summary)

    def test_non_object_json_is_evidence_error(self):
        code, summary, _ = self.run_gate(None, raw_lines=['[1,2,3]', '"text"'])
        self.assertEqual(code, 3)
        self.assertIsNone(summary)

    def test_unparsable_line_is_evidence_error(self):
        code, summary, _ = self.run_gate(
            None,
            raw_lines=[json.dumps(event("run", "core/repositories", "TestGood")),
                       '{"Action":"pass","Package":"core/repositories","Test":"TestGo'],
        )
        self.assertEqual(code, 3)
        self.assertIsNone(summary)

    def test_tool_package_without_tests_is_allowed(self):
        events = complete("core/repositories", "TestGood") + [
            event("start", "core/tools/something"),
            event("output", "core/tools/something"),
            event("skip", "core/tools/something", elapsed=0.0),
        ]
        code, summary, _ = self.run_gate(events)
        self.assertEqual(code, 0)
        self.assertEqual(summary["result"], "pass")

    def test_nonzero_exit_with_all_pass_fails(self):
        code, summary, _ = self.run_gate(complete("core/repositories", "TestGood"), test_exit=1)
        self.assertEqual(code, 2)
        self.assertIn("recorded go test exit code 1", " ".join(summary["violations"]))

    def test_vet_failure_fails(self):
        code, summary, _ = self.run_gate(complete("core/repositories", "TestGood"), vet_exit=2)
        self.assertEqual(code, 2)
        self.assertIn("recorded go vet exit code 2", " ".join(summary["violations"]))

    def test_build_failure_fails(self):
        code, summary, _ = self.run_gate(complete("core/repositories", "TestGood"), build_exit=1)
        self.assertEqual(code, 2)
        self.assertIn("recorded go build exit code 1", " ".join(summary["violations"]))

    def test_package_without_tests_required_fails(self):
        events = [event("skip", "core/repositories", elapsed=0.0)]
        code, summary, _ = self.run_gate(events)
        self.assertEqual(code, 2)
        joined = " ".join(summary["violations"])
        self.assertIn("must-run test has no run event", joined)
        self.assertIn("must-run package has no terminal pass", joined)

    def test_missing_evidence_file_is_evidence_error(self):
        with tempfile.TemporaryDirectory(dir=TMP_ROOT) as tmp:
            must_path = os.path.join(tmp, "must-run.txt")
            with open(must_path, "w", encoding="utf-8") as handle:
                handle.write("pkg core/repositories TestGood\n")
            result = subprocess.run([
                sys.executable, GATE,
                "--json", os.path.join(tmp, "absent.jsonl"),
                "--exit-code", os.path.join(tmp, "absent-exit"),
                "--must-run", must_path,
                "--summary", os.path.join(tmp, "summary.json"),
            ], capture_output=True)
            self.assertEqual(result.returncode, 3)

    def test_subtests_are_counted_individually(self):
        events = [
            event("run", "core/repositories", "TestParent"),
            event("run", "core/repositories", "TestParent/sub_one"),
            event("pass", "core/repositories", "TestParent/sub_one", 0.01),
            event("run", "core/repositories", "TestParent/sub_two"),
            event("pass", "core/repositories", "TestParent/sub_two", 0.01),
            event("pass", "core/repositories", "TestParent", 0.02),
            event("pass", "core/repositories", elapsed=0.02),
        ]
        code, summary, _ = self.run_gate(events, must_run="pkg core/repositories TestParent\n")
        self.assertEqual(code, 0)
        self.assertEqual(summary["tests_passed"], 3)


if __name__ == "__main__":
    unittest.main(verbosity=2)
