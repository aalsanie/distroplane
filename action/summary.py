#!/usr/bin/env python3
"""Best-effort, post-upload GitHub Action summary. Never changes CLI outcomes."""

import json
import os
import re
import sys
from pathlib import Path
from urllib.parse import urlsplit

IDENTITY = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")
ARTIFACT_DIGEST = re.compile(r"sha256:[0-9a-fA-F]{64}\Z")
STATES = frozenset({"PLANNED", "READY", "RUNNING", "WAITING_EXTERNAL", "BLOCKED",
                    "PUBLISHED", "REJECTED", "FAILED", "CANCELLED"})
COMMANDS = frozenset({"plan", "apply", "reconcile", "publish", "resume"})


def identity(value):
    return value if isinstance(value, str) and IDENTITY.fullmatch(value) else None


def parse_result(raw):
    if not isinstance(raw, str) or not raw or len(raw) > 4 * 1024 * 1024:
        return None
    try:
        result = json.loads(raw)
    except (ValueError, TypeError, RecursionError):
        return None
    return result if isinstance(result, dict) and result.get("outputSchemaVersion") == "1" else None


def plan_from(value):
    if not isinstance(value, dict) or value.get("outputSchemaVersion") != "1":
        return None
    pid = identity(value.get("planId"))
    if not pid or not isinstance(value.get("path"), str) or not value["path"].strip():
        return None
    for field in ("targets", "operations"):
        count = value.get(field)
        if type(count) is not int or count < 0:
            return None
    return pid


def state_from(value, exit_code):
    if not isinstance(value, dict) or value.get("outputSchemaVersion") != "1":
        return None
    pid, rid = identity(value.get("planId")), identity(value.get("runId"))
    completed, cancelled = value.get("completed"), value.get("cancelled")
    if not pid or not rid or type(completed) is not bool or type(cancelled) is not bool:
        return None
    truncated = value.get("journalTruncatedTail", False)
    if type(truncated) is not bool:
        return None
    targets, operations = value.get("targets"), value.get("operations")
    if (not isinstance(targets, list) or not isinstance(operations, list)
            or not targets or not operations or len(targets) > 1024 or len(operations) > 1024):
        return None
    ambiguous = external = blocked = rejected = failed = False
    outstanding = not completed
    all_published = completed and not cancelled
    affected = []
    for kind, values in (("target", targets), ("operation", operations)):
        for item in values:
            if not isinstance(item, dict):
                return None
            ident = identity(item.get("id"))
            status = item.get("state")
            reconcile, unknown = item.get("reconcileRequired"), item.get("ambiguous")
            if (not ident or not isinstance(status, str) or status not in STATES or type(reconcile) is not bool
                    or type(unknown) is not bool):
                return None
            if kind == "operation" and not identity(item.get("targetId")):
                return None
            if status != "PUBLISHED" or reconcile or unknown:
                all_published = False
            ambiguous |= unknown
            external |= status == "WAITING_EXTERNAL" or reconcile
            blocked |= status == "BLOCKED"
            rejected |= status == "REJECTED"
            failed |= status in ("FAILED", "CANCELLED")
            outstanding |= status not in ("PUBLISHED", "REJECTED", "FAILED", "CANCELLED") or reconcile or unknown
            if kind == "target" and status != "PUBLISHED" and len(affected) < 5:
                affected.append((ident, status, unknown))
    failed |= cancelled
    expected = 6 if failed else 5 if rejected else 4 if outstanding else 0
    if expected != exit_code or (exit_code == 0 and not all_published):
        return None
    if truncated:
        all_published = False
    mixed = (failed or rejected) and (ambiguous or external or blocked or outstanding)
    if truncated:
        outcome = "Journal truncated; only the durable prefix is known"
    elif mixed:
        outcome = "Mixed terminal and outstanding target outcomes"
    elif ambiguous:
        outcome = "Uncertain external outcome; publication may already have occurred"
    elif external and not all_published:
        outcome = "Waiting for external review or reconciliation"
    elif blocked:
        outcome = "Blocked before completion"
    elif failed:
        outcome = "Failed or cancelled"
    elif rejected:
        outcome = "Rejected by a destination"
    elif all_published:
        outcome = "Recorded as published; public availability not independently verified"
    else:
        outcome = "Recorded run incomplete"
    return {"plan": pid, "run": rid, "outcome": outcome, "pending": exit_code == 4,
            "ambiguous": ambiguous, "truncated": truncated, "affected": affected,
            "published": all_published, "mixed": mixed}


def artifact(env, prefix):
    if env.get(prefix + "_OUTCOME") != "success":
        return None
    aid, url, digest = (env.get(prefix + suffix, "") for suffix in ("_ID", "_URL", "_DIGEST"))
    repo, run = env.get("GITHUB_REPOSITORY", ""), env.get("GITHUB_RUN_ID", "")
    if (not re.fullmatch(r"[1-9][0-9]{0,19}", aid)
            or not re.fullmatch(r"[1-9][0-9]{0,19}", run)
            or not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repo)
            or any(part in (".", "..") for part in repo.split("/"))
            or not ARTIFACT_DIGEST.fullmatch(digest)):
        return None
    # Python URL parsing removes embedded CR/LF/TAB; reject raw URL bytes first.
    if not isinstance(url, str) or len(url) > 512 or not re.fullmatch(r"[A-Za-z0-9:/._-]+", url):
        return None
    try:
        parts = urlsplit(url)
    except ValueError:
        return None
    expected = f"/{repo}/actions/runs/{run}/artifacts/{aid}"
    if (parts.scheme != "https" or parts.netloc != "github.com" or parts.path != expected
            or parts.query or parts.fragment or parts.username or parts.password):
        return None
    return (aid, url)


