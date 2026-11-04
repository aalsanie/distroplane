package io.github.aalsanie.distroplane.gradle;

import org.gradle.api.GradleException;
import org.gradle.api.Plugin;
import org.gradle.api.Project;

/** Registers explicit native CLI operations. */
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
        options.getDoctorRemote().convention(false);
        options.getReconcileOnly().convention(false);
        options.getInitOutput().convention("distroplane.json");
        options.getInitFields().convention(java.util.Map.of());
        for (String operation : new String[]{"init", "doctor", "validate", "plan", "publish", "status", "resume"}) {
            String name = "distroplane" + Character.toUpperCase(operation.charAt(0)) + operation.substring(1);
            project.getTasks().register(name, DistroplaneTask.class, task -> {
                task.setGroup("publishing");
                task.setDescription(switch (operation) {
                    case "init" -> "Create a Distroplane configuration from explicit release metadata.";
                    case "doctor" -> "Check local release readiness; optionally probe remote connectivity.";
                    case "validate" -> "Validate the selected release configuration.";
                    case "plan" -> "Create and review an immutable release plan.";
                    case "publish" -> "Publish only after explicit plan approval.";
                    case "status" -> "Inspect the original recorded release run.";
                    case "resume" -> "Resume or reconcile the original recorded run.";
                    default -> throw new IllegalArgumentException("Unsupported Distroplane operation");
                });
                task.setOptions(options);
                task.setOperation(operation);
                task.getOutputs().upToDateWhen(ignored -> false);
                task.notCompatibleWithConfigurationCache("Explicit archive selection reads the existing Gradle task graph");
            });
        }
    }
}
