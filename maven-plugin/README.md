# Distroplane Maven plugin

Published: [`io.github.aalsanie:distroplane-maven-plugin:1.0.1`](https://repo1.maven.org/maven2/io/github/aalsanie/distroplane-maven-plugin/1.0.1/). Requires JDK 17+ and Maven 3.9+. The default native CLI is `0.9.3`.

Add to your project's `pom.xml`:

```xml
<plugin>
  <groupId>io.github.aalsanie</groupId>
  <artifactId>distroplane-maven-plugin</artifactId>
  <version>1.0.1</version>
</plugin>
```

After building your release artifact and preparing `distroplane.json`:

```sh
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.1:validate
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.1:plan
mvn -Ddistroplane.approve=true io.github.aalsanie:distroplane-maven-plugin:1.0.1:publish
```

Review the plan before approving publication. Use `-Ddistroplane.confirmPlan=PLAN_ID` instead of `approve=true` to confirm a specific plan. Inspect pending releases with the `status` and `resume` goals. A pending native exit code 4 remains a failed Maven goal until the original run is reconciled.

For `init`, set `distroplane.destination`, `distroplane.release` and any destination-specific `initFields`; run the `init` goal explicitly.

## Artifact and credential mappings

For multi-module or classified artifacts, identify the exact produced Maven artifact:

```xml
<artifactMappings>
  <artifactMapping>
    <name>app</name>
    <module>com.example:server</module>
    <type>jar</type>
  </artifactMapping>
</artifactMappings>
<credentialMappings>
  <npm-publish>NPM_TOKEN</npm-publish>
</credentialMappings>
```

Build the Maven artifacts first. Mappings refer to names already declared in `distroplane.json`. Credentials are read from the specified environment variables for publication and resume.

The plugin verifies the official CLI archive checksum, package identity and embedded binary digests. Verified CLI packages are cached under `~/.m2/distroplane/cli/` and reverified on use. Maven `-o` requires a verified cached package; `archiveSha256` independently pins the archive digest. An explicit mapped configuration is stored in `.distroplane-maven/`; retain it and `.distroplane/` during recovery.

[Release recovery](../docs/recovery.md) · [Configuration](../docs/configuration.md)
