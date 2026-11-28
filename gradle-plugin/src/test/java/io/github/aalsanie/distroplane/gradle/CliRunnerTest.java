package io.github.aalsanie.distroplane.gradle;

import org.gradle.api.logging.Logging;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;

final class CliRunnerTest {
    @TempDir Path root;
    private Path fixture;
    private Path workspace;

    @BeforeEach void setup() throws Exception {
        fixture = NativeFixture.binary();
        workspace = Files.createDirectory(root.resolve("workspace with spaces")).toRealPath();
    }

    private CliRunner.Result call(String mode, Map<String,String> environment) throws Exception {
        return CliRunner.run(fixture, workspace, List.of("fixture-process", mode), environment,
            Duration.ofSeconds(10), Logging.getLogger(CliRunnerTest.class));
    }

    @Test void forwardsOnlyApprovedCredentialAndBlocksLeakedOutput() throws Exception {
        var environment = Map.of("ALLOWED_SECRET", "secret-canary");
        var result = call("environment", environment);
        assertEquals(0, result.exitCode());
        assertEquals("passed", result.stdout().strip());
        for (String mode : List.of("stdout-secret", "stderr-secret")) {
            IOException error = assertThrows(IOException.class, () -> call(mode, environment));
            assertTrue(error.getMessage().contains("suppressed"));
            assertFalse(error.getMessage().contains("secret-canary"));
        }
    }

    @Test void preservesSeparatedBoundedChannelsWithoutLoggingProviderText() throws Exception {
        var result = call("channels", Map.of());
        assertEquals(3, result.exitCode());
        assertEquals("{\"outputSchemaVersion\":\"1\"}", result.stdout().strip());
        assertEquals("UNTRUSTED-PROVIDER-CONTENT", result.stderr().strip());
        assertEquals(4 << 20, call("stdout-limit", Map.of()).stdout().length());
        assertEquals(1 << 20, call("stderr-limit", Map.of()).stderr().length());
        for (String mode : List.of("stdout-overflow", "stderr-overflow")) {
            IOException error = assertThrows(IOException.class, () -> call(mode, Map.of()));
            assertTrue(error.getMessage().contains("Could not read bounded CLI output"), error.getMessage());
        }
    }

    @Test void refusesInvalidOrMissingCredentialsAndExecutable() throws Exception {
        assertThrows(IOException.class, () -> call("ok", Map.of("BAD-VAR", "value")));
        assertThrows(IOException.class, () -> call("ok", Map.of("GOOD_VAR", "")));
        assertThrows(IOException.class, () -> CliRunner.run(root.resolve("nonexistent"), workspace,
            List.of("fixture-process", "ok"), Map.of(), Duration.ofSeconds(2), Logging.getLogger(CliRunnerTest.class)));
    }

    @Test void reportsPendingWithoutReissuingCommand() throws Exception {
        assertEquals(4, call("pending", Map.of()).exitCode());
        assertEquals(List.of("pending"), Files.readAllLines(workspace.resolve("fixture-invocations")));
    }

    private List<ProcessHandle> awaitProcesses() throws Exception {
        Path file = workspace.resolve("fixture-pids");
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(8);
        while (System.nanoTime() < deadline) {
            if (Files.isRegularFile(file)) {
                String[] values = Files.readString(file).strip().split(" ");
                if (values.length == 2 && !values[1].isBlank()) {
                    var parent = ProcessHandle.of(Long.parseLong(values[0]));
                    var child = ProcessHandle.of(Long.parseLong(values[1]));
                    if (parent.isPresent() && child.isPresent() && parent.get().isAlive() && child.get().isAlive() &&
                        parent.get().parent().map(ProcessHandle::pid).orElse(-1L) == ProcessHandle.current().pid() &&
                        child.get().parent().map(ProcessHandle::pid).orElse(-1L) == parent.get().pid())
                        return List.of(parent.get(), child.get());
                }
            }
            Thread.sleep(20);
        }
        fail("Native parent and descendant never started");
        return List.of();
    }

    private void assertStopped(List<ProcessHandle> processes) throws Exception {
        for (ProcessHandle process : processes) {
            process.onExit().get(5, TimeUnit.SECONDS);
            assertFalse(process.isAlive(), "Native process survived cancellation: " + process.pid());
        }
    }

    @Test void timeoutTerminatesNativeParentAndDescendant() throws Exception {
        ExecutorService executor = Executors.newSingleThreadExecutor();
        List<ProcessHandle> processes = List.of();
        try {
            Future<IOException> timed = executor.submit(() -> assertThrows(IOException.class,
                () -> CliRunner.run(fixture, workspace, List.of("fixture-process", "descendant"),
                    Map.of(), Duration.ofSeconds(10), Logging.getLogger(CliRunnerTest.class))));
            processes = awaitProcesses();
            assertTrue(timed.get(20, TimeUnit.SECONDS).getMessage().contains("timed out"));
            assertStopped(processes);
        } finally {
            processes.forEach(ProcessHandle::destroyForcibly);
            executor.shutdownNow();
        }
    }

    @Test void interruptionTerminatesNativeParentAndDescendant() throws Exception {
        AtomicReference<Throwable> result = new AtomicReference<>();
        Thread worker = new Thread(() -> {
            try {
                CliRunner.run(fixture, workspace, List.of("fixture-process", "descendant"), Map.of(),
                    Duration.ofSeconds(45), Logging.getLogger(CliRunnerTest.class));
                result.set(new AssertionError("Interrupted invocation returned normally"));
            } catch (Throwable error) {
                result.set(error);
            }
        });
        worker.setDaemon(true);
        List<ProcessHandle> processes = List.of();
        worker.start();
        try {
            processes = awaitProcesses();
            worker.interrupt();
            worker.join(15000);
            assertFalse(worker.isAlive(), "Interrupted runner did not return");
            assertInstanceOf(InterruptedException.class, result.get());
            assertStopped(processes);
        } finally {
            worker.interrupt();
            processes.forEach(ProcessHandle::destroyForcibly);
        }
    }
}