def interpret(env):
    command = env.get("SUMMARY_COMMAND", "")
    execute = env.get("SUMMARY_EXECUTE_OUTCOME", "")
    code_text = env.get("SUMMARY_EXIT_CODE", "")
    code = int(code_text) if re.fullmatch(r"[0-6]", code_text) else None
    raw = parse_result(env.get("SUMMARY_RESULT_JSON", "")) if execute == "success" else None
    detail = None
    if command in COMMANDS and raw is not None and code is not None:
        if command == "plan" and code == 0:
            pid = plan_from(raw)
            if pid:
                detail = {"plan": pid, "run": None, "outcome": "Planned; no publication attempted",
                          "pending": False, "ambiguous": False, "truncated": False,
                          "affected": [], "published": False, "mixed": False}
        elif command == "publish":
            plan = raw.get("plan")
            run = raw.get("run")
            pid = plan_from(plan)
            if pid and run is not None and raw.get("approved") is True:
                detail = state_from(run, code)
                if detail is not None and detail["plan"] != pid:
                    detail = None
        else:
            detail = state_from(raw, code)
    if detail is not None:
        # An output alone is never trusted as release identity; it must match the JSON.
        if env.get("SUMMARY_PLAN_ID", "") != detail["plan"]:
            detail = None
        elif detail["run"] and env.get("SUMMARY_RUN_ID", "") != detail["run"]:
            detail = None
        elif detail["run"] is None and env.get("SUMMARY_RUN_ID", ""):
            detail = None
    return command, execute, code, detail


def render(env):
    command, execute, code, detail = interpret(env)
    command_label = command if command in COMMANDS else "unknown command"
    lines = [f"### Distroplane: {command_label}", ""]
    recovery = artifact(env, "SUMMARY_RECOVERY")
    plan_artifact = artifact(env, "SUMMARY_PLAN_ARTIFACT")
    evidence = artifact(env, "SUMMARY_EVIDENCE")
    if detail is None:
        if execute != "success":
            outcome = "Setup or execution did not produce a verified CLI result"
        else:
            outcome = "CLI result unavailable or inconsistent; publication status unknown"
        next_action = "Inspect the original recorded run and CI logs without retrying publication. Preserve any existing plan, journal, provider snapshots and exact artifacts."
        lines.append(f"**Outcome:** {outcome}.")
    else:
        lines.append(f"**Outcome:** {detail['outcome']}.")
        lines.append(f"**Plan:** `{detail['plan']}`" +
                     (f" | **Run:** `{detail['run']}`" if detail["run"] else ""))
        if detail["affected"] and (detail["mixed"] or detail["pending"] or detail["ambiguous"]):
            lines.append("\n**Recorded target states:**")
            for ident, status, unknown in detail["affected"]:
                marker = " (ambiguous external effect)" if unknown else ""
                lines.append(f"- `{ident}`: {status}{marker}")
        if command == "plan":
            next_action = "Review the immutable plan and explicitly authorize its exact plan ID before any publication."
        elif detail["truncated"]:
            next_action = "Preserve and inspect the original journal's durable prefix before any recovery; never start a replacement publication."
        elif detail["ambiguous"]:
            next_action = "Reconcile the original recorded run before any retry; the external side effect may already have happened."
        elif detail["pending"]:
            next_action = "Inspect the original pending run and reconcile external acceptance; do not republish."
        elif code in (5, 6):
            next_action = "Inspect each recorded target outcome and preserve the original run; do not retry automatically."
        elif detail["published"]:
            next_action = "Verify public destination availability independently; local completion does not establish registry visibility."
        else:
            next_action = "Inspect the original recorded run before any further publication attempt."
    if detail and (detail["pending"] or detail["ambiguous"] or detail["truncated"]):
        if recovery:
            next_action += " Restore only the verified recovery bundle for this exact run before a permitted resume or reconciliation."
        else:
            next_action += " No verified recovery artifact is available from this run; preserve the original state through an approved mechanism."
    lines += ["", f"**Next:** {next_action}", ""]
    if recovery:
        aid, url = recovery
        lines.append(f"**Recovery artifact:** [Uploaded bundle #{aid}]({url}).")
        lines.append("**Recovery compatibility:** Restore to the same runner OS, CPU architecture, and exact absolute `GITHUB_WORKSPACE` path. A new job is not automatically compatible; preserve original artifacts, plan, journal, and provider snapshots.")
    else:
        lines.append("**Recovery artifact:** No verified recovery artifact is available from this run. Runner-local files are not guaranteed to persist into another job.")
    if plan_artifact:
        aid, url = plan_artifact
        lines.append(f"**Plan artifact:** [Uploaded plan #{aid}]({url}) (review only; not publication evidence).")
    if evidence:
        aid, url = evidence
        lines.append(f"**Evidence artifact:** [Uploaded evidence #{aid}]({url}) (recorded evidence; not proof of public availability).")
    return "\n".join(lines) + "\n"


def main():
    target = os.environ.get("GITHUB_STEP_SUMMARY", "")
    try:
        if not target:
            raise OSError("summary path absent")
        report = render(os.environ)
        with open(target, "a", encoding="utf-8", newline="\n") as output:
            output.write(report)
    except (OSError, ValueError, TypeError, UnicodeError, KeyError):
        # No user-controlled message and no effect on CLI outputs or enforcement.
        print("::warning::Distroplane summary unavailable.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
