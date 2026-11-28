# Distroplane Maven plugin

Run Distroplane from Maven 3.9+ with JDK 17+. Publication is opt-in and never attached to the normal build lifecycle.

Add the plugin to `<build><plugins>`:

```xml
<plugin>
  <groupId>io.github.aalsanie</groupId>
  <artifactId>distroplane-maven-plugin</artifactId>
  <version>1.0.1</version>
  <configuration>
    <cliVersion>0.9.3</cliVersion>
    <config>distroplane.json</config>
  </configuration>
</plugin>
```

## Usage

Build your artifacts and review the release:

```sh
mvn package
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.1:validate
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.1:plan
```

Approve publication explicitly with `-Ddistroplane.approve=true` or `-Ddistroplane.confirmPlan=<exact-id>`:

```sh
mvn -Ddistroplane.approve=true io.github.aalsanie:distroplane-maven-plugin:1.0.1:publish
```

Inspect or continue the original run:

```sh
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.1:status
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.1:resume
```

Pending releases fail the goal with CLI exit code 4. After a timeout or interruption, inspect the original run before resuming; do not start another publication.

The `init` goal requires explicit destination, release, artifact and required metadata (use `<initFields>` for nonsecret provider options). The source also provides `doctor`, not yet available in the published plugin.

## CLI installation

The plugin verifies the release archive, package metadata and executable hashes before caching the CLI and providers in `~/.m2/distroplane/cli/<version>/<os>-<arch>/sha256-<digest>/`. Cached files are verified on every invocation; corruption fails closed. Maven `-o` requires an existing verified cache.

Set `archiveSha256` or `-Ddistroplane.archiveSha256=<hash>` to an independently obtained archive digest to pin the download. Release-hosted checksums alone do not authenticate the publisher.

## Multi-module and classified artifacts

Map declared Distroplane artifact names to exact, already packaged reactor artifacts:

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

Each mapping must match exactly one regular artifact file. Missing, ambiguous, symlinked or unbuilt artifacts fail before publication. The plugin writes a rebased configuration to `.distroplane-maven/distroplane.json` without modifying the original. Preserve this directory and the release state for recovery.

## Credentials

```xml
<credentialMappings><npm-publish>NPM_TOKEN</npm-publish></credentialMappings>
```

Only mapped credentials are passed to the CLI. Output containing their values is suppressed.

See the [npm example](examples/npm/README.md).

## Plugin publication

The `central-release` profile signs and stages artifacts for manual Central Portal approval. The [staging workflow](../.github/workflows/maven-central-stage.yml) requires a protected `maven-plugin/vX.Y.Z` tag, an approved `maven-central` environment, namespace ownership and these secrets:

- `MAVEN_CENTRAL_TOKEN_USERNAME`
- `MAVEN_CENTRAL_TOKEN_PASSWORD`
- `MAVEN_GPG_PRIVATE_KEY`
- `MAVEN_GPG_PASSPHRASE`

After publication, run the [clean consumer workflow](../.github/workflows/maven-central-consumer.yml) for the released version.
