# Distroplane Gradle plugin

Run Distroplane from Gradle 8.14.6 or 9.8.2 with Java 17+. Apply the plugin to the root project. Publication tasks are never attached to the build lifecycle.

```groovy
plugins {
    id 'io.github.aalsanie.distroplane' version '1.0.0'
}

distroplane {
    cliVersion = '0.9.3'
    config = 'distroplane.json'
}
```

## Usage

Build your artifacts, then review the release with `gradle distroplanePlan`. To publish, set either `approve = true` or `confirmPlan = '<exact plan ID>'` and run `gradle distroplanePublish`.

Use `distroplaneStatus` to inspect a run and `distroplaneResume` to continue it. Pending releases fail the task with CLI exit code 4; resume the original run rather than publishing again. Select a run with `release` or `run`, and use `reconcileOnly` for read-only reconciliation.

`distroplaneValidate` checks configuration. `distroplaneInit` requires `initDestination`, `initRelease` and any required nonsecret `initFields`. The source also provides `distroplaneDoctor`, not yet available in the published plugin.

## CLI installation

The plugin verifies the release archive, package metadata and executable hashes before caching the CLI and providers in Gradle User Home under `distroplane/cli/<version>/<os>-<arch>/sha256-<digest>/`. Cached files are verified on every invocation; corruption fails closed. Gradle `--offline` requires an existing verified cache.

Set `archiveSha256` to an independently obtained archive digest to pin the download. Release-hosted checksums alone do not authenticate the publisher.

## Multi-project builds

Map declared Distroplane artifact names to exact, already built archive tasks:

```groovy
distroplane {
    artifactTasks.put('app', ':server:jar')
    artifactTasks.put('sources', ':client:sourcesJar')
}
```

The plugin writes a rebased configuration to `.distroplane-gradle/distroplane.json` without modifying the original or running build tasks. Preserve this directory and the release state for recovery. Gradle configuration cache is unsupported because artifact selection reads the live task graph.

## Credentials

```groovy
distroplane {
    credentialMappings.put('npm-publish', 'NPM_TOKEN')
}
```

Only mapped credentials are passed to the CLI. Output containing their values is suppressed.
