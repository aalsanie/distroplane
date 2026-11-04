package io.github.aalsanie.distroplane.gradle;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.*;

final class TaskGuidanceTest {
    @TempDir Path root;

    @Test void taskNamesAreRealAndPreferTheProjectWrapper() throws Exception {
        assertEquals("gradle :distroplaneDoctor", TaskGuidance.task(root, "doctor", "Linux"));
        assertEquals("gradle :distroplaneStatus", TaskGuidance.task(root, "status", "Windows 11"));
        Files.writeString(root.resolve("gradlew"), "#!/bin/sh");
        Files.writeString(root.resolve("gradlew.bat"), "@echo off");
        for (String goal : List.of("doctor", "validate", "plan", "status", "resume")) {
            assertTrue(TaskGuidance.task(root, goal, "Linux").startsWith("./gradlew :distroplane"));
            assertTrue(TaskGuidance.task(root, goal, "Windows 11").startsWith("gradlew.bat :distroplane"));
        }
        assertEquals("", TaskGuidance.task(root, "publish", "Linux"));
        assertEquals("", TaskGuidance.task(root, "evil", "Linux"));
        assertEquals("", TaskGuidance.next(root, "evil", "Linux"));
        String read = TaskGuidance.next(root, "status", "Linux");
        assertTrue(read.contains("distroplane.release or distroplane.run"));
        assertTrue(read.contains("./gradlew :distroplaneStatus"));
        assertFalse(read.contains("-P"));
        String resume = TaskGuidance.next(root, "resume", "Windows");
        assertTrue(resume.contains("gradlew.bat :distroplaneResume"));
        assertTrue(TaskGuidance.next(root, "doctor", "Linux").contains("./gradlew :distroplaneDoctor"));
        assertTrue(TaskGuidance.next(root, "plan", "Linux").contains("./gradlew :distroplanePlan"));
        assertTrue(TaskGuidance.next(root, "validate", "Linux").contains("./gradlew :distroplaneValidate"));
    }

    @Test void preexecutionErrorsHaveSafePreciseActions() {
        for (var tc : List.of(
            new String[]{"Invalid Gradle archive task path", "root-qualified"},
            new String[]{"Distroplane mapping requires an existing Gradle archive task", "correct distroplane.artifactTasks"},
            new String[]{"Mapped artifact has not been packaged", "run the configured archive task"},
            new String[]{"must be inside Gradle root", "inside Gradle root"},
            new String[]{"must not traverse a symbolic link", "inside Gradle root"},
            new String[]{"init requires explicit destination", "explicit destination"},
            new String[]{"Select exactly one of approve=true", "exact plan ID"},
            new String[]{"Required credential environment variable is unavailable", "credential environment variable is unavailable"},
            new String[]{"Invalid or conflicting credential mapping", "mapping invalid"},
            new String[]{"CLI timed out", "original journal"},
            new String[]{"output stream did not close", "original journal"},
            new String[]{"Cached CLI is corrupt", "corrupt"},
            new String[]{"Unsafe cache directory", "unsafe"},
            new String[]{"Checksum mismatch", "corrupt"},
            new String[]{"offline mode requires cache", "Offline CLI cache"},
            new String[]{"Requested package not present", "Offline CLI cache"},
            new String[]{"Invalid or unsupported structured CLI response", "Invalid or unsafe CLI output"},
            new String[]{"Could not read bounded CLI output", "Invalid or unsafe CLI output"},
            new String[]{"CLI produced excessive output", "Invalid or unsafe CLI output"},
            new String[]{"output suppressed", "Invalid or unsafe CLI output"},
            new String[]{"Unsupported operating system for Distroplane", "Unsupported CLI platform"},
            new String[]{"Unsupported CPU architecture for Distroplane", "Unsupported CLI platform"},
            new String[]{"Invalid Distroplane timeoutSeconds", "1 to 86400"},
            new String[]{"opaque provider exception", "could not start safely"}
        )) {
            String message = TaskGuidance.preCli(new IOException(tc[0] + " SECRET-CANARY"));
            assertTrue(message.contains(tc[1]), tc[0] + " => " + message);
            assertFalse(message.contains("SECRET-CANARY"), message);
        }
        assertFalse(TaskGuidance.preCli(new IOException()).isBlank());
    }
}
