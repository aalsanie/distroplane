# Distroplane Gradle plugin

The Gradle plugin invokes the native Distroplane CLI; it never publishes directly. It registers six opt-in tasks only and is not attached to `build`, `assemble`, or `publish`. The plugin source targets Java 17+, Gradle 8.14.6 and Gradle 9.8.2. The native CLI version defaults to `0.9.3`; the plugin is independently versioned and is not yet published.

```groovy
plugins {
    id 'io.github.aalsanie.distroplane'
}

distroplane {
    cliVersion = '0.9.3'
    config = 'distroplane.json'
    // archiveSha256 = 'exact 64-character official release archive SHA-256'
}
```

Run `gradle distroplaneValidate`, `gradle distroplanePlan`, `gradle distroplaneStatus`, or `gradle distroplaneResume`. To publish, explicitly set exactly one of `approve = true` or `confirmPlan = '<exact plan ID>'` and invoke `gradle distroplanePublish`. Pending external state (exit 4) fails the Gradle task; inspect the same release state and use `distroplaneResume`, never a new `publish` invocation. `distroplaneInit` requires `initDestination` and `initRelease`, with nonsecret `initFields`. `release`, `run`, and `reconcileOnly` control status/resume.

The plugin downloads the official platform archive and `SHA256SUMS` and verifies the SHA-256 digest, exact package members, release identity and embedded executable hashes. Verified packages are cached under Gradle User Home at `distroplane/cli/<version>/<os>-<arch>/sha256-<digest>/`. Cached members are reverified on every use; corrupt caches fail closed. Gradle `--offline` forbids downloads and requires an already verified cached package. Use `archiveSha256` to pin the archive independently of the release-hosted checksum manifest. GitHub release checksums are not independent publisher signatures.

For multi-project builds, build the artifacts first, then explicitly map Distroplane artifact names to **exact** Gradle archive tasks (without adding an implicit build dependency):

```groovy
distroplane {
    artifactTasks.put('app', ':server:jar')
    artifactTasks.put('sources', ':client:sourcesJar')
}
```

Mappings must refer to already produced regular files and to artifact names already declared in `distroplane.json`. The plugin never guesses an artifact or builds one automatically. It preserves the original configuration and generates a stable, rebased copy in `.distroplane-gradle/distroplane.json`, alongside the unchanged `.distroplane` durable release state. Keep both directories for recovery. Apply the plugin to the root project only.

To opt into publishing credentials without passing secret values on the command line:

```groovy
distroplane {
    credentialMappings.put('npm-publish', 'NPM_TOKEN')
}
```

Only explicitly selected environment variable values are passed into the child process; CLI output containing any selected secret is suppressed. Configuration-cache execution is explicitly unsupported because exact archive task selection requires the live task graph. The native CLI remains authoritative for provider credentials, side effects, journaling and reconciliation.

Run `gradle -p gradle-plugin check` to execute unit and functional tests and the strict **greater-than-90% line and branch coverage** gates. The CI matrix exercises Gradle 8 and 9, JDK 17 and 21, and Linux/macOS/Windows.
