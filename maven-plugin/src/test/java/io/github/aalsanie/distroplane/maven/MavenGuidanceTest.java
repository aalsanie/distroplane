package io.github.aalsanie.distroplane.maven;

import org.junit.jupiter.api.Test;
import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

final class MavenGuidanceTest {
    @Test void doctorForwardsUnresolvedCredentialReferencesWithoutSecretValues() throws Exception {
        List<String> command = new ArrayList<>(List.of("doctor"));
        Map<String,String> environment = AbstractDistroplaneMojo.credentialEnvironment(
            "doctor", Map.of("npm-publish", "DISTROPLANE_UNSET_TEST", "github", "GITHUB_TOKEN"),
            command, name -> name.equals("GITHUB_TOKEN") ? "canary-secret" : null);
        assertEquals(Map.of("GITHUB_TOKEN", "canary-secret"), environment);
        assertTrue(command.contains("npm-publish=DISTROPLANE_UNSET_TEST"));
        assertTrue(command.contains("github=GITHUB_TOKEN"));
        assertFalse(command.toString().contains("canary-secret"));
        assertEquals(2, command.stream().filter("--credential"::equals).count());
    }

    @Test void publishAndResumeRequirePresentExplicitCredentials() throws Exception {
        for (String goal : List.of("publish", "resume")) {
            List<String> command = new ArrayList<>(List.of(goal));
            assertThrows(IOException.class, () -> AbstractDistroplaneMojo.credentialEnvironment(
                goal, Map.of("publish-auth", "MISSING_ENV"), command, name -> null));
            assertThrows(IOException.class, () -> AbstractDistroplaneMojo.credentialEnvironment(
                goal, Map.of("publish-auth", "MISSING_ENV"), command, name -> ""));
            command.clear();
            command.add(goal);
            assertEquals(Map.of("MISSING_ENV", "private"),
                AbstractDistroplaneMojo.credentialEnvironment(goal,
                    Map.of("publish-auth", "MISSING_ENV"), command, name -> "private"));
            assertTrue(command.contains("publish-auth=MISSING_ENV"));
            assertFalse(command.toString().contains("private"));
        }
    }

    @Test void mappingRejectsInvalidAndCollidingNamesWithoutLeakingSource() throws Exception {
        for (Map<String,String> mappings : List.of(
            Map.of("a", "NOT-VALID"),
            Map.of("b\nnew", "TOKEN"),
            Map.of("a", "TOKEN", "b", "token"),
            Map.of("a", ""))
        ) {
            assertThrows(IOException.class, () -> AbstractDistroplaneMojo.credentialEnvironment(
                "doctor", mappings, new ArrayList<>(), name -> "secret"));
        }
        assertEquals(Map.of(), AbstractDistroplaneMojo.credentialEnvironment(
            "plan", Map.of("a", "TEST"), new ArrayList<>(), name -> "value"));
        assertEquals(Map.of(), AbstractDistroplaneMojo.credentialEnvironment(
            "doctor", null, new ArrayList<>(), name -> "value"));
    }

    @Test void preCliRemediationUsesFixedSafeText() {
        for (String message : List.of("Invalid or conflicting credential mapping",
                "Required credential environment source is unavailable", "artifact mapping is missing",
                "Mapped Maven artifact has not been packaged", "config must be inside Maven reactor root",
                "CLI timed out", "stream did not close", "Cached Distroplane CLI is corrupt",
                "SHA-256 checksum mismatch", "Maven offline mode requires verified cache",
                "Requested release not present", "Cannot start executable", "unknown")) {
            String next = AbstractDistroplaneMojo.preCliAction(new IOException(message + " SECRET-CANARY"));
            assertFalse(next.isBlank());
            assertFalse(next.contains("SECRET-CANARY"));
        }
        assertFalse(AbstractDistroplaneMojo.preCliAction(new IOException()).isBlank());
    }
}
