package io.github.aalsanie.distroplane.gradle;

import org.gradle.api.GradleException;
import org.gradle.api.Plugin;
import org.gradle.api.Project;

/** Registers six explicit native CLI operations; never hooks a Gradle lifecycle task. */
public final class DistroplanePlugin implements Plugin<Project> {
    @Override public void apply(Project project) {
        if (project != project.getRootProject()) throw new GradleException("Apply Distroplane only to the root project");
        DistroplaneExtension options = project.getExtensions().create("distroplane", DistroplaneExtension.class);
        options.getConfig().convention("distroplane.json");
        options.getCliVersion().convention("0.9.3");
        options.getTimeoutSeconds().convention(900);
        options.getArtifactTasks().convention(java.util.Map.of());
        options.getCredentialMappings().convention(java.util.Map.of());
        options.getApprove().convention(false);
        options.getReconcileOnly().convention(false);
        options.getInitOutput().convention("distroplane.json");
        options.getInitFields().convention(java.util.Map.of());
        for (String operation : new String[]{"init", "validate", "plan", "publish", "status", "resume"}) {
            String name = "distroplane" + Character.toUpperCase(operation.charAt(0)) + operation.substring(1);
            project.getTasks().register(name, DistroplaneTask.class, task -> {
                task.setGroup("publishing");
                task.setDescription("Run Distroplane " + operation + " without changing the Gradle build lifecycle.");
                task.setOptions(options);
                task.setOperation(operation);
                task.getOutputs().upToDateWhen(ignored -> false);
                task.notCompatibleWithConfigurationCache("Explicit archive selection reads the existing Gradle task graph");
            });
        }
    }
}
