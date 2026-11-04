package io.github.aalsanie.distroplane.gradle;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.util.Locale;
import java.util.Set;

/** Fixed, copyable Gradle guidance; no raw native or provider text is echoed. */
final class TaskGuidance {
    private static final Set<String> GOALS = Set.of("doctor", "status", "resume", "plan", "validate");
    private TaskGuidance() { }

    static String task(Path root, String goal, String os) {
        if (!GOALS.contains(goal)) return "";
        String task = ":distroplane" + Character.toUpperCase(goal.charAt(0)) + goal.substring(1);
        if (os.toLowerCase(Locale.ROOT).contains("win")) {
            return Files.isRegularFile(root.resolve("gradlew.bat"), LinkOption.NOFOLLOW_LINKS) ? "gradlew.bat " + task : "gradle " + task;
        }
        return Files.isRegularFile(root.resolve("gradlew"), LinkOption.NOFOLLOW_LINKS) ? "./gradlew " + task : "gradle " + task;
    }

    static String next(Path root, String goal, String os) {
        if (!GOALS.contains(goal)) return "";
        String cmd = task(root, goal, os);
        if ("status".equals(goal) || "resume".equals(goal)) {
            return " Set distroplane.release or distroplane.run in the root build to select the exact recorded run, then use " + cmd + ".";
        }
        return " Next Gradle task: " + cmd + ".";
    }

    static String preCli(IOException error) {
        String text = error.getMessage() == null ? "" : error.getMessage().toLowerCase(Locale.ROOT);
        if (text.contains("invalid gradle archive task path"))
            return "Invalid Gradle archive task path; use an exact root-qualified archive task such as :app:jar";
        if (text.contains("requires an existing gradle archive task"))
            return "Mapping requires an existing Gradle archive task; correct distroplane.artifactTasks";
        if (text.contains("has not been packaged") || text.contains("mapped artifact"))
            return "Mapped release artifact has not been packaged; run the configured archive task explicitly";
        if (text.contains("inside gradle root") || text.contains("must not traverse a symbolic link"))
            return "Selected configuration path must be inside Gradle root and must not traverse symbolic links";
        if (text.contains("init requires explicit"))
            return "Distroplane init requires explicit destination and release ID";
        if (text.contains("select exactly one"))
            return "Select exactly one of approve=true or confirmPlan with the reviewed exact plan ID";
        if (text.contains("credential") && (text.contains("unavailable") || text.contains("invalid") || text.contains("conflicting")))
            return "Required credential environment variable is unavailable or mapping invalid; correct the approved source";
        if (text.contains("timed out") || text.contains("stream did not close"))
            return "Distroplane CLI timed out; inspect the original journal before retrying an uncertain publication";
        if (text.contains("corrupt") || text.contains("checksum") || text.contains("unsafe"))
            return "Verified CLI cache is corrupt or unsafe; repair it manually and preserve the original run";
        if (text.contains("offline") || text.contains("not present"))
            return "Offline CLI cache is missing; populate the verified release cache before retrying";
        if (text.contains("invalid or unsupported structured") || text.contains("could not read bounded") ||
            text.contains("excessive output") || text.contains("output suppressed"))
            return "Invalid or unsafe CLI output; preserve the original release run and inspect the verified CLI installation";
        if (text.contains("unsupported operating system") || text.contains("unsupported cpu architecture"))
            return "Unsupported CLI platform; select a compatible verified OS and architecture";
        if (text.contains("invalid distroplane timeoutseconds"))
            return "Invalid Distroplane timeoutSeconds; select a value from 1 to 86400";
        return "Distroplane could not start safely; check the root configuration and verified CLI installation";
    }
}
