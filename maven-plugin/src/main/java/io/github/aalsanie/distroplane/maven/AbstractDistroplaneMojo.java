package io.github.aalsanie.distroplane.maven;

import org.apache.maven.execution.MavenSession;
import org.apache.maven.plugin.AbstractMojo;
import org.apache.maven.plugin.MojoExecutionException;
import org.apache.maven.plugin.MojoFailureException;
import org.apache.maven.plugins.annotations.Parameter;

import java.io.IOException;
import java.nio.file.Path;
import java.nio.file.Files;
import java.time.Duration;
import java.util.*;

/** Thin wrapper: the native CLI owns planning, publication, journal and retry semantics. */
abstract class AbstractDistroplaneMojo extends AbstractMojo {
    @Parameter(defaultValue = "${session}", readonly = true, required = true)
    protected MavenSession session;
    @Parameter(property = "distroplane.config", defaultValue = "distroplane.json")
    protected String config;
    @Parameter(property = "distroplane.cliVersion", defaultValue = "0.9.2")
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
    protected void executeCli(List<String> args, boolean useConfig) throws MojoExecutionException, MojoFailureException {
        if (timeoutSeconds < 1 || timeoutSeconds > 86400) throw new MojoFailureException("Invalid Distroplane timeoutSeconds");
        try {
            if (useConfig) { args.add("--config"); args.add(configPath().toString()); }
            List<String> command = new ArrayList<>(args);
            Map<String,String> sources = new HashMap<>();
            if (credentialMappings != null && (args.get(0).equals("publish") || args.get(0).equals("resume"))) {
                Set<String> used = new HashSet<>();
                for (var item : credentialMappings.entrySet()) {
                    if (!item.getKey().matches("[A-Za-z0-9][A-Za-z0-9._-]*") ||
                        item.getValue() == null || !item.getValue().matches("[A-Za-z_][A-Za-z0-9_]*") ||
                        !used.add(item.getValue().toUpperCase(Locale.ROOT))) throw new IOException("Invalid or conflicting credential mapping");
                    String value = System.getenv(item.getValue());
                    if (value == null || value.isEmpty()) throw new IOException("Required credential environment variable is unavailable: " + item.getValue());
                    sources.put(item.getValue(), value);
                    command.add("--credential"); command.add(item.getKey() + "=" + item.getValue());
                }
            }
            command.add("--json");
            Path cache = Path.of(session.getLocalRepository().getBasedir(), "distroplane", "cli");
            Path binary = new CliBootstrap().resolve(cache, cliVersion,
                CliBootstrap.Platform.current(System.getProperty("os.name"), System.getProperty("os.arch")),
                session.isOffline(), archiveSha256);
            CliRunner.Result result = CliRunner.run(binary, root(), command, sources, Duration.ofSeconds(timeoutSeconds), getLog());
            // All non-zero CLI codes, including WAITING_EXTERNAL, must remain visible to Maven.
            // Never start a new run to turn a pending result into success.
            if (result.exitCode() != 0) throw new MojoFailureException("Distroplane " + args.get(0) + " exited with code " + result.exitCode() + "; inspect the existing release run");
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new MojoExecutionException("Distroplane invocation interrupted; recover the existing run", e);
        } catch (IOException e) {
            throw new MojoExecutionException("Distroplane invocation failed safely: " + e.getMessage(), e);
        }
    }
}
