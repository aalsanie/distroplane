package io.github.aalsanie.distroplane.gradle;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.util.List;

import static org.junit.jupiter.api.Assertions.*;

final class CliFeedbackTest {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static ObjectNode root() {
        ObjectNode value = JSON.createObjectNode();
        value.put("outputSchemaVersion", "1");
        return value;
    }
    private static ObjectNode plan() {
        ObjectNode value = root();
        value.put("planId", "plan-valid");
        value.put("path", "/repo/plan.json");
        value.put("targets", 1);
        value.put("operations", 1);
        return value;
    }
    private static ObjectNode validate() {
        ObjectNode value = root();
        value.put("valid", true);
        value.put("schemaVersion", "2");
        value.put("targets", 1);
        return value;
    }
    private static ObjectNode init() {
        ObjectNode value = root();
        value.put("configPath", "/project/distroplane.json");
        value.set("artifact", JSON.createObjectNode());
        value.set("artifacts", JSON.createObjectNode());
        value.putArray("destinations").add("npm");
        return value;
    }
    private static ObjectNode doctor(boolean healthy, String code, String severity) {
        ObjectNode value = root();
        value.put("healthy", healthy);
        ObjectNode check = value.putArray("checks").addObject();
        check.put("code", code);
        check.put("severity", severity);
        check.put("subject", "fixture");
        check.put("message", "untrusted detail never printed");
        return value;
    }
    private static ObjectNode state(String state, boolean completed, boolean reconcile, boolean ambiguous) {
        ObjectNode value = root();
        value.put("planId", "plan-valid");
        value.put("runId", "run-valid");
        value.put("completed", completed);
        value.put("cancelled", false);
        ObjectNode target = value.putArray("targets").addObject();
        target.put("id", "target-valid");
        target.put("state", state);
        target.put("reconcileRequired", reconcile);
        target.put("ambiguous", ambiguous);
        ObjectNode operation = value.putArray("operations").addObject();
        operation.put("id", "operation-valid");
        operation.put("targetId", "target-valid");
        operation.put("state", state);
        operation.put("reconcileRequired", reconcile);
        operation.put("ambiguous", ambiguous);
        return value;
    }
    private static ObjectNode publish(ObjectNode state) {
        ObjectNode value = root();
        value.set("plan", plan());
        value.put("approved", true);
        value.put("journalPath", "/project/original-run.journal");
        value.set("run", state);
        return value;
    }
    private static ObjectNode error(String code) {
        ObjectNode value = root();
        ObjectNode issue = value.putObject("error");
        issue.put("kind", "fixture_error");
        issue.put("code", code);
        issue.put("message", "untrusted message content");
        issue.put("hint", "untrusted suggestion");
        return value;
    }
    private static CliFeedback.Detail inspect(String goal, int exit, JsonNode output) throws IOException {
        return CliFeedback.interpret(goal, new CliRunner.Result(exit, output.toString(), ""));
    }
    private static CliFeedback.Detail errorResponse(int exit, JsonNode output) throws IOException {
        return CliFeedback.interpret("publish", new CliRunner.Result(exit, "", output.toString()));
    }
    private static void bad(String goal, int exit, JsonNode output) {
        assertThrows(IOException.class, () -> inspect(goal, exit, output));
    }
    private static void badError(int exit, JsonNode output) {
        assertThrows(IOException.class, () -> errorResponse(exit, output));
    }

