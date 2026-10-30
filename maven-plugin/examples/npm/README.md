# Maven release plugin consumer example

The plugin is a Maven build/release plugin, not a runtime dependency. It publishes
**already built** artifacts through the Distroplane CLI; it does not create a
package, host an archive, or automatically publish during `mvn package`.

The sample POM deliberately requires an actual published plugin version:

1. Verify that `io.github.aalsanie:distroplane-maven-plugin:<version>` has been
   published to Maven Central. A local `1.0.0-SNAPSHOT` checkout is not a public
   artifact.
2. Build and test your real npm package, then create `package.tgz` in this
   directory with `npm pack`. Update the release ID, artifact, and npm destination
   to match your own package. Do not use the sample against somebody else's npm
   package or publish with placeholder metadata.
3. Run `mvn -Ddistroplane.plugin.version=<published-version> distroplane:validate`,
   followed by `distroplane:plan`. Review the resulting plan before publication.
4. Provide a scoped registry token through the `NPM_TOKEN` environment variable;
   The example POM already maps `npm-publish=NPM_TOKEN`.
   Then explicitly run `mvn -Ddistroplane.plugin.version=<published-version>
   -Ddistroplane.approve=true distroplane:publish`.
5. A pending/rejected result fails the Maven goal and leaves the run for
   `distroplane:status` or `distroplane:resume`. Never delete `.distroplane` to
   retry an unknown publication.

Before the public Central release is confirmed, all commands above are examples
of the intended consumer invocation, not evidence of published coordinates.
