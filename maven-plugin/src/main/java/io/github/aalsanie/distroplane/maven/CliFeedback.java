package io.github.aalsanie.distroplane.maven;

import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.HashSet;
import java.util.Set;
import java.util.regex.Pattern;

/** Strict, non-echoing interpretation of the public CLI JSON contract. */
final class CliFeedback {
    private static final ObjectMapper JSON = new ObjectMapper().enable(JsonParser.Feature.STRICT_DUPLICATE_DETECTION);
    private static final Pattern CODE = Pattern.compile("[A-Za-z0-9][A-Za-z0-9._-]{0,127}");
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
        if (identifier && !validIdentifier(text)) throw invalid();
        return text;
    }

    private static boolean validIdentifier(String value) {
        return !value.isEmpty() && StandardCharsets.UTF_8.newEncoder().canEncode(value) &&
            value.getBytes(StandardCharsets.UTF_8).length <= 256 &&
            !edgeSpace(value.codePointAt(0)) && !edgeSpace(value.codePointBefore(value.length())) &&
            value.codePoints().noneMatch(Character::isISOControl);
    }

    private static boolean edgeSpace(int c) {
        return Character.isSpaceChar(c) || c == 0x85 || c >= 9 && c <= 13;
    }

    private static String display(String value) {
        StringBuilder out = new StringBuilder();
        value.codePoints().forEach(c -> {
            if (Character.getType(c) == Character.FORMAT || c == 0x2028 || c == 0x2029)
                out.append(String.format("\\u{%x}", c));
            else out.appendCodePoint(c);
        });
        return out.toString();
    }

    private static String requiredCode(JsonNode parent, String name) throws IOException {
        String value = requiredText(parent, name, false);
        if (!CODE.matcher(value).matches()) throw invalid();
        return value;
    }

    private static JsonNode stateList(JsonNode parent, String name) throws IOException {
        JsonNode value = parent.get(name);
        if (value == null || !value.isArray()) throw invalid();
        return value;
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
        return new Detail("immutable plan " + display(planId) + " saved", null,
            "Review the exact plan before explicit publication approval");
    }

    private static Detail publish(JsonNode root, int exit) throws IOException {
        JsonNode plan = member(root, "plan");
        requiredText(plan, "outputSchemaVersion", false);
        if (!"1".equals(plan.get("outputSchemaVersion").textValue())) throw invalid();
        String planId = requiredText(plan, "planId", true);
        requiredText(plan, "path", false);
        int targets = count(plan, "targets");
        int operations = count(plan, "operations");
        boolean approved = booleanField(root, "approved");
        JsonNode run = root.get("run");
        if (run == null || run.isNull()) {
            if (exit != 0 || approved) throw invalid();
            return new Detail("plan preview completed; nothing published", null, "");
        }
        if (!approved || !planId.equals(requiredText(run, "planId", true)) ||
            targets != stateList(run, "targets").size() || operations != stateList(run, "operations").size()) throw invalid();
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
        for (JsonNode check : checks) {
            if (!check.isObject()) throw invalid();
            String code = requiredCode(check, "code");
            String severity = requiredText(check, "severity", false);
            requiredText(check, "subject", false);
            requiredText(check, "message", false);
            if (!Set.of("PASS", "WARN", "FAIL", "SKIP").contains(severity)) throw invalid();
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
        if (healthy) return new Detail("readiness checks passed; publication acceptance unverified", null, "");
        return new Detail("readiness blocked: " + issue, "doctor", action);
    }

    private static Detail state(JsonNode root, int exit) throws IOException {
        requiredText(root, "outputSchemaVersion", false);
        if (!"1".equals(root.get("outputSchemaVersion").textValue())) throw invalid();
        String planId = requiredText(root, "planId", true);
        String runId = requiredText(root, "runId", true);
        boolean completed = booleanField(root, "completed");
        boolean cancelled = booleanField(root, "cancelled");
        boolean truncated = root.has("journalTruncatedTail") && booleanField(root, "journalTruncatedTail");
        boolean legacy = root.has("legacyRetryPending") && booleanField(root, "legacyRetryPending");
        JsonNode targets = stateList(root, "targets");
        JsonNode operations = stateList(root, "operations");
        if (targets.isEmpty()) throw invalid();
        boolean ambiguous = false, external = false, blocked = false, rejected = false;
        boolean credentialBlocked = false, dependencyBlocked = false, mayExist = truncated || legacy;
        boolean failed = cancelled, pending = !completed, allPublished = completed && !cancelled;
        Set<String> targetIds = new HashSet<>(), operationIds = new HashSet<>();
        for (JsonNode target : targets) {
            if (!target.isObject() || !targetIds.add(requiredText(target, "id", true))) throw invalid();
            String status = requiredText(target, "state", false);
            if (!STATES.contains(status)) throw invalid();
            boolean reconcile = booleanField(target, "reconcileRequired");
            boolean unknown = booleanField(target, "ambiguous");
            ambiguous |= unknown;
            external |= ("WAITING_EXTERNAL".equals(status) || reconcile) && !unknown;
            if (!status.equals("PUBLISHED") || reconcile || unknown) allPublished = false;
        }
        for (JsonNode operation : operations) {
            if (!operation.isObject() || !operationIds.add(requiredText(operation, "id", true)) ||
                !targetIds.contains(requiredText(operation, "targetId", true))) throw invalid();
            String status = requiredText(operation, "state", false);
            if (!STATES.contains(status)) throw invalid();
            boolean reconcile = booleanField(operation, "reconcileRequired");
            boolean unknown = booleanField(operation, "ambiguous");
            ambiguous |= unknown;
            external |= ("WAITING_EXTERNAL".equals(status) || reconcile) && !unknown;
            if (!status.equals("PUBLISHED") || reconcile || unknown) allPublished = false;
            if ("FAILED".equals(status) || "CANCELLED".equals(status)) failed = true;
            else if ("REJECTED".equals(status)) rejected = true;
            else if (!"PUBLISHED".equals(status) || reconcile || unknown) pending = true;
            mayExist |= reconcile || unknown || !Set.of("PLANNED", "READY", "BLOCKED").contains(status);
            JsonNode reason = operation.get("errorCode");
            if (reason != null && !reason.isTextual()) throw invalid();
            if ("BLOCKED".equals(status)) {
                blocked = true;
                credentialBlocked |= reason != null && Set.of("CREDENTIAL_UNAVAILABLE", "CREDENTIAL_RESOLUTION_FAILED").contains(reason.textValue());
                dependencyBlocked |= reason != null && "DEPENDENCY_TERMINAL".equals(reason.textValue());
            }
        }
        int expected = failed ? 6 : rejected ? 5 : pending ? 4 : 0;
        if (expected != exit || (exit == 0 && !allPublished)) throw invalid();
        boolean noop = allPublished && operations.isEmpty();
        mayExist |= ambiguous || external;
        String outcome = (failed || rejected) && (ambiguous || external || blocked || pending)
            ? "mixed terminal and outstanding destinations"
            : ambiguous ? "ambiguous external outcome; publication may already have occurred"
            : external ? "awaiting external acceptance"
            : blocked ? (credentialBlocked ? "blocked by unavailable credentials" : "blocked before completion")
            : failed ? "failed or cancelled"
            : rejected ? "rejected"
            : noop ? "completed no-op; no publication operations were planned"
            : allPublished ? "recorded as published (public availability unverified)"
            : "pending";
        String identity = "run " + display(runId) + " (plan " + display(planId) + "): ";
        String recorded = "; publication may already exist (recorded run): " + mayExist +
            "; evidence is a recorded snapshot, not fresh external verification";
        String selector = "; select the exact recorded run shown above using distroplane.run";
        if (legacy) return new Detail(identity + "unsupported retry authorization" + recorded, null,
            "Preserve the original plan and journal and seek manual recovery; do not republish or resume this grant");
        if (truncated) return new Detail(identity + "journal has a truncated final record" + recorded, "status",
            "Preserve the original journal and verify its durable prefix before recovery" + selector);
        String next = exit == 0 ? null : "status";
        String action = ambiguous
            ? "Reconcile the original recorded run before any retry; do not republish; use resume with reconcile-only" + selector
            : external ? "Check the original external submission by reconciling its recorded run; use resume with reconcile-only" + selector
            : blocked && (dependencyBlocked || failed || rejected)
                ? "Inspect the recorded prerequisite and terminal outcomes; a human decision is required before further work on this same run" + selector
            : blocked ? (credentialBlocked ? "Restore the approved credential environment source, then resume the same run" : "Restore the recorded prerequisite, then resume the same run") + selector
            : exit != 0 ? "Inspect the recorded cause; do not create a replacement run" + selector
            : noop ? "Inspect recorded evidence if needed; no publication was attempted by this run"
            : "Inspect recorded evidence and verify destination availability independently";
        return new Detail(identity + outcome + recorded, next, action);
    }

    private static Detail error(JsonNode root, int exit) throws IOException {
        JsonNode err = member(root, "error");
        String kind = requiredCode(err, "kind");
        requiredText(err, "message", false);
        JsonNode codeNode = err.get("code");
        String code = codeNode == null ? kind : requiredCode(err, "code");
        JsonNode hint = err.get("hint");
        if (hint != null && !hint.isTextual()) throw invalid();
        String next = switch (code) {
            case "RUN_AMBIGUOUS", "RUN_ALREADY_EXISTS", "RELEASE_CONFLICT",
                 "AWAITING_EXTERNAL", "INTERRUPTED" -> "status";
            case "STATE_CORRUPT", "UNSAFE_RETRY" -> null;
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
                "Restore the exact originally planned artifact bytes; do not create replacement artifacts or plans";
            case "MISSING_CONFIG", "INVALID_CONFIG" ->
                "Correct the reactor-root configuration or set distroplane.config";
            case "PROVIDER_NOT_FOUND", "VERSION_MISMATCH", "ARCH_MISMATCH" ->
                "Restore the expected provider executable and verify its original identity";
            case "RUN_AMBIGUOUS" ->
                "Select the exact recorded release/run using distroplane.release or distroplane.run";
            case "UNSAFE_RETRY" -> "Preserve original files and seek manual recovery; do not resume an unsupported retry grant or republish";
            case "AWAITING_EXTERNAL", "INTERRUPTED" ->
                "Inspect and reconcile the original run before considering further publication";
            case "RUN_ALREADY_EXISTS", "RELEASE_CONFLICT", "STATE_CORRUPT" ->
                "Preserve the original plan, run catalog, journal, exact artifacts and retained provider executables; inspect existing state";
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