    @Test void validSuccessEnvelopesAndAdditiveFields() throws Exception {
        assertTrue(inspect("validate", 0, validate()).summary().contains("1 destination"));
        assertEquals("doctor", inspect("init", 0, init()).nextGoal());
        assertTrue(inspect("plan", 0, plan()).summary().contains("plan-valid"));
        assertEquals("Review the exact plan before explicit publication approval",
            inspect("plan", 0, plan()).action());
        ObjectNode withUnknown = plan();
        withUnknown.put("futureAdditiveField", "untrusted and not printed");
        assertFalse(inspect("plan", 0, withUnknown).summary().contains("untrusted"));
        assertNull(inspect("doctor", 0, doctor(true, "CONFIG_VALID", "PASS")).nextGoal());
        assertTrue(inspect("doctor", 0, doctor(true, "REMOTE_REACHABLE", "PASS"))
            .summary().contains("remote TCP connectivity checked"));
        assertTrue(inspect("doctor", 0, doctor(true, "REMOTE_SKIPPED", "SKIP"))
            .summary().contains("remote connectivity not verified"));
        ObjectNode preview = root();
        preview.set("plan", plan());
        preview.put("approved", false);
        assertTrue(inspect("publish", 0, preview).summary().contains("nothing published"));
        assertTrue(inspect("publish", 0, publish(state("PUBLISHED", true, false, false)))
            .summary().contains("public availability unverified"));
        assertTrue(inspect("status", 0, state("PUBLISHED", true, false, false)).summary().contains("recorded as published"));
        assertTrue(inspect("resume", 0, state("PUBLISHED", true, false, false)).action().contains("independently"));
    }

    @Test void doctorFailureCodesAndHealthySemantics() throws Exception {
        for (String code : List.of("MISSING_CREDENTIAL", "MISSING_ARTIFACT", "MISSING_CONFIG",
                "PROVIDER_NOT_FOUND", "RUN_AMBIGUOUS", "REMOTE_UNREACHABLE", "RANDOM_FAILURE")) {
            CliFeedback.Detail detail = inspect("doctor", 3, doctor(false, code, "FAIL"));
            assertTrue(detail.summary().contains(code));
            assertEquals("doctor", detail.nextGoal());
            assertFalse(detail.action().isBlank());
            assertFalse(detail.summary().contains("untrusted"));
        }
        assertTrue(inspect("doctor", 1, doctor(false, "REMOTE_UNREACHABLE", "FAIL"))
            .action().contains("remote endpoint"));
        ObjectNode passWarn = doctor(true, "CONFIG_VALID", "PASS");
        passWarn.withArray("checks").addObject().put("code", "SCHEMA_V1").put("severity", "WARN")
            .put("subject", "config").put("message", "supported");
        passWarn.withArray("checks").addObject().put("code", "REMOTE_SKIPPED").put("severity", "SKIP")
            .put("subject", "network").put("message", "offline");
        assertNotNull(inspect("doctor", 0, passWarn));
        bad("doctor", 0, doctor(false, "MISSING_CONFIG", "FAIL"));
        bad("doctor", 3, doctor(true, "CONFIG_VALID", "PASS"));
        bad("doctor", 2, doctor(false, "MISSING_CONFIG", "FAIL"));
        bad("doctor", 1, doctor(true, "CONFIG_VALID", "FAIL"));
        bad("doctor", 0, doctor(true, "MISSING_CONFIG", "FAIL"));
        bad("doctor", 3, doctor(false, "CONFIG_VALID", "PASS"));
        ObjectNode excessive = doctor(true, "CONFIG_VALID", "PASS");
        for (int i = 0; i < 1024; i++) excessive.withArray("checks").addObject();
        bad("doctor", 0, excessive);
        ObjectNode noChecks = doctor(true, "CONFIG_VALID", "PASS");
        noChecks.putArray("checks");
        bad("doctor", 0, noChecks);
        ObjectNode wrongCode = doctor(false, "UNSAFE\nVALUE", "FAIL");
        bad("doctor", 3, wrongCode);
        ObjectNode wrongSeverity = doctor(false, "CONFIG_VALID", "WAT");
        bad("doctor", 3, wrongSeverity);
        ObjectNode wrongSubject = doctor(false, "CONFIG_VALID", "FAIL");
        ((ObjectNode)wrongSubject.withArray("checks").get(0)).put("subject", 2);
        bad("doctor", 3, wrongSubject);
        ObjectNode wrongMessage = doctor(false, "CONFIG_VALID", "FAIL");
        ((ObjectNode)wrongMessage.withArray("checks").get(0)).remove("message");
        bad("doctor", 3, wrongMessage);
        ObjectNode wrongCheck = doctor(true, "CONFIG_VALID", "PASS");
        wrongCheck.withArray("checks").removeAll().add(5);
        bad("doctor", 0, wrongCheck);
        ObjectNode wrongHealthy = doctor(true, "CONFIG_VALID", "PASS");
        wrongHealthy.put("healthy", "true");
        bad("doctor", 0, wrongHealthy);
    }

