# Distroplane Maven plugin

A Maven **build plugin** which runs the same native Distroplane CLI and providers as the regular release distribution. This plugin does not build artifacts, publish them directly, or attach any goal to the Maven lifecycle. Maven Central publication and externally consumable coordinates remain pending until the separate release work is finished.

Supported baseline: **JDK 17+**, **Maven 3.9+**, and Distroplane **CLI 0.9.2**. The implementation uses `io.github.aalsanie:distroplane-maven-plugin:1.0.0-SNAPSHOT` for local development only. These coordinates are not an assertion of public Maven Central availability.

For a locally installed development plugin, add the following to `<build><plugins>`:

```xml
<plugin>
  <groupId>io.github.aalsanie</groupId>
  <artifactId>distroplane-maven-plugin</artifactId>
  <version>1.0.0-SNAPSHOT</version>
  <configuration>
    <cliVersion>0.9.2</cliVersion>
    <config>distroplane.json</config>
  </configuration>
</plugin>
```

Run `mvn -f maven-plugin/pom.xml install` from the Distroplane source checkout, then in a project with a prebuilt release artifact:

```sh
mvn package
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0-SNAPSHOT:validate
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0-SNAPSHOT:plan
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0-SNAPSHOT:publish -Ddistroplane.approve=true
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0-SNAPSHOT:status
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0-SNAPSHOT:resume
```

`init` requires a selected provider, release, artifact and provider-specific nonsecret metadata (configure `<initFields><field>value</field></initFields>` for the native `--set` options): `mvn distroplane:init -Ddistroplane.destination=npm -Ddistroplane.release=v1`. No prompts or secret values are supported for Maven `init`; missing metadata fails without generating a guessed configuration. `publish` refuses to execute unless either `-Ddistroplane.approve=true` or `-Ddistroplane.confirmPlan=<exact-id>` is supplied. `resume` uses the original plan and journal and never generates a replacement publication run. Exit code 4 (`WAITING_EXTERNAL`) fails the Maven goal rather than falsely reporting completion.

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
