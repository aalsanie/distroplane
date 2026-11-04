package io.github.aalsanie.distroplane.maven;

import org.apache.maven.plugin.logging.SystemStreamLog;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

final class CliRunnerTest {
    @TempDir Path root;
    @Test void forwardsOnlyApprovedCredentialAndBlocksLeakedOutput() throws Exception {
        if (System.getProperty("os.name").toLowerCase().contains("win")) return;
        Path script = root.resolve("cli");
        Files.writeString(script, "#!/bin/sh\n[ \"$MY_NPM_SECRET\" = 'canary-secret-value' ] || exit 14\n[ -z \"${GITHUB_TOKEN:-}\" ] || exit 15\nprintf 'ok\\n'\n");
        script.toFile().setExecutable(true);
        var result = CliRunner.run(script, root, List.of("version", "--json"), Map.of("MY_NPM_SECRET", "canary-secret-value"), Duration.ofSeconds(3), new SystemStreamLog());
        assertEquals(0, result.exitCode());
        Files.writeString(script, "#!/bin/sh\necho \"$MY_NPM_SECRET\"\n");
        assertThrows(IOException.class, () -> CliRunner.run(script, root, List.of("version"), Map.of("MY_NPM_SECRET", "canary-secret-value"), Duration.ofSeconds(3), new SystemStreamLog()));
    }
    @Test void preservesSeparatedBoundedChannelsWithoutLoggingProviderText() throws Exception {
        if (System.getProperty("os.name").toLowerCase().contains("win")) return;
        Path script = root.resolve("cli");
        Files.writeString(script, "#!/bin/sh\nprintf '{\"outputSchemaVersion\":\"1\"}\\n'\nprintf 'UNTRUSTED-PROVIDER-CONTENT\\n' >&2\nexit 3\n");
        script.toFile().setExecutable(true);
        CliRunner.Result result = CliRunner.run(script, root, List.of("status", "--json"),
            Map.of(), Duration.ofSeconds(3), new SystemStreamLog());
        assertEquals(3, result.exitCode());
        assertTrue(result.stdout().contains("outputSchemaVersion"));
        assertTrue(result.stderr().contains("UNTRUSTED-PROVIDER-CONTENT"));
    }

    @Test void suppressesSecretsInStderrAndRejectsExcessiveOutput() throws Exception {
        if (System.getProperty("os.name").toLowerCase().contains("win")) return;
        Path script = root.resolve("cli");
        Files.writeString(script, "#!/bin/sh\necho \"$MY_SECRET\" >&2\n");
        script.toFile().setExecutable(true);
        assertThrows(IOException.class, () -> CliRunner.run(script, root, List.of("status"),
            Map.of("MY_SECRET", "canary-secret"), Duration.ofSeconds(3), new SystemStreamLog()));
        Files.writeString(script, "#!/bin/sh\nhead -c 1100000 /dev/zero\n");
        assertThrows(IOException.class, () -> CliRunner.run(script, root, List.of("status"),
            Map.of(), Duration.ofSeconds(3), new SystemStreamLog()));
    }

    @Test void reportsPendingWithoutReissuingCommand() throws Exception {
        if (System.getProperty("os.name").toLowerCase().contains("win")) return;
        Path script = root.resolve("cli"); Files.writeString(script, "#!/bin/sh\nexit 4\n"); script.toFile().setExecutable(true);
        assertEquals(4, CliRunner.run(script, root, List.of("publish"), Map.of(), Duration.ofSeconds(3), new SystemStreamLog()).exitCode());
    }
}