    @Test void doctorWarnsAboutRecordedRunsWithoutRecommendingPublication() throws Exception {
        for (String code : List.of("RUN_RESERVED", "RECONCILE_REQUIRED", "PLAN_AMBIGUOUS")) {
            ObjectNode report = doctor(true, "CONFIG_VALID", "PASS");
            ObjectNode warning = report.withArray("checks").addObject();
            warning.put("code", code);
            warning.put("severity", "WARN");
            warning.put("subject", "run");
            warning.put("message", "read-only state");
            CliFeedback.Detail next = inspect("doctor", 0, report);
            assertEquals("status", next.nextGoal());
            assertTrue(next.summary().contains(code));
            assertTrue(next.action().contains("original recorded run"));
        }
    }

    @Test void statePendingAmbiguousBlockedRejectedAndMixedOutcomes() throws Exception {
        CliFeedback.Detail external = inspect("status", 4, state("WAITING_EXTERNAL", false, true, false));
        assertTrue(external.summary().contains("awaiting external"));
        assertEquals("status", external.nextGoal());
        assertTrue(external.action().contains("reconciling"));
        CliFeedback.Detail ambiguous = inspect("resume", 4, state("WAITING_EXTERNAL", false, true, true));
        assertTrue(ambiguous.summary().contains("ambiguous"));
        assertTrue(ambiguous.action().contains("before any retry"));
        CliFeedback.Detail blocked = inspect("status", 4, state("BLOCKED", false, false, false));
        assertTrue(blocked.summary().contains("blocked"));
        assertTrue(blocked.action().contains("same run"));
        for (String code : List.of("CREDENTIAL_UNAVAILABLE", "CREDENTIAL_RESOLUTION_FAILED")) {
            ObjectNode unavailable = state("BLOCKED", false, false, false);
            ((ObjectNode)unavailable.withArray("operations").get(0)).put("errorCode", code);
            CliFeedback.Detail check = inspect("status", 4, unavailable);
            assertTrue(check.summary().contains("unavailable credentials"));
            assertTrue(check.action().contains("approved credential environment source"));
        }
        ObjectNode malformed = state("BLOCKED", false, false, false);
        ((ObjectNode)malformed.withArray("operations").get(0)).put("errorCode", 3);
        bad("status", 4, malformed);
        assertTrue(inspect("status", 6, state("FAILED", true, false, false)).summary().contains("failed"));
        assertTrue(inspect("status", 5, state("REJECTED", true, false, false)).summary().contains("rejected"));
        ObjectNode cancelled = state("CANCELLED", false, false, false);
        cancelled.put("cancelled", true);
        assertTrue(inspect("status", 6, cancelled).summary().contains("failed"));
        for (String state : List.of("PLANNED", "READY", "RUNNING")) {
            assertEquals("status", inspect("status", 4, state(state, false, false, false)).nextGoal());
        }
        ObjectNode mixed = state("REJECTED", false, false, false);
        mixed.withArray("targets").addObject().put("id", "other").put("state", "WAITING_EXTERNAL")
            .put("reconcileRequired", true).put("ambiguous", false);
        mixed.withArray("operations").addObject().put("id", "other-op").put("targetId", "other")
            .put("state", "WAITING_EXTERNAL").put("reconcileRequired", true).put("ambiguous", false);
        assertTrue(inspect("status", 5, mixed).summary().contains("mixed"));
        mixed = state("FAILED", false, false, false);
        ((ObjectNode)mixed.withArray("operations").get(0)).put("ambiguous", true);
        assertTrue(inspect("status", 6, mixed).summary().contains("mixed"));
        ObjectNode targetOnlyUncertain = state("PUBLISHED", true, false, true);
        bad("status", 0, targetOnlyUncertain);
    }

