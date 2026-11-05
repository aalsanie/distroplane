package io.github.aalsanie.distroplane.maven;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import org.junit.jupiter.api.Test;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

/** One recorded-state contract, also exercised by the native CLI and Action. */
final class ReleaseAcceptanceTest {
    private static final ObjectMapper JSON = new ObjectMapper();

    private static JsonNode cases() throws Exception {
        Path fixture = Path.of("..", "tests", "consumer", "release-guidance-cases.json");
        if (!Files.isRegularFile(fixture))
            fixture = Path.of("tests", "consumer", "release-guidance-cases.json");
        assertTrue(Files.isRegularFile(fixture), "shared recorded release cases must be present");
        JsonNode root = JSON.readTree(fixture.toFile());
        assertEquals("1", root.path("schemaVersion").asText());
        JsonNode tests = root.path("cases");
        assertTrue(tests.isArray());
        assertEquals(12, tests.size());
        return tests;
    }

    @Test void recordedOutcomeMatrixKeepsOriginalRunAndNeverApprovesPublication() throws Exception {
        Map<String, String> categories = Map.of(
            "published", "recorded as published",
            "external", "awaiting external acceptance",
            "ambiguous", "ambiguous external outcome",
            "credential", "blocked",
            "blocked", "blocked",
            "rejected", "rejected",
            "failed", "failed or cancelled",
            "mixed", "mixed terminal and outstanding",
            "truncated", "truncated final record");
        for (JsonNode tc : cases()) {
            String name = tc.path("name").asText();
            String kind = tc.path("kind").asText();
            int exit = tc.path("exit").asInt(-1);
            assertNotNull(categories.get(kind), name);
            assertTrue(exit == 0 || exit == 4 || exit == 5 || exit == 6, name);
            ObjectNode state = ((ObjectNode)tc.path("state")).deepCopy();
            // Extra provider text must never be included in safe summaries or actions.
            state.put("providerMessage", "SECRET-CANARY\n## injected heading");
            CliFeedback.Detail result = CliFeedback.interpret("status",
                new CliRunner.Result(exit, state.toString(), ""));
            String summary = result.summary();
            String action = result.action();
            assertTrue(summary.contains(categories.get(kind)), name + ": " + summary);
            assertTrue(summary.contains("run-acceptance"), name);
            assertTrue(summary.contains("plan-acceptance"), name);
            assertFalse(summary.contains("SECRET-CANARY"), name);
            assertFalse(action.contains("SECRET-CANARY"), name);
            assertFalse(summary.contains("## injected"), name);
            assertFalse(action.contains("publish --yes"), name);
            assertFalse(action.contains("new plan"), name);
            assertFalse(action.contains("delete .distroplane"), name);
            if (exit == 0 && !kind.equals("truncated")) {
                assertNull(result.nextGoal(), name);
                assertTrue(action.contains("independently"), name);
            } else {
                assertEquals("status", result.nextGoal(), name);
            }
            if (!kind.equals("published")) {
                assertFalse(summary.contains("recorded as published"), name);
            }
            if (kind.equals("external") || kind.equals("ambiguous")) {
                assertTrue(action.contains("reconcil") || action.contains("Reconcile"), name);
            }
            if (kind.equals("credential") || kind.equals("blocked")) {
                assertTrue(action.contains("same run"), name);
            }
            if (kind.equals("mixed")) {
                assertTrue(action.contains("original") || action.contains("reconcil"), name);
            }
        }
    }

    @Test void diagnosticEnvelopeIsSeparateFromStateAndSuppressesProviderMessages() throws Exception {
        for (String code : new String[]{"MISSING_CONFIG", "MISSING_ARTIFACT", "MISSING_CREDENTIAL",
                "PROVIDER_NOT_FOUND", "APPROVAL_REQUIRED", "RUN_ALREADY_EXISTS", "RUN_AMBIGUOUS"}) {
            ObjectNode issue = JSON.createObjectNode();
            issue.put("outputSchemaVersion", "1");
            ObjectNode error = issue.putObject("error");
            error.put("kind", "test_failure");
            error.put("code", code);
            error.put("message", "SECRET-CANARY\n## unsafe output");
            error.put("hint", "publish --yes without approval");
            CliFeedback.Detail detail = CliFeedback.interpret("publish", new CliRunner.Result(3, "", issue.toString()));
            assertTrue(detail.summary().contains(code));
            assertFalse(detail.summary().contains("SECRET-CANARY"));
            assertFalse(detail.action().contains("SECRET-CANARY"));
            assertFalse(detail.action().contains("publish --yes"));
        }
    }
}
