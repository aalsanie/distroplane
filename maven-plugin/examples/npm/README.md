# Maven release plugin consumer example

The published `io.github.aalsanie:distroplane-maven-plugin:1.0.0` is a Maven
build/release plugin, not a runtime dependency. It distributes **already built**
artifacts through the native Distroplane CLI and does not publish during
`mvn package`.

1. Build and test your real npm package. Run `npm pack`, then copy or rename
   the generated archive to `package.tgz` beside this POM. Update the
   release ID, artifact, and destination to match your package. Do not
   publish the example's placeholder identity.
2. Run `mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0:validate`,
   then `mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0:plan`.
   Review the plan before publication.
3. Supply a scoped registry token using the `NPM_TOKEN` environment variable.
   The example POM maps `npm-publish=NPM_TOKEN`. Publish only after review:
   `mvn -Ddistroplane.approve=true io.github.aalsanie:distroplane-maven-plugin:1.0.0:publish`.
4. If the result is pending, inspect the existing run with
   `mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0:status` and
   `mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0:resume` when safe.
   Never delete `.distroplane` to retry an unknown publication.