    @Test void rejectsUntrustedOrInconsistentState() throws Exception {
        ObjectNode normal = state("PUBLISHED", true, false, false);
        for (int exit : List.of(1, 4, 5, 6)) bad("status", exit, normal);
        bad("status", 0, state("PUBLISHED", false, false, false));
        bad("status", 0, state("WAITING_EXTERNAL", true, true, false));
        bad("status", 4, state("FAILED", false, false, false));
        bad("status", 5, state("BLOCKED", false, false, false));
        ObjectNode invalid = normal.deepCopy();
        invalid.put("runId", "run\ninjected");
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        invalid.put("planId", 3);
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        invalid.remove("completed");
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        invalid.put("cancelled", "false");
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        invalid.putArray("targets");
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        invalid.putArray("operations");
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        invalid.withArray("targets").removeAll().add("not-an-object");
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        ((ObjectNode)invalid.withArray("targets").get(0)).put("id", "\u001b[31m");
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        ((ObjectNode)invalid.withArray("targets").get(0)).put("state", "UNKNOWN");
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        ((ObjectNode)invalid.withArray("targets").get(0)).put("reconcileRequired", "false");
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        ((ObjectNode)invalid.withArray("targets").get(0)).remove("ambiguous");
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        invalid.withArray("operations").removeAll().add(false);
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        ((ObjectNode)invalid.withArray("operations").get(0)).put("id", "bad id");
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        ((ObjectNode)invalid.withArray("operations").get(0)).remove("targetId");
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        ((ObjectNode)invalid.withArray("operations").get(0)).put("state", "UNKNOWN");
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        ((ObjectNode)invalid.withArray("operations").get(0)).put("reconcileRequired", 1);
        bad("status", 0, invalid);
        invalid = normal.deepCopy();
        ((ObjectNode)invalid.withArray("operations").get(0)).put("ambiguous", 1);
        bad("status", 0, invalid);
    }

    @Test void truncatedJournalsDoNotClaimFinalPublication() throws Exception {
        ObjectNode success = state("PUBLISHED", true, false, false);
        success.put("journalTruncatedTail", true);
        CliFeedback.Detail detail = inspect("status", 0, success);
        assertTrue(detail.summary().contains("truncated final record"));
        assertEquals("status", detail.nextGoal());
        assertTrue(detail.action().contains("durable prefix"));
        success.put("journalTruncatedTail", "true");
        bad("status", 0, success);
        success.remove("journalTruncatedTail");
        assertNull(inspect("status", 0, success).nextGoal());
        ObjectNode unknown = state("READY", false, false, false);
        assertEquals("status", inspect("status", 4, unknown).nextGoal());
    }

    @Test void validatesErrorOnStderrAndNeverPrintsMessages() throws Exception {
        for (String code : List.of("RUN_AMBIGUOUS", "RUN_ALREADY_EXISTS", "RELEASE_CONFLICT", "STATE_CORRUPT",
                "AWAITING_EXTERNAL", "UNSAFE_RETRY", "INTERRUPTED", "MISSING_CONFIG", "INVALID_CONFIG",
                "MISSING_ARTIFACT", "PROVIDER_NOT_FOUND", "VERSION_MISMATCH",
                "CREDENTIAL_UNAVAILABLE", "APPROVAL_REQUIRED", "REMOTE_UNREACHABLE", "UNRECOGNIZED_ERROR")) {
            CliFeedback.Detail detail = errorResponse(3, error(code));
            assertTrue(detail.summary().contains(code));
            assertFalse(detail.summary().contains("untrusted"));
            assertFalse(detail.action().contains("untrusted"));
        }
        ObjectNode noCode = error("FOO");
        ((ObjectNode)noCode.get("error")).remove("code");
        assertTrue(errorResponse(3, noCode).summary().contains("fixture_error"));
        ObjectNode malicious = error("UNSAFE\nCODE");
        badError(3, malicious);
        ObjectNode badKind = error("FOO");
        ((ObjectNode)badKind.get("error")).put("kind", 17);
        badError(3, badKind);
        ObjectNode noMessage = error("FOO");
        ((ObjectNode)noMessage.get("error")).remove("message");
        badError(3, noMessage);
        ObjectNode badHint = error("FOO");
        ((ObjectNode)badHint.get("error")).put("hint", 14);
        badError(3, badHint);
        ObjectNode badObject = root();
        badError(3, badObject);
        badError(0, error("FOO"));
        assertThrows(IOException.class, () -> CliFeedback.interpret("publish", new CliRunner.Result(3, "", "")));
    }

