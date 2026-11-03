package io.github.aalsanie.distroplane.gradle;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.gradle.api.GradleException;
import org.gradle.api.Project;
import org.gradle.api.plugins.JavaPlugin;
import org.gradle.testfixtures.ProjectBuilder;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

final class DistroplaneTaskTest {
    @TempDir Path root;
    Project project;
    DistroplaneExtension options;

    @BeforeEach void createProject() throws Exception {
        root = root.toRealPath();
        project = ProjectBuilder.builder().withProjectDir(root.toFile()).build();
        project.getPlugins().apply(DistroplanePlugin.class);
        options = project.getExtensions().getByType(DistroplaneExtension.class);
    }

    DistroplaneTask task(String action) {
        return (DistroplaneTask) project.getTasks().getByName("distroplane" +
            Character.toUpperCase(action.charAt(0)) + action.substring(1));
    }

    @SuppressWarnings("unchecked")
    List<String> arguments(String action) throws Exception {
        Method method = DistroplaneTask.class.getDeclaredMethod("arguments", Path.class);
        method.setAccessible(true);
        try { return (List<String>) method.invoke(task(action), root.toRealPath()); }
        catch (InvocationTargetException e) { throw (Exception) e.getCause(); }
    }

    @SuppressWarnings("unchecked")
    Map<String, String> credentials(String action, List<String> args) throws Exception {
        Method method = DistroplaneTask.class.getDeclaredMethod("credentialSources", List.class);
        method.setAccessible(true);
        try { return (Map<String, String>) method.invoke(task(action), args); }
        catch (InvocationTargetException e) { throw (Exception) e.getCause(); }
    }

    @Test void registersAllTasksWithoutImplicitDependencies() {
        for (String name : List.of("init", "validate", "plan", "publish", "status", "resume")) {
            DistroplaneTask entry = task(name);
            assertEquals(name, entry.getOperation());
            assertSame(options, entry.getOptions());
            assertTrue(entry.getTaskDependencies().getDependencies(entry).isEmpty());
        }
        assertEquals("0.9.3", options.getCliVersion().get());
        assertEquals(900, options.getTimeoutSeconds().get());
        assertFalse(options.getApprove().get());
        assertEquals("distroplane.json", options.getConfig().get());
    }

    @Test void cannotAttachPluginToSubproject() {
        Project child = ProjectBuilder.builder().withParent(project).withName("child").build();
        assertThrows(GradleException.class, () -> child.getPlugins().apply(DistroplanePlugin.class));
    }

    @Test void buildsAllExplicitCommandsAndRecoverySelectors() throws Exception {
        assertEquals(List.of("validate", "--config", root.resolve("distroplane.json").toString()),
            arguments("validate"));
        assertEquals(List.of("plan", "--config", root.resolve("distroplane.json").toString()),
            arguments("plan"));
        options.getRelease().set("my-release");
        options.getRun().set("my-run");
        options.getReconcileOnly().set(true);
        assertEquals(List.of("status", "--release", "my-release", "--run", "my-run",
            "--config", root.resolve("distroplane.json").toString()), arguments("status"));
        assertEquals(List.of("resume", "--release", "my-release", "--run", "my-run", "--reconcile-only",
            "--config", root.resolve("distroplane.json").toString()), arguments("resume"));
        options.getRelease().set("");
        options.getRun().set("");
        options.getReconcileOnly().set(false);
        assertEquals(List.of("resume", "--config", root.resolve("distroplane.json").toString()),
            arguments("resume"));
    }

    @Test void rejectsUnapprovedOrDoubleApprovedPublication() throws Exception {
        IOException missing = assertThrows(IOException.class, () -> arguments("publish"));
        assertTrue(missing.getMessage().contains("exactly one"));
        options.getApprove().set(true);
        assertEquals(List.of("publish", "--yes", "--config", root.resolve("distroplane.json").toString()),
            arguments("publish"));
        options.getConfirmPlan().set("id");
        assertThrows(IOException.class, () -> arguments("publish"));
        options.getApprove().set(false);
        assertEquals(List.of("publish", "--confirm-plan", "id",
            "--config", root.resolve("distroplane.json").toString()), arguments("publish"));
        options.getConfirmPlan().set("");
        assertThrows(IOException.class, () -> arguments("publish"));
    }

    @Test void validatesInitFieldsAndConfinesOutputToRoot() throws Exception {
        assertThrows(IOException.class, () -> arguments("init"));
        options.getInitDestination().set("npm");
        assertThrows(IOException.class, () -> arguments("init"));
        options.getInitRelease().set("v1");
        options.getInitArtifact().set("tgz");
        options.getInitFields().put("tag", "latest");
        assertEquals(List.of("init", "--destination", "npm", "--release", "v1",
            "--artifact", "tgz", "--output", root.resolve("distroplane.json").toString(),
            "--non-interactive", "--set", "tag=latest"), arguments("init"));
        options.getInitOutput().set("../outside");
        assertThrows(IOException.class, () -> arguments("init"));
        options.getInitOutput().set("safe.json");
        options.getInitFields().put("?", "value");
        assertThrows(IOException.class, () -> arguments("init"));
        options.getInitFields().put("?", "");
        assertThrows(IOException.class, () -> arguments("init"));
    }

