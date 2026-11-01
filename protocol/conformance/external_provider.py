#!/usr/bin/env python3
"""Independent provider protocol v1 fixture: no Distroplane library imports."""
import json
import os
from pathlib import Path
import sys
import time

LIMIT = 1 << 20
CAPABILITIES = ["plan", "apply", "reconcile"]


def reject_duplicates(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON key")
        result[key] = value
    return result


def error(code, message, retryable=False):
    return {"status": "error", "error": {
        "code": code, "message": message, "retryable": retryable,
    }}


def reply(request, mode):
    op = request["operation"]
    payload = request["payload"]
    if request["protocolVersion"] != "1" or op not in ("describe", "plan", "apply", "reconcile"):
        return error("PROTOCOL_ERROR", "unsupported protocol version or operation")
    if not isinstance(payload, dict):
        return error("PROTOCOL_ERROR", "payload must be an object")
    if op == "describe":
        caps = CAPABILITIES
        if mode == "missing-capability":
            caps = ["plan", "apply"]
        if mode == "unknown-capability":
            caps = CAPABILITIES + ["future"]
        versions = ["2"] if mode == "unsupported-version" else ["1"]
        return {"status": "ok", "payload": {
            "provider": {"name": "external", "version": "1.0.0"},
            "protocolVersions": versions, "capabilities": caps,
        }}
    if op == "plan":
        if not isinstance(payload.get("release"), dict) or not isinstance(payload.get("target"), dict):
            return error("PROTOCOL_ERROR", "missing release or target")
        configuration = payload["target"].get("configuration")
        if not isinstance(configuration, dict):
            return error("CONFIGURATION_ERROR", "invalid target configuration")
        operation = {
            "id": "publish", "kind": "publish", "sideEffecting": True,
            "timeoutMillis": 3000, "providerPayload": configuration,
        }
        if mode == "invalid-plan":
            operation["dependencies"] = ["missing"]
        return {"status": "ok", "payload": {"operations": [operation]}}
    required = ("planId", "targetId", "operationId", "idempotencyKey", "attempt", "providerPayload")
    if any(key not in payload for key in required):
        return error("PROTOCOL_ERROR", "missing execution field")
    execution = payload["providerPayload"]
    if not isinstance(execution, dict):
        return error("PROTOCOL_ERROR", "invalid execution payload")
    if op == "apply":
        if execution.get("mode") == "transient":
            return error("TRANSIENT_EXTERNAL_ERROR", "controlled temporary error", True)
        audit = execution.get("auditFile")
        if audit:
            with open(audit, "a", encoding="utf-8") as output:
                output.write("apply\n")
        state = "WAITING_EXTERNAL" if execution.get("mode") == "waiting" else "PUBLISHED"
    else:
        # Reconciliation observes only; it must not append a publication event.
        state = "PUBLISHED"
    if mode == "invalid-result":
        state = "UNKNOWN"
    return {"status": "ok", "payload": {"result": {
        "state": state, "providerState": "observed",
        "evidence": {"fixture": "external", "operationId": payload["operationId"]},
    }}}


def main():
    # Mode on executable name allows testing the CLI's environment isolation.
    marker = "distroplane-provider-external--"
    filename = Path(sys.argv[0]).name
    mode = filename.split(marker, 1)[1] if marker in filename else "normal"
    if len(sys.argv) == 3 and sys.argv[1] == "--mode":
        mode = sys.argv[2]
    if mode == "timeout":
        time.sleep(10)
    if mode == "crash":
        print("fixture crash", file=sys.stderr)
        return 70
    if mode == "empty":
        return 0
    if mode == "malformed":
        sys.stdout.write("{")
        return 0
    if mode == "oversized":
        sys.stdout.write("X" * (LIMIT + 1))
        return 0
    try:
        raw = sys.stdin.buffer.read(LIMIT + 1)
        if len(raw) > LIMIT:
            raise ValueError("request too large")
        request = json.loads(raw.decode("utf-8"), object_pairs_hook=reject_duplicates)
        if not isinstance(request, dict):
            raise ValueError("request must be an object")
        if not isinstance(request.get("requestId"), str) or not request["requestId"]:
            raise ValueError("invalid request ID")
        if not isinstance(request.get("operation"), str) or not isinstance(request.get("protocolVersion"), str):
            raise ValueError("invalid envelope")
        response = reply(request, mode)
        response.update(protocolVersion="1", requestId=request["requestId"],
                        operation=request["operation"])
        if mode == "wrong-id":
            response["requestId"] += "-wrong"
        if mode == "wrong-operation":
            response["operation"] = "reconcile" if request["operation"] != "reconcile" else "apply"
        if mode == "wrong-version":
            response["protocolVersion"] = "2"
        if mode == "dual-envelope":
            response["error"] = {"code": "PROTOCOL_ERROR", "message": "unexpected", "retryable": False}
        data = json.dumps(response, separators=(",", ":"), ensure_ascii=False).encode("utf-8")
        sys.stdout.buffer.write(data)
        return 0
    except (UnicodeError, ValueError, KeyError, TypeError) as exc:
        print("invalid provider request: " + str(exc), file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
