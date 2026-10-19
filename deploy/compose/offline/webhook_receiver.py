#!/usr/bin/env python3
"""Dev-only webhook receiver for the gateway offline stack (LOCAL-WEBHOOK-01).

Scope and limits (read before reuse):
  * Local test fixture only. It is attached to the compose `internal` network of
    the offline stack and publishes no host port, so only containers on that
    network (the gateway) can reach it.
  * It does NOT verify the X-Gateway-Signature HMAC, does NOT persist anything,
    and does NOT touch orders, ledgers, payment sessions, or funds. A 200 from
    this process proves transport reachability of the configured URL only. It is
    never evidence of a real payment, and never evidence of production-grade
    asynchronous webhook delivery.
  * Logs carry a non-sensitive summary only: method, path, content type, body
    length, whether a signature header was present, and event_type/event_id from
    the JSON body. The raw body, signature values, and credentials are never
    logged, and merchant/domain identifiers are never echoed.
"""

from __future__ import annotations

import json
import os
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

MAX_BODY_BYTES = int(os.environ.get("MAX_BODY_BYTES", "65536"))
PORT = int(os.environ.get("PORT", "8080"))
WEBHOOK_PATH = os.environ.get("WEBHOOK_PATH", "/webhook")

_log_lock = threading.Lock()


def _log(message: str) -> None:
    with _log_lock:
        print(message, flush=True)


def _summary(raw: bytes) -> dict[str, str]:
    """Extract only non-sensitive identifiers from a JSON body."""
    try:
        payload = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        return {}
    if not isinstance(payload, dict):
        return {}
    summary: dict[str, str] = {}
    for field in ("event_type", "event_id"):
        value = payload.get(field)
        if isinstance(value, (str, int)):
            summary[field] = str(value)[:128]
    return summary


class _Handler(BaseHTTPRequestHandler):
    server_version = "gateway-webhook-test/1.0"
    protocol_version = "HTTP/1.1"

    def _respond(self, status: int, payload: dict[str, Any]) -> None:
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        if self.path.split("?", 1)[0] == "/health":
            self._respond(200, {"ok": True, "service": "webhook-test", "mode": "dev-only"})
            return
        self._respond(200, {"ok": True, "hint": "POST %s" % WEBHOOK_PATH})

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            length = 0
        if length < 0 or length > MAX_BODY_BYTES:
            self._respond(413, {"ok": False, "error": "body too large"})
            return

        raw = self.rfile.read(length) if length > 0 else b""
        event = _summary(raw)
        _log(
            "[webhook-test] received method=POST path=%s content_type=%s bytes=%d "
            "signature_header=%s event_type=%s event_id=%s"
            % (
                self.path,
                self.headers.get("Content-Type", ""),
                len(raw),
                "present" if self.headers.get("X-Gateway-Signature") else "absent",
                event.get("event_type", "<none>"),
                event.get("event_id", "<none>"),
            )
        )
        self._respond(
            200,
            {
                "ok": True,
                "received": True,
                "event_type": event.get("event_type"),
                "event_id": event.get("event_id"),
            },
        )

    def log_message(self, format: str, *args: Any) -> None:
        # Suppress the default per-request access log; only the summary line is emitted.
        return


def main() -> int:
    server = ThreadingHTTPServer(("0.0.0.0", PORT), _Handler)
    server.daemon_threads = True
    _log(
        "[webhook-test] dev-only receiver listening on 0.0.0.0:%d - no signature "
        "verification, no persistence, no funds; logs a non-sensitive summary only" % PORT
    )
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
