package io.github.aalsanie.distroplane.gradle;

import org.gradle.api.provider.MapProperty;
import org.gradle.api.provider.Property;

/** Gradle configuration for Distroplane CLI operations. */
public abstract class DistroplaneExtension {
    public abstract Property<String> getConfig();
    public abstract Property<String> getCliVersion();
    public abstract Property<String> getArchiveSha256();
    public abstract Property<Integer> getTimeoutSeconds();
    /** Distroplane artifact name to exact Gradle archive task path, e.g. :app:jar. */
    public abstract MapProperty<String, String> getArtifactTasks();
    /** Maps Distroplane credential IDs to environment variable names. */
    public abstract MapProperty<String, String> getCredentialMappings();
    /** Opt-in network connectivity checks; doctor never publishes. */
    public abstract Property<Boolean> getDoctorRemote();
    public abstract Property<Boolean> getApprove();
    public abstract Property<String> getConfirmPlan();
    public abstract Property<String> getRelease();
    public abstract Property<String> getRun();
    public abstract Property<Boolean> getReconcileOnly();
    public abstract Property<String> getInitDestination();
    public abstract Property<String> getInitRelease();
    public abstract Property<String> getInitArtifact();
    public abstract Property<String> getInitOutput();
    public abstract MapProperty<String, String> getInitFields();
}
