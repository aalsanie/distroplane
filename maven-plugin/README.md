# Distroplane Maven plugin

A Maven **build plugin** which runs the native Distroplane CLI and providers. It does not build artifacts, publish directly, or attach publication goals to the normal Maven lifecycle.

Published Maven Central coordinates: `io.github.aalsanie:distroplane-maven-plugin:1.0.0`. Supported baseline: **JDK 17+**, **Maven 3.9+**, and Distroplane **CLI 0.9.2**. The Maven plugin and native CLI are versioned independently.

Add the published plugin to `<build><plugins>`:

```xml
<plugin>
  <groupId>io.github.aalsanie</groupId>
  <artifactId>distroplane-maven-plugin</artifactId>
  <version>1.0.0</version>
  <configuration>
    <cliVersion>0.9.2</cliVersion>
    <config>distroplane.json</config>
  </configuration>
</plugin>
```

In a project with a configured destination and a prebuilt release artifact:

```sh
mvn package
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0:validate
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0:plan
mvn -Ddistroplane.approve=true io.github.aalsanie:distroplane-maven-plugin:1.0.0:publish
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0:status
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0:resume
```

`init` requires a selected provider, release, artifact and provider-specific nonsecret metadata (configure `<initFields><field>value</field></initFields>` for the native `--set` options): `mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0:init -Ddistroplane.destination=npm -Ddistroplane.release=v1`. No prompts or secret values are supported for Maven `init`; missing metadata fails without generating a guessed configuration. `publish` refuses to execute unless either `-Ddistroplane.approve=true` or `-Ddistroplane.confirmPlan=<exact-id>` is supplied. `resume` uses the original plan and journal and never generates a replacement publication run. Exit code 4 (`WAITING_EXTERNAL`) fails the Maven goal rather than falsely reporting completion.

## Verified bootstrap and offline usage

The plugin downloads the **version-pinned** platform archive and release `SHA256SUMS` from the official GitHub release over HTTPS, verifies the archive SHA-256, rejects unexpected/missing archive members, validates embedded package metadata, and hashes all five executable files. It extracts to an isolated cache under `~/.m2/distroplane/cli/<version>/<os>-<arch>/sha256-<digest>/` with a per-version file lock and atomic publication. It rechecks the executable digests on every invocation, including offline use. A corrupt cached package **fails**; the plugin never silently re-downloads over it or changes the plan's bound providers.

For an independent archive trust pin, configure `<archiveSha256>...64 hex digits...</archiveSha256>` or `-Ddistroplane.archiveSha256=<hash>`. Checksums downloaded alongside a release detect download corruption but do not independently authenticate GitHub's release publisher. In Maven offline mode (`mvn -o ...`), no network fetch occurs: a valid previously cached package is required. Unknown OS/architecture pairs fail before invoking Distroplane. The plugin downloads no artifacts other than the verified CLI package and does not install globally.

## Multi-module and classified artifacts

The plugin never guesses which reactor artifact to publish. Build with `mvn package` first and opt into explicit reactor mappings only for artifacts already declared in `distroplane.json`:

```xml
<artifactMappings>
  <artifactMapping>
    <name>server</name>
    <module>com.example:server</module>
    <type>jar</type>
  </artifactMapping>
  <artifactMapping>
    <name>sources</name>
    <module>com.example:client</module>
    <classifier>sources</classifier>
    <type>jar</type>
  </artifactMapping>
</artifactMappings>
```

The reactor must contain **exactly one** matching produced artifact for every mapping. Missing, duplicate, symlinked, or unbuilt artifacts fail before publication. The original configuration is never overwritten; an explicit mapped configuration is atomically created under `.distroplane-maven/distroplane.json` and that same stable location is used on subsequent invocations. Keep that managed directory and its `.distroplane` run history for recovery. The plugin does not auto-build, guess a classifier, or create a destination from Maven metadata.

## Credential handling

No secret appears in CLI arguments. To opt into a credential source:

```xml
<credentialMappings><npm-publish>NPM_TOKEN</npm-publish></credentialMappings>
```

The plugin passes only explicitly mapped environment values to the spawned CLI, plus minimal operating-system process variables. It suppresses command output containing those secrets and does not persist them in the configuration. The CLI and providers remain the authority for credential resolution, review, journaling, publication, and reconciliation. Terminated or timed-out goals preserve existing release state; inspect and resume rather than restarting publication.

## Compatibility and external publication

Maven plugin integration tests cover Maven 3.9+ with JDK 17/21 on Linux,
macOS, and Windows native runners. They verify the six explicit goals,
immutable cache imports, offline execution, cancellation after durable state
creation, pending-to-resume handoff, duplicate-publication rejection, and
multi-module/classifier selection. The controlled test fixture **never publishes**
to a remote registry. Maven plugin version and CLI version are independently
versioned; CLI `0.9.2` is the current tested bootstrap target.

The Central Portal release profile (`-Pcentral-release`) prepares main,
source, JavaDoc, POM, and GPG-signed artifacts. The manual
`Stage Maven plugin in Central Portal` workflow requires an exact protected
`maven-plugin/vX.Y.Z` tag, verified namespace ownership for
`io.github.aalsanie`, GitHub environment `maven-central` approval, Central
Portal user token secrets, and a GPG signing key. It uploads a bundle for
**manual Central Portal approval** (`autoPublish=false`); the GitHub workflow
does not make artifacts public. Required repository secrets are:
`MAVEN_CENTRAL_TOKEN_USERNAME`, `MAVEN_CENTRAL_TOKEN_PASSWORD`,
`MAVEN_GPG_PRIVATE_KEY`, and `MAVEN_GPG_PASSPHRASE`.

Version `1.0.0` was signed, staged, and manually published through the Central
Portal. [The clean-consumer verification](https://github.com/aalsanie/distroplane/actions/runs/41023374518)
passed on Linux, macOS, and Windows with fresh Maven repositories and the
real native CLI bootstrap. [The npm example](examples/npm/README.md) uses
the published `1.0.0` version.
