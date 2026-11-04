package io.github.aalsanie.distroplane.gradle;

import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.io.IOException;
import java.util.Set;
import java.util.regex.Pattern;

/** Strict, non-echoing interpretation of the public CLI JSON contract. */
final class CliFeedback {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final Pattern IDENTIFIER = Pattern.compile("[A-Za-z0-9][A-Za-z0-9._-]{0,127}");
    private static final Set<String> STATES = Set.of(
        "PLANNED", "READY", "RUNNING", "WAITING_EXTERNAL", "BLOCKED",
        "PUBLISHED", "REJECTED", "FAILED", "CANCELLED");

    record Detail(String summary, String nextGoal, String action) { }

    static Detail interpret(String goal, CliRunner.Result result) throws IOException {
        int exit = result.exitCode();
        if (exit < 0 || exit > 6) throw invalid();
        if (!result.stdout().isBlank()) {
            JsonNode root = document(result.stdout());
            return switch (goal) {
                case "doctor" -> doctor(root, exit);
                case "validate" -> validate(root, exit);
                case "plan" -> plan(root, exit);
                case "init" -> init(root, exit);
                case "publish" -> publish(root, exit);
                case "status", "resume" -> state(root, exit);
                default -> throw invalid();
            };
        }
        if (exit != 0 && !result.stderr().isBlank()) return error(document(result.stderr()), exit);
        throw invalid();
    }

    private static JsonNode document(String value) throws IOException {
        if (value.length() > (4 << 20)) throw invalid();
        try (JsonParser parser = JSON.getFactory().createParser(value)) {
            JsonNode node = JSON.readTree(parser);
            if (node == null || !node.isObject() || parser.nextToken() != null) throw invalid();
            requiredText(node, "outputSchemaVersion", false);
            if (!"1".equals(node.get("outputSchemaVersion").textValue())) throw invalid();
            return node;
        } catch (IOException e) {
            throw invalid();
        }
    }

    private static JsonNode member(JsonNode parent, String name) throws IOException {
        JsonNode value = parent.get(name);
        if (value == null || !value.isObject()) throw invalid();
        return value;
    }

    private static String requiredText(JsonNode parent, String name, boolean identifier) throws IOException {
        JsonNode value = parent.get(name);
        if (value == null || !value.isTextual() || value.textValue().isBlank()) throw invalid();
        String text = value.textValue();
        if (identifier && !IDENTIFIER.matcher(text).matches()) throw invalid();
        return text;
    }

    private static boolean booleanField(JsonNode parent, String name) throws IOException {
        JsonNode value = parent.get(name);
        if (value == null || !value.isBoolean()) throw invalid();
        return value.booleanValue();
    }

    private static int count(JsonNode parent, String name) throws IOException {
        JsonNode value = parent.get(name);
        if (value == null || !value.isIntegralNumber() || !value.canConvertToInt() || value.intValue() < 0)
            throw invalid();
        return value.intValue();
    }

    private static JsonNode list(JsonNode parent, String name) throws IOException {
        JsonNode value = parent.get(name);
        if (value == null || !value.isArray() || value.size() > 1024) throw invalid();
        return value;
    }

    private static Detail validate(JsonNode root, int exit) throws IOException {
        if (exit != 0 || !booleanField(root, "valid")) throw invalid();
        requiredText(root, "schemaVersion", false);
        int targets = count(root, "targets");
        return new Detail("configuration valid; " + targets + " destination(s)", null, "");
    }

    private static Detail init(JsonNode root, int exit) throws IOException {
        if (exit != 0) throw invalid();
        requiredText(root, "configPath", false);
        member(root, "artifact");
        member(root, "artifacts");
        list(root, "destinations");
        return new Detail("configuration created", "doctor",
            "Review generated metadata and run the configured doctor goal");
    }

    private static Detail plan(JsonNode root, int exit) throws IOException {
        if (exit != 0) throw invalid();
        String planId = requiredText(root, "planId", true);
        requiredText(root, "path", false);
        count(root, "targets");
        count(root, "operations");
        return new Detail("immutable plan " + planId + " saved", null,
            "Review the exact plan before explicit publication approval");
    }

