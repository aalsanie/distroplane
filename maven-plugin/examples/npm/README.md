# Maven release plugin consumer example

Publish an existing npm tarball using the [Maven plugin](../../README.md).

1. Run `npm pack` in your tested npm project and copy the archive to
   `package.tgz` beside this POM. Replace the placeholder release ID and
   destination with your own.
2. Run `mvn io.github.aalsanie:distroplane-maven-plugin:1.0.1:validate`,
   then `mvn io.github.aalsanie:distroplane-maven-plugin:1.0.1:plan`.
   Review the plan before publication.
3. Supply a scoped registry token using the `NPM_TOKEN` environment variable.
   The example POM maps `npm-publish=NPM_TOKEN`. Publish only after review:
   `mvn -Ddistroplane.approve=true io.github.aalsanie:distroplane-maven-plugin:1.0.1:publish`.
4. If the result is pending, inspect the existing run with
   `mvn io.github.aalsanie:distroplane-maven-plugin:1.0.1:status` and
   `mvn io.github.aalsanie:distroplane-maven-plugin:1.0.1:resume` when safe.
   Never delete `.distroplane` to retry an unknown publication.
