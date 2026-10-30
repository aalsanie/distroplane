package io.github.aalsanie.distroplane.maven;

import org.apache.maven.plugin.logging.Log;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.*;

/** No shell, no secrets on argv, no retries of unknown publication outcomes. */
final class CliRunner {
    record Result(int exitCode, String stdout) { }
    static Result run(Path exe, Path workspace, List<String> args, Map<String,String> approvedEnvironment,
                      Duration timeout, Log log) throws IOException, InterruptedException {
        List<String> command = new ArrayList<>(); command.add(exe.toString()); command.addAll(args);
        ProcessBuilder builder = new ProcessBuilder(command).directory(workspace.toFile());
        Map<String,String> environment = builder.environment();
        Map<String,String> inherited = new HashMap<>(environment);
        environment.clear();
        for (String name : List.of("PATH", "SystemRoot", "WINDIR", "HOME", "USERPROFILE", "TMP", "TEMP", "TMPDIR", "LANG", "JAVA_HOME")) {
            if (inherited.containsKey(name)) environment.put(name, inherited.get(name));
        }
        for (Map.Entry<String,String> entry : approvedEnvironment.entrySet()) {
            if (!entry.getKey().matches("[A-Za-z_][A-Za-z0-9_]*") || entry.getValue() == null || entry.getValue().isEmpty())
                throw new IOException("Configured credential environment source is unavailable or invalid");
            environment.put(entry.getKey(), entry.getValue());
        }
        Process process = builder.start();
        ExecutorService threads = Executors.newFixedThreadPool(2, r -> {
            Thread t = new Thread(r, "distroplane-maven-io"); t.setDaemon(true); return t;
        });
        Future<String> out = threads.submit(() -> readBounded(process.getInputStream(), 4 << 20));
        Future<String> err = threads.submit(() -> readBounded(process.getErrorStream(), 1 << 20));
        try {
            boolean done = process.waitFor(timeout.toMillis(), TimeUnit.MILLISECONDS);
            if (!done) throw new IOException("Distroplane CLI timed out; inspect the original journal before resuming");
            String stdout = get(out), stderr = get(err);
            // A compromised command cannot leak a passed secret through Maven output.
            for (String secret : approvedEnvironment.values()) {
                if (stdout.contains(secret) || stderr.contains(secret)) throw new IOException("CLI emitted credential contents; output suppressed");
            }
            if (!stderr.isBlank()) log.warn(stderr.strip());
            if (process.exitValue() == 4) log.warn("Distroplane is pending external completion; retain the original run state");
            if (!stdout.isBlank()) log.info(stdout.strip());
            return new Result(process.exitValue(), stdout);
        } catch (ExecutionException e) {
            throw new IOException("Could not read bounded CLI output", e.getCause());
        } finally {
            if (process.isAlive()) {
                process.descendants().forEach(child -> child.destroyForcibly());
                process.destroyForcibly();
                process.waitFor(5, TimeUnit.SECONDS);
            }
            threads.shutdownNow();
        }
    }
    private static String get(Future<String> f) throws ExecutionException, InterruptedException, IOException {
        try { return f.get(5, TimeUnit.SECONDS); }
        catch (TimeoutException e) { throw new IOException("CLI output stream did not close", e); }
    }
    private static String readBounded(InputStream stream, int limit) throws IOException {
        try (stream) {
            byte[] bytes = stream.readNBytes(limit + 1);
            if (bytes.length > limit) throw new IOException("CLI produced excessive output");
            return new String(bytes, StandardCharsets.UTF_8);
        }
    }
}
