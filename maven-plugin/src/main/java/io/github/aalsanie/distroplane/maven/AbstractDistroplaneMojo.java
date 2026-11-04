package io.github.aalsanie.distroplane.maven;

import org.apache.maven.execution.MavenSession;
import org.apache.maven.plugin.AbstractMojo;
import org.apache.maven.plugin.MojoExecution;
import org.apache.maven.plugin.MojoExecutionException;
import org.apache.maven.plugin.MojoFailureException;
import org.apache.maven.plugins.annotations.Parameter;

import java.io.IOException;
import java.nio.file.Path;
import java.time.Duration;
import java.util.*;
import java.util.function.Function;

/** Thin wrapper: the native CLI owns planning, publication, journal and retry semantics. */
abstract class AbstractDistroplaneMojo extends AbstractMojo {
    @Parameter(defaultValue = "${session}", readonly = true, required = true)
    protected MavenSession session;
    @Parameter(property = "distroplane.config", defaultValue = "distroplane.json")
    protected String config;
    @Parameter(property = "distroplane.cliVersion", defaultValue = "0.9.3")
    protected String cliVersion;
    @Parameter(property = "distroplane.archiveSha256")
    protected String archiveSha256;
    @Parameter(property = "distroplane.timeoutSeconds", defaultValue = "900")
    protected int timeoutSeconds;
    @Parameter
    protected List<ArtifactMapping> artifactMappings;
    @Parameter
    protected Map<String,String> credentialMappings;

    protected final Path root() { return Path.of(session.getExecutionRootDirectory()).toAbsolutePath().normalize(); }
    protected Path configPath() throws IOException {
        Path requested = root().resolve(config).normalize();
        if (!requested.startsWith(root())) throw new IOException("Distroplane config must be inside the Maven reactor root");
        if (artifactMappings == null || artifactMappings.isEmpty()) return requested;
        List<ArtifactMapper.Selection> selections = new ArrayList<>();
        for (ArtifactMapping m : artifactMappings) selections.add(new ArtifactMapper.Selection(m.getName(), m.getModule(), m.getClassifier(), m.getType()));
        return ArtifactMapper.materialize(requested, root(), selections, ArtifactMapper.collect(session.getProjects()));
    }
    @Parameter(defaultValue = "${mojoExecution}", readonly = true)
    protected MojoExecution mojoExecution;

    static Map<String,String> credentialEnvironment(String goal, Map<String,String> mappings,
            List<String> command, Function<String,String> lookup) throws IOException {
        Map<String,String> values = new HashMap<>();
        if (mappings == null || !Set.of("doctor", "publish", "resume").contains(goal)) return values;
        Set<String> names = new HashSet<>();
        List<Map.Entry<String,String>> sorted = new ArrayList<>(mappings.entrySet());
        sorted.sort(Comparator.comparing(e -> e.getKey() == null ? "" : e.getKey()));
        for (var entry : sorted) {
            String reference = entry.getKey(), environment = entry.getValue();
            if (reference == null || !reference.matches("[A-Za-z0-9][A-Za-z0-9._-]*") ||
                environment == null || !environment.matches("[A-Za-z_][A-Za-z0-9_]*") ||
                !names.add(environment.toUpperCase(Locale.ROOT)))
                throw new IOException("Invalid or conflicting credential mapping");
            String value = lookup.apply(environment);
            if ((value == null || value.isEmpty()) && !"doctor".equals(goal))
                throw new IOException("Required credential environment source is unavailable");
            // Doctor must see the reference even when its environment variable is unset.
            command.add("--credential");
            command.add(reference + "=" + environment);
            if (value != null && !value.isEmpty()) values.put(environment, value);
        }
        return values;
    }

    private String configuredGoal(String name) {
        if (mojoExecution == null || mojoExecution.getPlugin() == null) return "the configured Maven Distroplane " + name + " goal";
        var plugin = mojoExecution.getPlugin();
        String group = plugin.getGroupId(), artifact = plugin.getArtifactId(), version = plugin.getVersion();
        if (group == null || artifact == null || version == null ||
            !group.matches("[A-Za-z0-9_.-]+") ||
            !artifact.matches("[A-Za-z0-9_.-]+") ||
            !version.matches("[A-Za-z0-9_.-]+"))
            return "the configured Maven Distroplane " + name + " goal";
        return group + ":" + artifact + ":" + version + ":" + name;
    }

    static String preCliAction(IOException failure) {
        String reason = failure.getMessage() == null ? "" : failure.getMessage().toLowerCase(Locale.ROOT);
        if (reason.contains("credential mapping") || reason.contains("credential environment"))
            return "Correct credentialMappings and configure an approved, populated environment source";
        if (reason.contains("mapping") || reason.contains("packaged") || reason.contains("artifact"))
            return "Package the selected reactor artifacts and correct artifactMappings before rerunning this goal";
        if (reason.contains("config") || reason.contains("reactor root"))
            return "Correct distroplane.config and ensure it is inside the Maven reactor root";
        if (reason.contains("timed out") || reason.contains("stream did not close"))
            return "Inspect the original recorded run before any retry; a publication side effect may have occurred";
        if (reason.contains("corrupt") || reason.contains("checksum") || reason.contains("unsafe"))
            return "Manually repair the corrupt verified CLI installation or cache; preserve the original release journal";
        if (reason.contains("offline") || reason.contains("not present"))
            return "Populate the verified CLI cache online before running Maven offline";
        if (reason.contains("executable") || reason.contains("process") || reason.contains("permission"))
            return "Check the installed CLI version, platform support, and executable permissions";
        return "Check the original configuration, native CLI installation, and recorded release state";
    }

    protected void executeCli(List<String> args, boolean useConfig) throws MojoExecutionException, MojoFailureException {
        if (timeoutSeconds < 1 || timeoutSeconds > 86400)
            throw new MojoFailureException("Invalid Distroplane timeoutSeconds; select 1–86400 seconds");
        String goal = args.get(0);
        try {
            if (useConfig) { args.add("--config"); args.add(configPath().toString()); }
            List<String> command = new ArrayList<>(args);
            Map<String,String> sources = credentialEnvironment(goal, credentialMappings, command, System::getenv);
            command.add("--json");
            Path cache = Path.of(session.getLocalRepository().getBasedir(), "distroplane", "cli");
            Path binary = new CliBootstrap().resolve(cache, cliVersion,
                CliBootstrap.Platform.current(System.getProperty("os.name"), System.getProperty("os.arch")),
                session.isOffline(), archiveSha256);
            CliRunner.Result result = CliRunner.run(binary, root(), command, sources,
                Duration.ofSeconds(timeoutSeconds), getLog());
            CliFeedback.Detail detail = CliFeedback.interpret(goal, result);
            getLog().info("Distroplane " + goal + ": " + detail.summary());
            if (result.exitCode() != 0) {
                String next = detail.nextGoal() == null ? "" : " Next goal: " + configuredGoal(detail.nextGoal()) + ".";
                throw new MojoFailureException("Distroplane " + goal + " exited with code " +
                    result.exitCode() + ": " + detail.summary() + ". " + detail.action() + "." + next);
            }
            if (!detail.action().isEmpty()) getLog().info("Next: " + detail.action());
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new MojoExecutionException("Distroplane invocation interrupted; inspect the original run before retrying");
        } catch (IOException e) {
            throw new MojoExecutionException("Distroplane " + goal + " could not complete safely. " + preCliAction(e));
        }
    }
}