    private static Detail publish(JsonNode root, int exit) throws IOException {
        JsonNode plan = member(root, "plan");
        requiredText(plan, "outputSchemaVersion", false);
        if (!"1".equals(plan.get("outputSchemaVersion").textValue())) throw invalid();
        requiredText(plan, "planId", true);
        requiredText(plan, "path", false);
        count(plan, "targets");
        count(plan, "operations");
        boolean approved = booleanField(root, "approved");
        JsonNode run = root.get("run");
        if (run == null || run.isNull()) {
            if (exit != 0 || approved) throw invalid();
            return new Detail("plan preview completed; nothing published", null, "");
        }
        if (!approved) throw invalid();
        requiredText(root, "journalPath", false);
        return state(run, exit);
    }

    private static Detail doctor(JsonNode root, int exit) throws IOException {
        boolean healthy = booleanField(root, "healthy");
        JsonNode checks = list(root, "checks");
        if (checks.isEmpty() || (healthy != (exit == 0)) ||
            (!healthy && exit != 1 && exit != 3)) throw invalid();
        String issue = null;
        String action = null;
        String existing = null;
        boolean remoteReachable = false;
        for (JsonNode check : checks) {
            if (!check.isObject()) throw invalid();
            String code = requiredText(check, "code", true);
            String severity = requiredText(check, "severity", false);
            requiredText(check, "subject", false);
            requiredText(check, "message", false);
            if (!Set.of("PASS", "WARN", "FAIL", "SKIP").contains(severity)) throw invalid();
            if ("REMOTE_REACHABLE".equals(code) && "PASS".equals(severity)) remoteReachable = true;
            if ("WARN".equals(severity) && existing == null &&
                Set.of("RUN_RESERVED", "RECONCILE_REQUIRED", "PLAN_AMBIGUOUS").contains(code)) existing = code;
            if ("FAIL".equals(severity) && issue == null) {
                issue = code;
                action = advice(code);
            }
        }
        if (healthy && issue != null || !healthy && issue == null) throw invalid();
        if (healthy && existing != null) return new Detail("existing release requires inspection: " + existing,
            "status", "Inspect the original recorded run rather than creating a new publication");
        if (healthy) return new Detail("readiness checks passed; " +
            (remoteReachable ? "remote TCP connectivity checked" : "remote connectivity not verified") +
            "; authorization and publication acceptance unverified", null, "");
        return new Detail("readiness blocked: " + issue, "doctor", action);
    }

    private static Detail state(JsonNode root, int exit) throws IOException {
        requiredText(root, "outputSchemaVersion", false);
        if (!"1".equals(root.get("outputSchemaVersion").textValue())) throw invalid();
        String planId = requiredText(root, "planId", true);
        String runId = requiredText(root, "runId", true);
        boolean completed = booleanField(root, "completed");
        boolean cancelled = booleanField(root, "cancelled");
        JsonNode tail = root.get("journalTruncatedTail");
        if (tail != null && !tail.isBoolean()) throw invalid();
        boolean truncated = tail != null && tail.booleanValue();
        JsonNode targets = list(root, "targets");
        JsonNode operations = list(root, "operations");
        if (targets.isEmpty() || operations.isEmpty()) throw invalid();
        boolean ambiguous = false, external = false, blocked = false, rejected = false;
        boolean credentialBlocked = false;
        boolean failed = cancelled, pending = !completed, allPublished = completed && !cancelled;
        for (JsonNode target : targets) {
            if (!target.isObject()) throw invalid();
            requiredText(target, "id", true);
            String status = requiredText(target, "state", false);
            if (!STATES.contains(status)) throw invalid();
            boolean reconcile = booleanField(target, "reconcileRequired");
            boolean unknown = booleanField(target, "ambiguous");
            ambiguous |= unknown;
            external |= reconcile && !unknown;
            if (!status.equals("PUBLISHED") || reconcile || unknown) allPublished = false;
        }
        for (JsonNode operation : operations) {
            if (!operation.isObject()) throw invalid();
            requiredText(operation, "id", true);
            requiredText(operation, "targetId", true);
            String status = requiredText(operation, "state", false);
            if (!STATES.contains(status)) throw invalid();
            boolean reconcile = booleanField(operation, "reconcileRequired");
            boolean unknown = booleanField(operation, "ambiguous");
            ambiguous |= unknown;
            external |= reconcile && !unknown;
            if (!status.equals("PUBLISHED") || reconcile || unknown) allPublished = false;
            if ("FAILED".equals(status) || "CANCELLED".equals(status)) failed = true;
            else if ("REJECTED".equals(status)) rejected = true;
            else if (!"PUBLISHED".equals(status) || reconcile) pending = true;
            JsonNode reason = operation.get("errorCode");
            if (reason != null && !reason.isTextual()) throw invalid();
            if ("BLOCKED".equals(status)) {
                blocked = true;
                credentialBlocked |= reason != null && Set.of("CREDENTIAL_UNAVAILABLE", "CREDENTIAL_RESOLUTION_FAILED").contains(reason.textValue());
            }
        }
        int expected = failed ? 6 : rejected ? 5 : pending ? 4 : 0;
        if (expected != exit || (exit == 0 && !allPublished)) throw invalid();
        String outcome = failed && (ambiguous || external || blocked) || rejected && (ambiguous || external || blocked)
            ? "mixed terminal and outstanding destinations"
            : ambiguous ? "ambiguous external outcome"
            : external ? "awaiting external acceptance"
            : blocked ? (credentialBlocked ? "blocked by unavailable credentials" : "blocked before completion")
            : failed ? "failed or cancelled"
            : rejected ? "rejected"
            : allPublished ? "recorded as published (public availability unverified)"
            : "pending";
        if (truncated) return new Detail("run " + runId + " (plan " + planId +
            "): journal has a truncated final record", "status",
            "Preserve the original journal and verify its durable prefix before recovery");
        String next = exit == 0 ? null : "status";
        String action = ambiguous
            ? "Reconcile the original recorded run before any retry; do not republish"
            : external ? "Check the original external submission by reconciling its recorded run"
            : blocked ? (credentialBlocked ? "Restore the approved credential environment source, then resume the same run" : "Restore the recorded prerequisite, then resume the same run")
            : exit != 0 ? "Inspect the recorded cause; do not create a replacement run"
            : "Verify destination availability independently";
        return new Detail("run " + runId + " (plan " + planId + "): " + outcome, next, action);
    }

