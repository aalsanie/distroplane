package io.github.aalsanie.distroplane.gradle;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.gradle.testkit.runner.BuildResult;
import org.gradle.testkit.runner.GradleRunner;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.junit.jupiter.api.io.CleanupMode;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

/** Executes actual isolated Gradle builds against the native nonpublishing fixture. */
final class DistroplaneFunctionalTest {
    @TempDir Path temp;
    @TempDir(cleanup = CleanupMode.NEVER) Path gradleHome;
    Path project;
    Path fixture;
    String pin = "a".repeat(64);
    static final ObjectMapper JSON = new ObjectMapper();

    @BeforeEach void setup() throws Exception {
        project = Files.createDirectories(temp.resolve("project")).toRealPath();
        Files.writeString(project.resolve("settings.gradle"), "rootProject.name = 'distroplane-test'\n");
        Files.writeString(project.resolve("build.gradle"), "plugins { id 'io.github.aalsanie.distroplane' }\n");
        fixture = Path.of(System.getenv().getOrDefault("DISTROPLANE_FIXTURE_BINARY", ""));
    }

    private void configure(String content) throws IOException {
        Files.writeString(project.resolve("build.gradle"),
            "plugins { id 'io.github.aalsanie.distroplane' }\n" +
            "distroplane { cliVersion = '0.9.3'; archiveSha256 = '" + pin + "'; " + content + " }\n");
    }

    private BuildResult run(boolean success, String... tasks) {
        List<String> args = new ArrayList<>(List.of("--offline", "--no-watch-fs", "--stacktrace", "--gradle-user-home", gradleHome.toString()));
        args.addAll(List.of(tasks));
        boolean kotlinDsl = Files.isRegularFile(project.resolve("build.gradle.kts"));
        GradleRunner runner = GradleRunner.create().withProjectDir(project.toFile())
            .withPluginClasspath().withArguments(args).withDebug(!kotlinDsl);
        return success ? runner.build() : runner.buildAndFail();
    }

    private void cacheFixture() throws Exception {
        assumeTrue(Files.isRegularFile(fixture), "Controlled CLI fixture must be built by CI");
        var p = CliBootstrap.Platform.current(System.getProperty("os.name"), System.getProperty("os.arch"));
        Path root = gradleHome.resolve("distroplane/cli/0.9.3/" + p.os() + "-" + p.arch() + "/sha256-" + pin);
        Files.createDirectories(root.resolve("bin"));
        Files.createDirectories(root.resolve("libexec"));
        List<Map<String, Object>> records = new ArrayList<>();
        byte[] binary = Files.readAllBytes(fixture);
        for (String name : p.members()) {
            if (name.equals("PACKAGE-METADATA.json")) continue;
            Path target = root.resolve(name);
            Files.write(target, binary);
            if (!p.os().equals("windows")) assertTrue(target.toFile().setExecutable(true));
            records.add(Map.of("path", name, "sha256", digest(binary), "size", binary.length, "mode", "0755"));
        }
        Map<String, Object> metadata = new LinkedHashMap<>();
        metadata.put("schemaVersion", "1");
        metadata.put("version", "0.9.3");
        metadata.put("os", p.os());
        metadata.put("arch", p.arch());
        metadata.put("cgoEnabled", false);
        metadata.put("files", records);
        Files.write(root.resolve("PACKAGE-METADATA.json"), JSON.writeValueAsBytes(metadata));
        Files.writeString(root.resolve("archive.sha256"), pin);
    }