    @Test void validatesCredentialMappingsBeforeCLIExecution() throws Exception {
        assertTrue(credentials("plan", new java.util.ArrayList<>()).isEmpty());
        assertTrue(credentials("status", new java.util.ArrayList<>()).isEmpty());
        options.getCredentialMappings().put("npm-publish", "MISSING_DISTROPLANE_ENV_19052");
        assertThrows(IOException.class, () -> credentials("publish", new java.util.ArrayList<>()));
        options.getCredentialMappings().put("npm-publish", "PATH");
        var cmd = new java.util.ArrayList<String>();
        Map<String, String> passed = credentials("publish", cmd);
        assertTrue(passed.containsKey("PATH"));
        assertEquals(List.of("--credential", "npm-publish=PATH"), cmd);
        assertEquals(passed, credentials("resume", new java.util.ArrayList<>()));
        options.getCredentialMappings().put("other", "path");
        assertThrows(IOException.class, () -> credentials("publish", new java.util.ArrayList<>()));
        options.getCredentialMappings().put("other", "BAD-NAME");
        assertThrows(IOException.class, () -> credentials("resume", new java.util.ArrayList<>()));
        options.getCredentialMappings().put("?", "AN_ENV");
        assertThrows(IOException.class, () -> credentials("publish", new java.util.ArrayList<>()));
    }

    @Test void refusesSymlinksDirectoriesAndPathTraversal() throws Exception {
        options.getConfig().set("../other.json");
        assertThrows(IOException.class, () -> arguments("validate"));
        options.getConfig().set("");
        assertThrows(IOException.class, () -> arguments("validate"));
        Files.createDirectory(root.resolve("configuration"));
        options.getConfig().set("configuration");
        assertThrows(IOException.class, () -> arguments("validate"));
        if (!System.getProperty("os.name").toLowerCase().contains("win")) {
            Files.writeString(root.resolve("actual.json"), "{}");
            Files.createSymbolicLink(root.resolve("link.json"), root.resolve("actual.json"));
            options.getConfig().set("link.json");
            assertThrows(IOException.class, () -> arguments("validate"));
            Files.createDirectory(root.resolve("real-directory"));
            Files.createSymbolicLink(root.resolve("nested-link"), root.resolve("real-directory"));
            options.getConfig().set("nested-link/config.json");
            assertThrows(IOException.class, () -> arguments("validate"));
        }
        options.getConfig().set("distroplane.json");
        assertDoesNotThrow(() -> arguments("validate"));
    }

    @Test void mapsExactProducedArchiveTaskWithoutExecutingBuild() throws Exception {
        Project child = ProjectBuilder.builder().withParent(project).withName("child").build();
        child.getPlugins().apply(JavaPlugin.class);
        var jar = (org.gradle.api.tasks.bundling.Jar)child.getTasks().getByName("jar");
        Path output = jar.getArchiveFile().get().getAsFile().toPath();
        Files.createDirectories(output.getParent());
        Files.writeString(output, "archive");
        Files.writeString(root.resolve("distroplane.json"), """
            {"schemaVersion":"2","release":{"id":"r","artifacts":{"app":{"source":"unbuilt.jar"}}},"destinations":{}}
            """);
        options.getArtifactTasks().put("app", ":child:jar");
        List<String> args = arguments("plan");
        Path generated = root.resolve(".distroplane-gradle/distroplane.json");
        assertTrue(args.contains(generated.toString()));
        var document = new ObjectMapper().readTree(Files.readAllBytes(generated));
        assertEquals(output.toRealPath().toString(),
            document.path("release").path("artifacts").path("app").path("source").asText());
        assertEquals("unbuilt.jar", new ObjectMapper().readTree(Files.readAllBytes(root.resolve("distroplane.json")))
            .path("release").path("artifacts").path("app").path("source").asText());
        Files.delete(output);
        assertThrows(IOException.class, () -> arguments("plan"));
        options.getArtifactTasks().put("app", ":child:compileJava");
        assertThrows(IOException.class, () -> arguments("plan"));
        options.getArtifactTasks().put("app", ":missing:jar");
        assertThrows(IOException.class, () -> arguments("plan"));
        options.getArtifactTasks().put("app", "child:jar");
        assertThrows(IOException.class, () -> arguments("plan"));
    }

    @Test void validatesTimeoutBeforeAnyNetworkOrPublication() {
        options.getTimeoutSeconds().set(0);
        assertThrows(GradleException.class, () -> task("publish").invoke());
        options.getTimeoutSeconds().set(86401);
        assertThrows(GradleException.class, () -> task("publish").invoke());
        options.getTimeoutSeconds().set(1);
        assertThrows(GradleException.class, () -> task("publish").invoke()); // approval required
    }
}
