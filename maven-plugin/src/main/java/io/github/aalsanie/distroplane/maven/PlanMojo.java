package io.github.aalsanie.distroplane.maven;
import org.apache.maven.plugin.MojoExecutionException;
import org.apache.maven.plugin.MojoFailureException;
import org.apache.maven.plugins.annotations.Mojo;
import java.util.ArrayList;
import java.util.List;
@Mojo(name="plan", aggregator=true, threadSafe=true, requiresProject=true)
public final class PlanMojo extends AbstractDistroplaneMojo {
    @Override public void execute() throws MojoExecutionException, MojoFailureException { executeCli(new ArrayList<>(List.of("plan")), true); }
}
