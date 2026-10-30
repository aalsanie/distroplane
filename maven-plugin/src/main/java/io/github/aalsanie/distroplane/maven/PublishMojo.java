package io.github.aalsanie.distroplane.maven;
import org.apache.maven.plugin.MojoExecutionException;
import org.apache.maven.plugin.MojoFailureException;
import org.apache.maven.plugins.annotations.Mojo;
import org.apache.maven.plugins.annotations.Parameter;
import java.util.ArrayList;
import java.util.List;
@Mojo(name="publish", aggregator=true, threadSafe=true, requiresProject=true)
public final class PublishMojo extends AbstractDistroplaneMojo {
    @Parameter(property="distroplane.approve", defaultValue="false")
    private boolean approve;
    @Parameter(property="distroplane.confirmPlan")
    private String confirmPlan;
    @Override public void execute() throws MojoExecutionException, MojoFailureException {
        if (approve == (confirmPlan != null && !confirmPlan.isBlank()))
            throw new MojoFailureException("Explicitly select approve=true or an exact confirmPlan, but not both");
        List<String> args = new ArrayList<>(List.of("publish"));
        if (approve) args.add("--yes");
        else { args.add("--confirm-plan"); args.add(confirmPlan); }
        executeCli(args, true);
    }
}