    @Test void rejectsInvalidDocumentShapesAndCommandEnvelopes() throws Exception {
        for (String content : List.of("{}", "{\"outputSchemaVersion\":2}", "{\"outputSchemaVersion\":\"2\"}",
                "[]", "null", "true", "garbage", "{\"outputSchemaVersion\":\"1\"} {}")) {
            assertThrows(IOException.class, () -> CliFeedback.interpret("plan", new CliRunner.Result(0, content, "")));
        }
        assertThrows(IOException.class, () -> CliFeedback.interpret("plan", new CliRunner.Result(0, " ".repeat(4 << 20) + "!", "")));
        assertThrows(IOException.class, () -> CliFeedback.interpret("plan", new CliRunner.Result(-1, plan().toString(), "")));
        assertThrows(IOException.class, () -> CliFeedback.interpret("plan", new CliRunner.Result(7, plan().toString(), "")));
        assertThrows(IOException.class, () -> CliFeedback.interpret("not-a-goal", new CliRunner.Result(0, plan().toString(), "")));
        bad("validate", 3, validate());
        ObjectNode invalid = validate();
        invalid.put("valid", false);
        bad("validate", 0, invalid);
        invalid = validate();
        invalid.put("targets", -1);
        bad("validate", 0, invalid);
        invalid = validate();
        invalid.put("targets", 2147483648L);
        bad("validate", 0, invalid);
        invalid = validate();
        invalid.put("schemaVersion", "");
        bad("validate", 0, invalid);
        bad("init", 4, init());
        invalid = init();
        invalid.remove("artifact");
        bad("init", 0, invalid);
        invalid = init();
        invalid.put("artifacts", "no");
        bad("init", 0, invalid);
        invalid = init();
        invalid.put("destinations", "npm");
        bad("init", 0, invalid);
        invalid = init();
        invalid.remove("configPath");
        bad("init", 0, invalid);
        bad("plan", 4, plan());
        invalid = plan();
        invalid.put("planId", "bad/id");
        bad("plan", 0, invalid);
        invalid = plan();
        invalid.put("path", 14);
        bad("plan", 0, invalid);
        invalid = plan();
        invalid.put("operations", "1");
        bad("plan", 0, invalid);
        invalid = plan();
        invalid.remove("targets");
        bad("plan", 0, invalid);
        invalid = validate();
        invalid.put("targets", 1.5);
        bad("validate", 0, invalid);
    }

    @Test void rejectsInconsistentPublicationEnvelopes() throws Exception {
        ObjectNode published = publish(state("PUBLISHED", true, false, false));
        bad("publish", 4, published);
        ObjectNode wrong = published.deepCopy();
        wrong.put("approved", false);
        bad("publish", 0, wrong);
        wrong = published.deepCopy();
        wrong.remove("journalPath");
        bad("publish", 0, wrong);
        wrong = published.deepCopy();
        wrong.set("plan", JSON.createArrayNode());
        bad("publish", 0, wrong);
        wrong = published.deepCopy();
        ((ObjectNode)wrong.get("plan")).put("outputSchemaVersion", "2");
        bad("publish", 0, wrong);
        wrong = published.deepCopy();
        ((ObjectNode)wrong.get("plan")).remove("planId");
        bad("publish", 0, wrong);
        wrong = published.deepCopy();
        ((ObjectNode)wrong.get("plan")).remove("path");
        bad("publish", 0, wrong);
        wrong = published.deepCopy();
        ((ObjectNode)wrong.get("plan")).put("targets", "one");
        bad("publish", 0, wrong);
        wrong = published.deepCopy();
        ((ObjectNode)wrong.get("plan")).put("operations", -1);
        bad("publish", 0, wrong);
        ObjectNode preview = root();
        preview.set("plan", plan());
        preview.put("approved", true);
        bad("publish", 0, preview);
        bad("publish", 4, preview);
        preview.put("approved", false);
        bad("publish", 4, preview);
        wrong = published.deepCopy();
        ((ObjectNode)wrong.get("run")).put("outputSchemaVersion", "2");
        bad("publish", 0, wrong);
        wrong = published.deepCopy();
        wrong.put("approved", "true");
        bad("publish", 0, wrong);
    }
}
