package io.github.aalsanie.distroplane.gradle;

import java.nio.file.Files;
import java.nio.file.Path;

import static org.junit.jupiter.api.Assertions.*;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

final class NativeFixture {
    private NativeFixture() { }

    static Path binary() throws Exception {
        String configured = System.getenv("DISTROPLANE_FIXTURE_BINARY");
        if ("true".equals(System.getenv("GITHUB_ACTIONS"))) {
            assertTrue(configured != null && !configured.isBlank(), "Native fixture must be built before adapter tests");
            assertEquals("unapproved-fixture-value", System.getenv("DISTROPLANE_UNAPPROVED_CANARY"));
        }
        assumeTrue(configured != null && !configured.isBlank(), "Build maven-plugin/it/fixture-cli/main.go and set DISTROPLANE_FIXTURE_BINARY");
        Path binary = Path.of(configured).toRealPath();
        assertTrue(Files.isRegularFile(binary), "Expected a native executable fixture");
        return binary;
    }
}
