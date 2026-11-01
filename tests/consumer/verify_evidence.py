#!/usr/bin/env python3
"""Independent consumer verification of Distroplane evidence v1."""
import argparse
import hashlib
import json
import re
from pathlib import Path

DIGEST = re.compile(r"^sha256:[0-9a-f]{64}$")
TERMINAL = {"PUBLISHED", "REJECTED"}
STATES = TERMINAL | {"WAITING_EXTERNAL", "FAILED", "BLOCKED", "READY", "CANCELLED"}


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
    if journal is not None:
        journal_bytes = Path(journal).read_bytes()
        require(len(journal_bytes) >= record["bytes"], "journal is shorter than evidence")
        require(sha256(journal_bytes[:record["bytes"]]) == record["digest"],
                "journal bytes disagree with evidence")
        if not record.get("truncatedTail", False):
            require(len(journal_bytes) == record["bytes"], "unreported journal tail")

    targets = list_value(bundle.get("targets"), "targets")
    require(targets, "evidence has no targets")
    seen_targets = set()
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
        require(ops, "target has no operations")
        operation_ids = set()
        for op in ops:
            object_value(op, "operation")
            operation_id = text_value(op.get("id"), "operation ID")
            require(operation_id not in operation_ids, "duplicate operation")
            operation_ids.add(operation_id)
            require(op.get("state") in STATES, "unknown operation state")
            require(type(op.get("attempt", 0)) is int and op.get("attempt", 0) >= 0, "invalid attempt")
            text_value(op.get("kind"), "operation kind")
            text_value(op.get("observedAt"), "operation observation")
            if op["state"] in TERMINAL:
                require("evidence" in op and op["evidence"] is not None, "terminal operation is missing evidence")
        if expected_state is not None:
            require(all(op["state"] == expected_state for op in ops),
                    "operation state differs from expected outcome")
    if expected_state in TERMINAL:
        require(run["completed"], "terminal run was not completed")
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