    private static String digest(byte[] bytes) throws Exception {
        return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes));
    }

    private static String config() {
        return """
            {"schemaVersion":"2","release":{"id":"fixture-release","artifacts":{"package":{"source":"release.tgz"}}},
            "destinations":{"npm":{"artifact":"package","options":{"packagePath":"release.tgz",
            "registry":"https://registry.npmjs.org/","authentication":{"mode":"token","credential":"npm-publish"}}}}}
            """;
    }

    @Test void registersSevenOptInTasksWithoutLifecyclePublication() throws Exception {
        BuildResult tasks = run(true, "tasks", "--all");
        for (String name : List.of("distroplaneInit", "distroplaneValidate", "distroplanePlan",
            "distroplanePublish", "distroplaneStatus", "distroplaneResume", "distroplaneDoctor")) assertTrue(tasks.getOutput().contains(name));
        run(true, "help");
        assertFalse(Files.exists(project.resolve(".distroplane")));
    }

    @Test void doctorChecksReadinessWithoutPublicationAndHandlesUnsetCredentials() throws Exception {
        cacheFixture();
        configure("doctorRemote = false");
        Files.writeString(project.resolve("distroplane.json"), config());
        BuildResult healthy = run(true, "distroplaneDoctor");
        assertTrue(healthy.getOutput().contains("readiness checks passed"));
        assertFalse(Files.exists(project.resolve(".distroplane/fixture-run.json")));
        configure("doctorRemote = true");
        BuildResult remote = run(true, "distroplaneDoctor");
        assertTrue(remote.getOutput().contains("readiness checks passed"));
        assertFalse(Files.exists(project.resolve(".distroplane/fixture-run.json")));
        configure("credentialMappings.put('npm-publish', 'MISSING_DISTROPLANE_GRADLE_SECRET_39755')");
        BuildResult missing = run(false, "distroplaneDoctor");
        assertTrue(missing.getOutput().contains("MISSING_CREDENTIAL"));
        assertTrue(missing.getOutput().contains("exited with code 3"));
        assertTrue(missing.getOutput().contains("distroplaneDoctor"));
        assertFalse(Files.exists(project.resolve(".distroplane/fixture-run.json")));
    }

    @Test void verifiesOfflineBootstrapAndOriginalRunRecovery() throws Exception {
        cacheFixture();
        configure("approve = true; timeoutSeconds = 5");
        Files.writeString(project.resolve("distroplane.json"), config());
        run(true, "distroplaneValidate");
        run(true, "distroplanePlan");
        Files.createFile(project.resolve(".fixture-pending"));
        assertTrue(run(false, "distroplanePublish").getOutput().contains("exited with code 4"));
        Path stateFile = project.resolve(".distroplane/fixture-run.json");
        var before = JSON.readTree(Files.readAllBytes(stateFile));
        assertEquals(1, before.path("attempts").asInt());
        assertTrue(run(false, "distroplanePublish").getOutput().contains("exited with code 3"));
        assertEquals(before, JSON.readTree(Files.readAllBytes(stateFile)));
        BuildResult pending = run(false, "distroplaneStatus");
        assertTrue(pending.getOutput().contains("exited with code 4"));
        assertTrue(pending.getOutput().contains("awaiting external acceptance"));
        assertFalse(pending.getOutput().contains("recorded as published"));
        Files.delete(project.resolve(".fixture-pending"));
        run(true, "distroplaneResume");
        var after = JSON.readTree(Files.readAllBytes(stateFile));
        assertEquals(before.path("planId"), after.path("planId"));
        assertEquals(before.path("runId"), after.path("runId"));
        assertEquals(1, after.path("attempts").asInt());
        assertTrue(after.path("completed").asBoolean());
    }

    @Test void refusesUnapprovedPublicationAndMismatchedApprovalOptions() throws Exception {
        cacheFixture();
        Files.writeString(project.resolve("distroplane.json"), config());
        configure("");
        assertTrue(run(false, "distroplanePublish").getOutput().contains("Select exactly one"));
        assertFalse(Files.exists(project.resolve(".distroplane")));
        configure("approve = true; confirmPlan = 'fixture-plan'");
        assertTrue(run(false, "distroplanePublish").getOutput().contains("Select exactly one"));
        configure("confirmPlan = 'fixture-plan'");
        run(true, "distroplanePublish");
        assertTrue(Files.isRegularFile(project.resolve(".distroplane/fixture-run.json")));
    }

    @Test void timeoutAndCorruptCacheNeverRestartPublication() throws Exception {
        cacheFixture();
        configure("approve = true; timeoutSeconds = 1");
        Files.writeString(project.resolve("distroplane.json"), config());
        Files.createFile(project.resolve(".fixture-hang"));
        assertTrue(run(false, "distroplanePublish").getOutput().contains("timed out"));
        var state = JSON.readTree(Files.readAllBytes(project.resolve(".distroplane/fixture-run.json")));
        Files.delete(project.resolve(".fixture-hang"));
        run(true, "distroplaneResume");
        var resumed = JSON.readTree(Files.readAllBytes(project.resolve(".distroplane/fixture-run.json")));
        assertEquals(state.path("planId"), resumed.path("planId"));
        assertEquals(1, resumed.path("attempts").asInt());
        var p = CliBootstrap.Platform.current(System.getProperty("os.name"), System.getProperty("os.arch"));
        Path cli = gradleHome.resolve("distroplane/cli/0.9.3/" + p.os() + "-" + p.arch() + "/sha256-" + pin).resolve(p.cli());
        Files.writeString(cli, "CORRUPTED", java.nio.file.StandardOpenOption.APPEND);
        assertTrue(run(false, "distroplaneValidate").getOutput().contains("corrupt"));
    }

    @Test void mapsExactArchiveTasksWithoutModifyingSourceConfiguration() throws Exception {
        cacheFixture();
        Files.writeString(project.resolve("settings.gradle"),
            "rootProject.name = 'multi'\ninclude 'alpha', 'beta'\n");
        Files.createDirectories(project.resolve("alpha"));
        Files.createDirectories(project.resolve("beta"));
        Files.writeString(project.resolve("build.gradle"),
            "plugins { id 'io.github.aalsanie.distroplane' }\n" +
            "subprojects { apply plugin: 'java' }\n" +
            "distroplane { archiveSha256 = '" + pin + "'; artifactTasks.put('app', ':alpha:jar'); artifactTasks.put('src', ':beta:jar') }\n");
        String raw = """
            {"schemaVersion":"2","release":{"id":"fixture","artifacts":{
            "app":{"source":"original-a.jar"},"src":{"source":"original-b.jar"},"unmapped":{"source":"untouched.txt"}}},
            "destinations":{}}
            """;
        Files.writeString(project.resolve("distroplane.json"), raw);
        run(true, ":alpha:jar", ":beta:jar");
        run(true, "distroplaneValidate");
        var mapped = JSON.readTree(Files.readAllBytes(project.resolve(".distroplane-gradle/distroplane.json")));
        assertTrue(Path.of(mapped.path("release").path("artifacts").path("app").path("source").asText()).toFile().isFile());
        assertTrue(Path.of(mapped.path("release").path("artifacts").path("src").path("source").asText()).toFile().isFile());
        assertEquals(project.resolve("untouched.txt").toString(),
            mapped.path("release").path("artifacts").path("unmapped").path("source").asText());
        assertEquals(raw, Files.readString(project.resolve("distroplane.json")));
        Files.delete(Path.of(mapped.path("release").path("artifacts").path("app").path("source").asText()));
        assertTrue(run(false, "distroplanePlan").getOutput().contains("has not been packaged"));
    }

    @Test void rejectsInvalidTasksAndPathsBeforeStartingCli() throws Exception {
        configure("artifactTasks.put('app', 'jar')");
        assertTrue(run(false, "distroplanePlan").getOutput().contains("Invalid Gradle archive task path"));
        configure("artifactTasks.put('app', ':missing:jar')");
        assertTrue(run(false, "distroplanePlan").getOutput().contains("requires an existing Gradle archive task"));
        configure("config = '../other.json'");
        assertTrue(run(false, "distroplaneValidate").getOutput().contains("inside Gradle root"));
        configure("initOutput = '../other.json'; initDestination = 'npm'; initRelease = 'v1'");
        assertTrue(run(false, "distroplaneInit").getOutput().contains("inside Gradle root"));
        configure("initDestination = 'npm'");
        assertTrue(run(false, "distroplaneInit").getOutput().contains("init requires explicit"));
        configure("approve = true; credentialMappings.put('npm-publish', 'MISSING_DISTROPLANE_GRADLE_SECRET_39755')");
        assertTrue(run(false, "distroplanePublish").getOutput().contains("environment variable is unavailable"));
    }

    @Test void KotlinDslCanRegisterPluginTasks() throws Exception {
        Files.delete(project.resolve("build.gradle"));
        Files.writeString(project.resolve("settings.gradle.kts"), "rootProject.name = \"kt-fixture\"\n");
        Files.delete(project.resolve("settings.gradle"));
        Files.writeString(project.resolve("build.gradle.kts"), """
            plugins { id("io.github.aalsanie.distroplane") }
            distroplane {
                cliVersion.set("0.9.3")
                config.set("distroplane.json")
                doctorRemote.set(true)
            }
            """);
        BuildResult result = run(true, "tasks", "--all");
        assertTrue(result.getOutput().contains("distroplaneResume"));
        assertTrue(result.getOutput().contains("distroplaneDoctor"));
    }
}
