package io.github.aalsanie.distroplane.maven;

import org.apache.maven.plugin.MojoExecutionException;
import org.apache.maven.plugin.MojoFailureException;
import org.apache.maven.plugins.annotations.Mojo;
import org.apache.maven.plugins.annotations.Parameter;
import java.util.ArrayList;
import java.util.List;

/** Read-only release readiness; no lifecycle binding or implicit artifact build. */
@Mojo(name="doctor", aggregator=true, threadSafe=true, requiresProject=true)
public final class DoctorMojo extends AbstractDistroplaneMojo {
    @Parameter(property="distroplane.doctorRemote", defaultValue="false")
    private boolean remote;

    @Override public void execute() throws MojoExecutionException, MojoFailureException {
        List<String> args = new ArrayList<>(List.of("doctor"));
        if (remote) args.add("--remote");
        executeCli(args, true);
    }
}
