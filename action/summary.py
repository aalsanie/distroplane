#!/usr/bin/env python3
"""Best-effort, post-upload GitHub Action summary. Never changes CLI outcomes."""

import json
import os
import re
import sys
import unicodedata
from pathlib import Path
from urllib.parse import urlsplit

ARTIFACT_DIGEST = re.compile(r"sha256:[0-9a-fA-F]{64}\Z")
STATES = frozenset({"PLANNED", "READY", "RUNNING", "WAITING_EXTERNAL", "BLOCKED",
                    "PUBLISHED", "REJECTED", "FAILED", "CANCELLED"})
COMMANDS = frozenset({"plan", "apply", "reconcile", "publish", "resume"})
DIAGNOSTICS = {
    "STATE_CORRUPT": "Preserve and restore the original plan, journal, exact artifacts and retained provider executables; seek compatible recovery.",
    "RUN_AMBIGUOUS": "List existing runs and select the exact run or release; never choose arbitrarily.",
    "RUN_ALREADY_EXISTS": "Inspect the existing recorded run instead of starting another publication.",
    "RELEASE_CONFLICT": "Inspect the original immutable plan and run; do not create a replacement.",
    "UNSAFE_RETRY": "Preserve original files and seek manual recovery; do not resume an unsupported retry grant or republish.",
    "PROVIDER_NOT_FOUND": "Restore the original provider executable and verify its recorded identity.",
    "VERSION_MISMATCH": "Restore the original provider executable and verify its recorded identity.",
    "MISSING_ARTIFACT": "Restore the exact originally planned artifact bytes; do not create replacement artifacts.",
    "MISSING_CONFIG": "Restore the original configuration for the existing release.",
    "INVALID_CONFIG": "Inspect and correct the original configuration without replacing the recorded release.",
    "INTERRUPTED": "Inspect the original recorded run and reconcile possible external dispatch before resuming.",
    "AWAITING_EXTERNAL": "Inspect and reconcile the original recorded run before any further publication.",
    "APPROVAL_REQUIRED": "Review the immutable plan and use explicit matching approval."
}


def identity(value):
    if not isinstance(value, str) or not value:
        return None
    try:
        if len(value.encode("utf-8")) > 256:
            return None
    except UnicodeError:
        return None
    whitespace = "\t\n\v\f\r \x85\xa0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"
    if value != value.strip(whitespace) or any(unicodedata.category(c) == "Cc" for c in value):
        return None
    return value


def display_identity(value):
    return "".join(f"\\u{{{ord(c):x}}}" if c in "`<>&" or unicodedata.category(c) in ("Cf", "Zl", "Zp") else c
                   for c in value)


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate key")
        result[key] = value
    return result


