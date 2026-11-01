#!/usr/bin/env python3
"""Independent consumer verification of Distroplane evidence v1."""
import argparse
import hashlib
import json
import re
import zlib
from pathlib import Path

DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
TERMINAL = {"PUBLISHED", "REJECTED", "FAILED", "CANCELLED"}
STATES = TERMINAL | {"PLANNED", "READY", "RUNNING", "WAITING_EXTERNAL", "BLOCKED"}
PRIORITY = ("REJECTED", "FAILED", "CANCELLED", "BLOCKED", "WAITING_EXTERNAL",
            "RUNNING", "READY", "PLANNED")
FRAME_MAGIC = b"DPJ1"
MAX_FRAME_BYTES = 1 << 20
EVENT_TYPES = {
    "RUN_STARTED", "RUN_COMPLETED", "RUN_CANCELLED", "OPERATION_READY",
    "LEASE_ACQUIRED", "LEASE_RENEWED", "LEASE_EXPIRED", "LEASE_RELEASED",
    "ATTEMPT_STARTED", "CREDENTIAL_RESOLVED", "PROVIDER_PROCESS_STARTED",
    "SIDE_EFFECT_DISPATCHED", "PROVIDER_RESPONSE_RECEIVED",
    "OPERATION_WAITING_EXTERNAL", "OPERATION_PUBLISHED", "OPERATION_REJECTED",
    "OPERATION_FAILED", "OPERATION_RESULT", "OPERATION_CANCELLED",
    "OUTCOME_AMBIGUOUS", "RECONCILE_STARTED", "RECONCILE_RESULT",
    "RETRY_AUTHORIZED",
}