    private static Detail error(JsonNode root, int exit) throws IOException {
        JsonNode err = member(root, "error");
        String kind = requiredText(err, "kind", true);
        requiredText(err, "message", false);
        JsonNode codeNode = err.get("code");
        String code = codeNode == null ? kind : requiredText(err, "code", true);
        JsonNode hint = err.get("hint");
        if (hint != null && !hint.isTextual()) throw invalid();
        String next = switch (code) {
            case "RUN_AMBIGUOUS", "RUN_ALREADY_EXISTS", "RELEASE_CONFLICT", "STATE_CORRUPT",
                 "AWAITING_EXTERNAL", "UNSAFE_RETRY", "INTERRUPTED" -> "status";
            case "MISSING_CONFIG", "INVALID_CONFIG", "MISSING_ARTIFACT",
                 "PROVIDER_NOT_FOUND", "VERSION_MISMATCH" -> "doctor";
            default -> null;
        };
        return new Detail("CLI returned " + code + " (exit " + exit + ")", next, advice(code));
    }

    private static String advice(String code) {
        return switch (code) {
            case "MISSING_CREDENTIAL", "CREDENTIAL_UNAVAILABLE", "CREDENTIAL_RESOLUTION_FAILED" ->
                "Restore the approved credential environment source and rerun doctor";
            case "MISSING_ARTIFACT", "ARTIFACT_CHANGED" ->
                "Build the mapped Gradle archive task and verify its original bytes";
            case "MISSING_CONFIG", "INVALID_CONFIG" ->
                "Correct distroplane.config in the root project; use a regular file inside the Gradle root";
            case "PROVIDER_NOT_FOUND", "VERSION_MISMATCH", "ARCH_MISMATCH" ->
                "Restore the expected provider executable and verify its original identity";
            case "RUN_AMBIGUOUS" ->
                "Select the exact recorded release/run using distroplane.release or distroplane.run";
            case "AWAITING_EXTERNAL", "UNSAFE_RETRY", "INTERRUPTED" ->
                "Inspect and reconcile the original run before considering further publication";
            case "RUN_ALREADY_EXISTS", "RELEASE_CONFLICT", "STATE_CORRUPT" ->
                "Preserve the original plan, run catalog, and journal; inspect existing state";
            case "APPROVAL_REQUIRED" ->
                "Review the immutable plan and explicitly set distroplane.approve or distroplane.confirmPlan";
            case "REMOTE_UNREACHABLE", "REMOTE_INVALID" ->
                "Inspect the remote endpoint; connectivity does not prove publication acceptance";
            default -> "Inspect the reported prerequisite and preserve existing release state";
        };
    }

    private static IOException invalid() {
        return new IOException("Invalid or unsupported structured CLI response; raw output suppressed");
    }
}
