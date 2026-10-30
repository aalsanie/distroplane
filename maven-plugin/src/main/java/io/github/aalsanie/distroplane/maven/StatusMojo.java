package io.github.aalsanie.distroplane.maven;
import org.apache.maven.plugin.MojoExecutionException;
import org.apache.maven.plugin.MojoFailureException;
import org.apache.maven.plugins.annotations.Mojo;
import org.apache.maven.plugins.annotations.Parameter;
import java.util.ArrayList;
import java.util.List;
@Mojo(name="status", aggregator=true, threadSafe=true, requiresProject=true)
public final class StatusMojo extends AbstractDistroplaneMojo {
    @Parameter(property="distroplane.release") private String release;
    @Parameter(property="distroplane.run") private String run;
    @Override public void execute() throws MojoExecutionException, MojoFailureException {
        List<String> args = new ArrayList<>(List.of("status"));
        if (release != null && !release.isBlank()) { args.add("--release"); args.add(release); }
        if (run != null && !run.isBlank()) { args.add("--run"); args.add(run); }
        executeCli(args, true);
    }
}
