package io.github.aalsanie.distroplane.gradle;

import org.gradle.api.DefaultTask;
import org.gradle.api.GradleException;
import org.gradle.api.Project;
import org.gradle.api.Task;
import org.gradle.api.tasks.Internal;
import org.gradle.api.tasks.TaskAction;
import org.gradle.api.tasks.bundling.AbstractArchiveTask;
import org.gradle.work.DisableCachingByDefault;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;

/** Executes one CLI operation and propagates its exit status. */
@DisableCachingByDefault(because = "Release operations have external and durable side effects")
public abstract class DistroplaneTask extends DefaultTask {
    private String operation;
    private DistroplaneExtension options;

    @Internal public String getOperation() { return operation; }
    public void setOperation(String operation) { this.operation = operation; }
    @Internal public DistroplaneExtension getOptions() { return options; }
    public void setOptions(DistroplaneExtension options) { this.options = options; }

    @TaskAction public void invoke() {
        try {
            int timeout = options.getTimeoutSeconds().get();
            if (timeout < 1 || timeout > 86400) throw new IOException("Invalid Distroplane timeoutSeconds");
            Path root = getProject().getRootDir().toPath().toRealPath();
            List<String> command = arguments(root);
            Map<String, String> sources = credentialSources(command);
            command.add("--json");
            Path cache = getProject().getGradle().getGradleUserHomeDir().toPath().resolve("distroplane/cli");
            String archivePin = options.getArchiveSha256().getOrNull();
            Path cli = new CliBootstrap().resolve(cache, options.getCliVersion().get(),
                CliBootstrap.Platform.current(System.getProperty("os.name"), System.getProperty("os.arch")),
                getProject().getGradle().getStartParameter().isOffline(), archivePin);
            CliRunner.Result result = CliRunner.run(cli, root, command, sources, Duration.ofSeconds(timeout), getLogger());
            if (result.exitCode() != 0) throw new GradleException(
                "Distroplane " + operation + " exited with code " + result.exitCode() + "; inspect the existing release run");
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new GradleException("Distroplane invocation interrupted; recover the existing run", e);
        } catch (IOException e) {
            throw new GradleException("Distroplane invocation failed safely: " + e.getMessage(), e);
        }
    }

    private List<String> arguments(Path root) throws IOException {
        List<String> args = new ArrayList<>(List.of(operation));
        if ("init".equals(operation)) {
            String destination = options.getInitDestination().getOrNull();
            String release = options.getInitRelease().getOrNull();
            if (destination == null || destination.isBlank() || release == null || release.isBlank())
                throw new IOException("init requires explicit destination and release ID");
            args.addAll(List.of("--destination", destination, "--release", release));
            String artifact = options.getInitArtifact().getOrNull();
            if (artifact != null && !artifact.isBlank()) args.addAll(List.of("--artifact", artifact));
            args.addAll(List.of("--output", inside(root, options.getInitOutput().get()).toString(), "--non-interactive"));
            for (var field : options.getInitFields().get().entrySet()) {
                if (field.getKey() == null || !field.getKey().matches("[A-Za-z0-9._-]+") ||
                    field.getValue() == null || field.getValue().isBlank())
                    throw new IOException("Invalid Distroplane init field");
                args.addAll(List.of("--set", field.getKey() + "=" + field.getValue()));
            }
            return args;
        }
        if ("publish".equals(operation)) {
            boolean approved = options.getApprove().get();
            String confirmed = options.getConfirmPlan().getOrNull();
            if (approved == (confirmed != null && !confirmed.isBlank()))
                throw new IOException("Select exactly one of approve=true or confirmPlan with the exact plan ID");
            if (approved) args.add("--yes");
            else args.addAll(List.of("--confirm-plan", confirmed));
        }
        if ("status".equals(operation) || "resume".equals(operation)) {
            String release = options.getRelease().getOrNull();
            String run = options.getRun().getOrNull();
            if (release != null && !release.isBlank()) args.addAll(List.of("--release", release));
            if (run != null && !run.isBlank()) args.addAll(List.of("--run", run));
            if ("resume".equals(operation) && options.getReconcileOnly().get()) args.add("--reconcile-only");
        }
        args.addAll(List.of("--config", materializeConfig(root).toString()));
        return args;
    }

    private Path materializeConfig(Path root) throws IOException {
        Path config = inside(root, options.getConfig().get());
        Map<String, String> mappings = options.getArtifactTasks().get();
        if (mappings.isEmpty()) return config;
        List<ArtifactMapper.Selection> selections = new ArrayList<>();
        List<ArtifactMapper.Source> sources = new ArrayList<>();
        for (var mapping : mappings.entrySet()) {
            String name = mapping.getKey(), taskPath = mapping.getValue();
            if (taskPath == null || !taskPath.matches(":(?:[A-Za-z0-9._-]+:)*[A-Za-z0-9._-]+"))
                throw new IOException("Invalid Gradle archive task path for Distroplane artifact: " + name);
            int last = taskPath.lastIndexOf(':');
            String projectPath = last == 0 ? ":" : taskPath.substring(0, last);
            Project module = getProject().getRootProject().findProject(projectPath);
            Task task = module == null ? null : module.getTasks().findByName(taskPath.substring(last + 1));
            if (!(task instanceof AbstractArchiveTask archive))
                throw new IOException("Distroplane mapping requires an existing Gradle archive task: " + taskPath);
            Path file = archive.getArchiveFile().get().getAsFile().toPath();
            selections.add(new ArtifactMapper.Selection(name, taskPath, "", ""));
            sources.add(new ArtifactMapper.Source(taskPath, "", "", file));
        }
        return ArtifactMapper.materialize(config, root, selections, sources);
    }

    private Map<String, String> credentialSources(List<String> args) throws IOException {
        if (!"publish".equals(operation) && !"resume".equals(operation)) return Map.of();
        Map<String, String> approved = new HashMap<>();
        Set<String> unique = new HashSet<>();
        for (var mapping : options.getCredentialMappings().get().entrySet()) {
            String key = mapping.getKey(), variable = mapping.getValue();
            if (key == null || !key.matches("[A-Za-z0-9][A-Za-z0-9._-]*") ||
                variable == null || !variable.matches("[A-Za-z_][A-Za-z0-9_]*") ||
                !unique.add(variable.toUpperCase(Locale.ROOT)))
                throw new IOException("Invalid or conflicting credential mapping");
            String value = System.getenv(variable);
            if (value == null || value.isEmpty())
                throw new IOException("Required credential environment variable is unavailable: " + variable);
            approved.put(variable, value);
            args.addAll(List.of("--credential", key + "=" + variable));
        }
        return approved;
    }

    private static Path inside(Path root, String selected) throws IOException {
        if (selected == null || selected.isBlank()) throw new IOException("Distroplane path must not be blank");
        Path target = root.resolve(selected).normalize();
        if (!target.startsWith(root)) throw new IOException("Distroplane path must be inside Gradle root");
        Path cursor = root;
        for (Path segment : root.relativize(target)) {
            cursor = cursor.resolve(segment);
            if (Files.isSymbolicLink(cursor)) throw new IOException("Distroplane path must not traverse a symbolic link");
        }
        if (Files.isSymbolicLink(target) || (Files.exists(target, LinkOption.NOFOLLOW_LINKS) && !Files.isRegularFile(target, LinkOption.NOFOLLOW_LINKS)))
            throw new IOException("Distroplane path must be a regular file, not a link or directory");
        return target;
    }
}