def strict_object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError(f"duplicate evidence key {key!r}")
        value[key] = item
    return value


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha256(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def object_value(value, label):
    require(isinstance(value, dict), f"{label} must be an object")
    return value


def list_value(value, label):
    require(isinstance(value, list), f"{label} must be a list")
    return value


def text_value(value, label):
    require(isinstance(value, str) and bool(value.strip()), f"{label} must be a non-empty string")
    return value


def digest_value(value, label):
    require(isinstance(value, str) and DIGEST.fullmatch(value) is not None, f"{label} is not a SHA-256 digest")
    return value



def journal_events(raw, plan_id, run_id):
    """Decode independent journal v2 frames, CRCs, ordering, and identity."""
    events = []
    offset = 0
    while offset < len(raw):
        require(len(raw) - offset >= 12, "incomplete journal frame header")
        header = raw[offset:offset + 12]
        require(header[:4] == FRAME_MAGIC, "invalid journal frame magic")
        length = int.from_bytes(header[4:8], "big")
        checksum = int.from_bytes(header[8:12], "big")
        require(0 < length <= MAX_FRAME_BYTES, "invalid journal frame length")
        end = offset + 12 + length
        require(end <= len(raw), "incomplete journal frame payload")
        payload = raw[offset + 12:end]
        require(zlib.crc32(payload) & 0xffffffff == checksum, "journal CRC mismatch")
        try:
            event = json.loads(payload.decode("utf-8"), object_pairs_hook=strict_object,
                               parse_constant=lambda _: (_ for _ in ()).throw(ValueError("nonfinite journal JSON")))
        except (UnicodeError, json.JSONDecodeError) as exc:
            raise ValueError("invalid journal event JSON") from exc
        object_value(event, "journal event")
        require(type(event.get("schemaVersion")) is int and event["schemaVersion"] == 2,
                "unsupported journal event version")
        require(type(event.get("sequence")) is int and event["sequence"] == len(events) + 1,
                "journal event sequence mismatch")
        require(event.get("runId") == run_id, "journal event run ID mismatch")
        text_value(event.get("observedAt"), "journal observation time")
        require(event.get("type") in EVENT_TYPES, "unsupported journal event type")
        object_value(event.get("payload"), "journal event payload")
        if not events:
            require(event["type"] == "RUN_STARTED" and event.get("planId") == plan_id,
                    "journal is not bound to the evidence plan")
        else:
            require("planId" not in event, "unexpected plan ID on journal event")
            require(event["type"] not in ("RUN_STARTED",), "duplicate journal start")
        if events and events[-1]["type"] == "RUN_COMPLETED":
            raise ValueError("journal event after run completion")
        events.append(event)
        offset = end
    require(events, "journal contains no durable events")
    return events


def verify_journal(record, journal, plan_id, run_id, run):
    data = Path(journal).read_bytes()
    length = record["bytes"]
    require(len(data) >= length, "journal is shorter than evidence")
    durable = data[:length]
    require(sha256(durable) == record["digest"], "journal bytes disagree with evidence")
    tail = data[length:]
    if record.get("truncatedTail", False):
        require(tail, "journal truncated tail is absent")
        if len(tail) >= 12:
            require(tail[:4] == FRAME_MAGIC, "invalid journal tail magic")
            size = int.from_bytes(tail[4:8], "big")
            require(0 < size <= MAX_FRAME_BYTES and len(tail) < 12 + size,
                    "journal tail contains a complete or invalid frame")
    else:
        require(not tail, "unreported journal tail")
    events = journal_events(durable, plan_id, run_id)
    require(len(events) == record["events"], "journal event count mismatch")
    completed = events[-1]["type"] == "RUN_COMPLETED"
    cancelled = any(event["type"] == "RUN_CANCELLED" for event in events)
    require(run["completed"] == completed and run["cancelled"] == cancelled,
            "run lifecycle disagrees with journal")
    return events


def aggregate_state(operations, started=True):
    if not started:
        return "PLANNED"
    if not operations or all(op["state"] == "PUBLISHED" for op in operations):
        return "PUBLISHED"
    states = {op["state"] for op in operations}
    return next(state for state in PRIORITY if state in states)


def verify(raw, *, expected_digest=None, expected_plan=None, expected_run=None,
           expected_state=None, journal=None, artifact=None):
    if expected_digest is not None:
        require(sha256(raw) == digest_value(expected_digest, "expected evidence digest"), "evidence digest mismatch")
    try:
        bundle = json.loads(raw.decode("utf-8"), object_pairs_hook=strict_object,
                            parse_constant=lambda name: (_ for _ in ()).throw(ValueError("nonfinite JSON")))
    except (UnicodeError, json.JSONDecodeError) as exc:
        raise ValueError(f"invalid evidence JSON: {exc}") from exc
    object_value(bundle, "bundle")
    require(bundle.get("schemaVersion") == "1", "unsupported evidence schema")
    require(bundle.get("protocolVersion") == "1", "unsupported provider protocol")
    plan_id = text_value(bundle.get("planId"), "plan ID")
    if expected_plan is not None:
        require(plan_id == expected_plan, "evidence plan identity mismatch")
    run = object_value(bundle.get("run"), "run")
    run_id = text_value(run.get("id"), "run ID")
    if expected_run is not None:
        require(run_id == expected_run, "evidence run identity mismatch")
    require(type(run.get("completed")) is bool and type(run.get("cancelled")) is bool,
            "run completion flags must be booleans")
    if run["completed"]:
        text_value(run.get("completedAt"), "completion timestamp")
    else:
        require("completedAt" not in run, "incomplete run claims completion timestamp")
    if run["cancelled"]:
        text_value(run.get("cancelledAt"), "cancellation timestamp")
    text_value(run.get("startedAt"), "start timestamp")
    text_value(bundle.get("observedAt"), "observation timestamp")

    release = object_value(bundle.get("release"), "release")
    text_value(release.get("id"), "release identity")
    artifacts = list_value(release.get("artifacts"), "artifacts")
    require(len(artifacts) > 0, "release has no artifacts")
    names = set()
    for value in artifacts:
        item = object_value(value, "artifact")
        name = text_value(item.get("name"), "artifact name")
        require(name not in names, "duplicate artifact")
        names.add(name)
        digest_value(item.get("digest"), "artifact digest")
        require(type(item.get("size")) is int and item["size"] >= 0, "invalid artifact size")
    if artifact is not None:
        found = [a for a in artifacts if a["name"] == artifact[0]]
        require(len(found) == 1, "expected artifact missing")
        raw_artifact = Path(artifact[1]).read_bytes()
        require(sha256(raw_artifact) == found[0]["digest"] and len(raw_artifact) == found[0]["size"],
                "artifact bytes do not match evidence")

    record = object_value(bundle.get("journal"), "journal")
    digest_value(record.get("digest"), "journal digest")
    require(type(record.get("events")) is int and record["events"] > 0, "invalid journal event count")
    require(type(record.get("bytes")) is int and record["bytes"] > 0, "invalid journal length")
    require(type(record.get("schemaVersion")) is int and record["schemaVersion"] == 2,
            "unsupported journal schema")
    text_value(record.get("reference"), "journal reference")
    require(type(record.get("truncatedTail", False)) is bool, "invalid truncated-tail flag")
    if journal is not None:
        events = verify_journal(record, journal, plan_id, run_id, run)
        require(record["reference"] == Path(journal).name, "journal reference mismatch")

    targets = list_value(bundle.get("targets"), "targets")
    require(targets, "evidence has no targets")
    seen_targets = set()
    all_operations = set()
    for target in targets:
        object_value(target, "target")
        target_id = text_value(target.get("id"), "target ID")
        require(target_id not in seen_targets, "duplicate target")
        seen_targets.add(target_id)
        provider = object_value(target.get("provider"), "provider")
        text_value(provider.get("name"), "provider name")
        text_value(provider.get("version"), "provider version")
        state = target.get("state")
        require(state in STATES, "unknown target state")
        if expected_state is not None:
            require(state == expected_state, "target state differs from expected outcome")
        ops = list_value(target.get("operations"), "operations")
        require(type(target.get("reconcileRequired", False)) is bool and
                type(target.get("ambiguous", False)) is bool, "invalid target recovery flags")
        operation_ids = set()
        for op in ops:
            object_value(op, "operation")
            operation_id = text_value(op.get("id"), "operation ID")
            require(operation_id not in all_operations, "duplicate operation")
            all_operations.add(operation_id)
            operation_ids.add(operation_id)
            require(op.get("state") in STATES, "unknown operation state")
            require(type(op.get("attempt", 0)) is int and op.get("attempt", 0) >= 0, "invalid attempt")
            text_value(op.get("kind"), "operation kind")
            text_value(op.get("observedAt"), "operation observation")
            require(type(op.get("reconcileRequired", False)) is bool and
                    type(op.get("ambiguous", False)) is bool and
                    type(op.get("retryable", False)) is bool, "invalid operation recovery flags")
        require(state == aggregate_state(ops), "target state disagrees with operations")
        require(target.get("reconcileRequired", False) == any(op.get("reconcileRequired", False) for op in ops),
                "target reconciliation flag disagrees with operations")
        require(target.get("ambiguous", False) == any(op.get("ambiguous", False) for op in ops),
                "target ambiguity flag disagrees with operations")
    if run["completed"]:
        require(all(op["state"] in TERMINAL for target in targets for op in target["operations"]),
                "completed run contains unfinished operations")
    if journal is not None:
        bindings = {op["id"]: (target["id"], op) for target in targets for op in target["operations"]}
        for event in events[1:]:
            if event["type"] in ("RUN_COMPLETED", "RUN_CANCELLED"):
                continue
            op_id = event.get("operationId")
            require(op_id in bindings and event.get("targetId") == bindings[op_id][0],
                    "journal references unknown or mismatched operation")
    return bundle


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--expected-digest")
    parser.add_argument("--expected-plan")
    parser.add_argument("--expected-run")
    parser.add_argument("--expected-state", choices=sorted(STATES))
    parser.add_argument("--journal", type=Path)
    parser.add_argument("--artifact-name")
    parser.add_argument("--artifact-path", type=Path)
    args = parser.parse_args()
    if (args.artifact_name is None) != (args.artifact_path is None):
        parser.error("--artifact-name and --artifact-path must be provided together")
    try:
        verify(args.evidence.read_bytes(),
               expected_digest=args.expected_digest,
               expected_plan=args.expected_plan,
               expected_run=args.expected_run,
               expected_state=args.expected_state,
               journal=args.journal,
               artifact=(args.artifact_name, args.artifact_path) if args.artifact_name else None)
    except (ValueError, OSError) as exc:
        parser.exit(1, f"consumer evidence rejected: {exc}\n")
    print("consumer evidence verified")


if __name__ == "__main__":
    main()
