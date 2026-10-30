package io.github.aalsanie.distroplane.maven;

import org.apache.maven.plugin.MojoExecutionException;
import org.apache.maven.plugin.MojoFailureException;
import org.apache.maven.plugins.annotations.Mojo;
import org.apache.maven.plugins.annotations.Parameter;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

// Goals are explicit, aggregator-only and have no Maven lifecycle default phase.
@Mojo(name="init", aggregator=true, threadSafe=true, requiresProject=true)
public final class InitMojo extends AbstractDistroplaneMojo {
    @Parameter(property="distroplane.destination", required=true)
    private String destination;
    @Parameter(property="distroplane.release", required=true)
    private String release;
    @Parameter(property="distroplane.artifact")
    private String artifact;
    @Parameter(property="distroplane.output", defaultValue="distroplane.json")
    private String output;
    @Parameter
    private Map<String,String> initFields;

    @Override public void execute() throws MojoExecutionException, MojoFailureException {
        if (destination == null || destination.isBlank() || release == null || release.isBlank())
            throw new MojoFailureException("init requires explicit destination and release ID");
        List<String> args = new ArrayList<>(List.of("init", "--destination", destination, "--release", release));
        if (artifact != null && !artifact.isBlank()) { args.add("--artifact"); args.add(artifact); }
        args.add("--output"); args.add(root().resolve(output).normalize().toString());
        args.add("--non-interactive");
        if (initFields != null) for (var field : initFields.entrySet()) {
            if (field.getKey() == null || field.getValue() == null || field.getValue().isBlank() ||
                !field.getKey().matches("[A-Za-z0-9._-]+")) throw new MojoFailureException("Invalid init field");
            args.add("--set"); args.add(field.getKey() + "=" + field.getValue());
        }
        executeCli(args, false);
    }
}