def parse_result(raw):
    if not isinstance(raw, str) or not raw or len(raw) > 4 * 1024 * 1024:
        return None
    try:
        result = json.loads(raw, object_pairs_hook=unique_object)
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
    legacy = value.get("legacyRetryPending", False)
    if type(truncated) is not bool or type(legacy) is not bool:
        return None
    targets, operations = value.get("targets"), value.get("operations")
    if (not isinstance(targets, list) or not isinstance(operations, list)
            or not targets):
        return None
    ambiguous = external = blocked = rejected = failed = False
    credential = dependency = False
    outstanding = False
    all_published = completed and not cancelled
    may_exist = truncated or legacy
    affected = []
    target_ids = set()
    operation_ids = set()
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
            if kind == "target":
                if ident in target_ids:
                    return None
                target_ids.add(ident)
            else:
                if ident in operation_ids or identity(item.get("targetId")) not in target_ids:
                    return None
                operation_ids.add(ident)
                reason = item.get("errorCode", "")
                if not isinstance(reason, str):
                    return None
                credential |= status == "BLOCKED" and reason in ("CREDENTIAL_UNAVAILABLE", "CREDENTIAL_RESOLUTION_FAILED")
                dependency |= status == "BLOCKED" and reason == "DEPENDENCY_TERMINAL"
                may_exist |= unknown or reconcile or status not in ("PLANNED", "READY", "BLOCKED")
            if status != "PUBLISHED" or reconcile or unknown:
                all_published = False
            ambiguous |= unknown
            external |= (status == "WAITING_EXTERNAL" or reconcile) and not unknown
            blocked |= status == "BLOCKED"
            rejected |= status == "REJECTED"
            failed |= status in ("FAILED", "CANCELLED")
            outstanding |= status not in ("PUBLISHED", "REJECTED", "FAILED", "CANCELLED") or reconcile or unknown
            if kind == "target" and status != "PUBLISHED" and len(affected) < 5:
                affected.append((ident, status, unknown))
    failed |= cancelled
    expected = 6 if failed else 5 if rejected else 4 if not completed or outstanding else 0
    if expected != exit_code or (exit_code == 0 and not all_published):
        return None
    noop = all_published and not operations and not truncated and not legacy
    may_exist |= ambiguous or external
    if truncated or legacy or noop:
        all_published = False
    mixed = (failed or rejected) and (ambiguous or external or blocked or outstanding)
    if legacy:
        outcome = "Unsupported retry authorization; manual recovery required"
    elif truncated:
        outcome = "Journal truncated; only the durable prefix is known"
    elif mixed:
        outcome = "Mixed terminal and outstanding target outcomes"
    elif ambiguous:
        outcome = "Uncertain external outcome; publication may already have occurred"
    elif external and not all_published:
        outcome = "Waiting for external review or reconciliation"
    elif blocked:
        outcome = "Blocked before publication dispatch by an unavailable credential" if credential else "Blocked by an unresolved prerequisite or dependency"
    elif failed:
        outcome = "Failed or cancelled"
    elif rejected:
        outcome = "Rejected by a destination"
    elif noop:
        outcome = "Completed no-op; no publication operations were planned"
    elif all_published:
        outcome = "Recorded as published; public availability not independently verified"
    else:
        outcome = "Recorded run incomplete"
    return {"plan": pid, "run": rid, "outcome": outcome, "pending": exit_code == 4,
            "ambiguous": ambiguous, "truncated": truncated, "affected": affected,
            "published": all_published, "mixed": mixed, "noop": noop, "external": external,
            "blocked": blocked, "credential": credential, "manual": dependency or (blocked and (failed or rejected)),
            "may_exist": may_exist, "legacy": legacy}


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
                          "affected": [], "published": False, "mixed": False, "noop": False,
                          "external": False, "blocked": False, "credential": False,
                          "manual": False, "may_exist": False, "legacy": False}
        elif command == "publish":
            plan = raw.get("plan")
            run = raw.get("run")
            pid = plan_from(plan)
            if pid and run is not None and raw.get("approved") is True:
                detail = state_from(run, code)
                if detail is not None and (detail["plan"] != pid or
                                           plan["targets"] != len(run["targets"]) or
                                           plan["operations"] != len(run["operations"])):
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
        diagnostic = env.get("SUMMARY_DIAGNOSTIC_CODE", "")
        if execute == "success" and code in (1, 2, 3, 4, 5, 6) and not env.get("SUMMARY_RESULT_JSON", "") and diagnostic in DIAGNOSTICS:
            outcome += f" ({diagnostic})"
            next_action = DIAGNOSTICS[diagnostic]
        lines.append(f"**Outcome:** {outcome}.")
    else:
        lines.append(f"**Outcome:** {detail['outcome']}.")
        lines.append(f"**Plan:** `{display_identity(detail['plan'])}`" +
                     (f" | **Run:** `{display_identity(detail['run'])}`" if detail["run"] else ""))
        if detail["run"]:
            lines.append(f"**Publication may already exist (recorded run):** {str(detail['may_exist']).lower()}.")
            lines.append("**Evidence:** Recorded snapshot, not fresh external verification.")
        if detail["affected"] and (detail["mixed"] or detail["pending"] or detail["ambiguous"]):
            lines.append("\n**Recorded target states:**")
            for ident, status, unknown in detail["affected"]:
                marker = " (ambiguous external effect)" if unknown else ""
                lines.append(f"- `{display_identity(ident)}`: {status}{marker}")
        if command == "plan":
            next_action = "Review the immutable plan and explicitly authorize its exact plan ID before any publication."
        elif detail["legacy"]:
            next_action = "Preserve the original plan and journal and seek manual recovery; do not republish or resume this grant."
        elif detail["truncated"]:
            next_action = "Preserve and inspect the original journal's durable prefix before any recovery; never start a replacement publication."
        elif detail["ambiguous"]:
            next_action = "Reconcile the original recorded run before any retry; the external side effect may already have happened."
        elif detail["external"]:
            next_action = "Check the original external submission using read-only reconciliation; do not republish."
        elif detail["manual"]:
            next_action = "Inspect the recorded prerequisite and terminal outcomes of the original run; a human decision is required before further work on this same run."
        elif detail["blocked"]:
            source = "approved credential environment source" if detail["credential"] else "recorded prerequisite"
            next_action = f"Restore the {source}, then resume the same run; do not republish."
        elif detail["pending"]:
            next_action = "Inspect the original recorded run before further work; do not republish."
        elif code in (5, 6):
            next_action = "Inspect each recorded target outcome and preserve the original run; do not retry automatically."
        elif detail["noop"]:
            next_action = "Inspect recorded evidence if needed; no publication was attempted by this run."
        elif detail["published"]:
            next_action = "Inspect the recorded evidence. Verify public destination availability independently; local completion does not establish registry visibility."
        else:
            next_action = "Inspect the original recorded run before any further publication attempt."
    if detail and detail["run"] and (detail["pending"] or detail["ambiguous"] or detail["external"] or detail["truncated"] or detail["legacy"]):
        if detail["legacy"] or detail["truncated"]:
            next_action += " Retain the exact original files and provider executables."
        elif detail["external"] or detail["ambiguous"]:
            next_action += " Select the exact Run ID above and enable reconcile-only for resume."
        elif detail["blocked"] and not detail["manual"]:
            next_action += " Select the exact Run ID above for resume."
        if recovery:
            next_action += " Restore only the verified recovery bundle for this exact run before any permitted recovery."
        else:
            next_action += " No verified recovery artifact is available from this run; preserve the original state through an approved mechanism."
    lines += ["", f"**Next:** {next_action}", ""]
    if recovery and detail is not None and detail["run"]:
        aid, url = recovery
        lines.append(f"**Recovery artifact:** [Uploaded bundle #{aid}]({url}).")
        lines.append("**Recovery compatibility:** Restore to the same runner OS, CPU architecture, and exact absolute `GITHUB_WORKSPACE` path. A new job is not automatically compatible; preserve original artifacts, plan, journal, and provider snapshots.")
    else:
        lines.append("**Recovery artifact:** No verified recovery artifact is available from this run. Runner-local files are not guaranteed to persist into another job.")
    if plan_artifact:
        aid, url = plan_artifact
        lines.append(f"**Plan artifact:** [Uploaded plan #{aid}]({url}) (review only; not publication evidence).")
    if evidence and detail is not None and detail["run"]:
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
