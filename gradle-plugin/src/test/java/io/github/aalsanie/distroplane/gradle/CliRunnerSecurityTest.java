package io.github.aalsanie.distroplane.gradle;

import org.gradle.api.logging.Logging;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

final class CliRunnerSecurityTest {
    @TempDir Path temp;

    private Path script(String body) throws IOException {
        Path file = temp.resolve("cli");
        Files.writeString(file, "#!/bin/sh\n" + body + "\n");
        assertTrue(file.toFile().setExecutable(true));
        return file;
    }

    private CliRunner.Result call(Path exe, Map<String,String> environment) throws Exception {
        return CliRunner.run(exe, temp, List.of("validate", "--json"), environment, Duration.ofSeconds(5),
            Logging.getLogger(CliRunnerSecurityTest.class));
    }

    @Test void limitsCredentialsToExplicitAllowlist() throws Exception {
        if (isWindows()) return;
        Path cli = script("[ -z \"${GITHUB_TOKEN:-}\" ] || exit 13\n[ \"$ALLOWED_SECRET\" = 'secret-canary' ] || exit 14\nprintf 'passed\\n'");
        assertEquals(0, call(cli, Map.of("ALLOWED_SECRET", "secret-canary")).exitCode());
        assertTrue(call(cli, Map.of("ALLOWED_SECRET", "secret-canary")).stdout().contains("passed"));
    }

    @Test void preventsCredentialsInStderrAndStdout() throws Exception {
        if (isWindows()) return;
        Path stderrCli = script("echo \"$ALLOWED_SECRET\" >&2");
        IOException error = assertThrows(IOException.class,
            () -> call(stderrCli, Map.of("ALLOWED_SECRET", "secret-canary")));
        assertTrue(error.getMessage().contains("suppressed"));
        Path stdoutCli = script("echo \"$ALLOWED_SECRET\"");
        assertThrows(IOException.class, () -> call(stdoutCli, Map.of("ALLOWED_SECRET", "secret-canary")));
    }

    @Test void refusesInvalidOrMissingCredentials() throws Exception {
        if (isWindows()) return;
        Path cli = script("exit 0");
        assertThrows(IOException.class, () -> call(cli, Map.of("BAD-VAR", "value")));
        assertThrows(IOException.class, () -> call(cli, Map.of("GOOD_VAR", "")));
        assertThrows(IOException.class, () -> CliRunner.run(temp.resolve("nonexistent"), temp,
            List.of("validate"), Map.of(), Duration.ofSeconds(2), Logging.getLogger(getClass())));
    }

    @Test void handlesNonzeroPendingAndExplicitTimeoutWithoutRetry() throws Exception {
        if (isWindows()) return;
        Path cli = script("exit 4");
        assertEquals(4, call(cli, Map.of()).exitCode());
        cli = script("sleep 3");
        Path finalCli = cli;
        IOException timeout = assertThrows(IOException.class, () -> CliRunner.run(finalCli, temp,
            List.of("publish"), Map.of(), Duration.ofMillis(80), Logging.getLogger(getClass())));
        assertTrue(timeout.getMessage().contains("timed out"));
    }

    @Test void rejectsExcessiveStdoutAndStderrInsteadOfStreamingUnboundedData() throws Exception {
        if (isWindows()) return;
        Path cli = script("head -c 5000000 /dev/zero");
        Path oversizedOut = cli;
        assertThrows(IOException.class, () -> call(oversizedOut, Map.of()));
        cli = script("head -c 1500000 /dev/zero >&2");
        Path oversizedErr = cli;
        assertThrows(IOException.class, () -> call(oversizedErr, Map.of()));
    }

    private static boolean isWindows() {
        return System.getProperty("os.name").toLowerCase().contains("win");
    }
}
