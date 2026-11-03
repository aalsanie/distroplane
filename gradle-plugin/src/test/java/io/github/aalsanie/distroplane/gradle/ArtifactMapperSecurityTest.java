package io.github.aalsanie.distroplane.gradle;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.*;

final class ArtifactMapperSecurityTest {
    @TempDir Path temp;
    static final ObjectMapper JSON = new ObjectMapper();

    Path write(String name, String content) throws IOException {
        Path file = temp.resolve(name);
        Files.writeString(file, content);
        return file;
    }
    static ArtifactMapper.Selection selection(String name) {
        return new ArtifactMapper.Selection(name, ":app:jar", "", "");
    }
    static ArtifactMapper.Source source(Path path) {
        return new ArtifactMapper.Source(":app:jar", "", "", path);
    }
    static String schema2() {
        return """
            {"schemaVersion":"2","release":{"id":"test","artifacts":{"app":{"source":"release.zip"},"other":{"source":"relative.tgz"}}},
             "destinations":{"npm":{"executable":"bin/provider","options":{"packagePath":"release.zip",
             "installer":{"archivePath":"installer.zip"}}},"groups":[{"executable":"bin/other"}]}}
            """;
    }

    @Test void rebasesUnmappedArtifactsAndProviderPathsInSchema2() throws Exception {
        Path built = write("built.jar", "contents");
        Path cfg = write("config.json", schema2());
        Path mapped = ArtifactMapper.materialize(cfg, temp, List.of(selection("app")), List.of(source(built)));
        JsonNode root = JSON.readTree(Files.readAllBytes(mapped));
        assertEquals(built.toRealPath().toString(), root.path("release").path("artifacts").path("app").path("source").asText());
        assertEquals(temp.resolve("relative.tgz").toString(), root.path("release").path("artifacts").path("other").path("source").asText());
        assertEquals(temp.resolve("bin/provider").toString(), root.path("destinations").path("npm").path("executable").asText());
        assertEquals(temp.resolve("release.zip").toString(), root.path("destinations").path("npm").path("options").path("packagePath").asText());
        assertEquals(temp.resolve("installer.zip").toString(), root.path("destinations").path("npm").path("options").path("installer").path("archivePath").asText());
        assertEquals(temp.resolve("bin/other").toString(), root.path("destinations").path("groups").get(0).path("executable").asText());
        assertEquals(schema2(), Files.readString(cfg));
        assertEquals(mapped, ArtifactMapper.materialize(cfg, temp, List.of(selection("app")), List.of(source(built))));
    }

    @Test void refusesMissingAmbiguousDuplicateAndUnbuiltArtifacts() throws Exception {
        Path cfg = write("config.json", schema2());
        Path file = write("built.jar", "ok");
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
            List.of(selection("app")), List.of()));
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
            List.of(selection("app")), List.of(source(file), source(file))));
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
            List.of(selection("app"), selection("app")), List.of(source(file))));
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
            List.of(new ArtifactMapper.Selection("missing", ":app:jar", "", "")), List.of(source(file))));
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
            List.of(new ArtifactMapper.Selection("?", ":app:jar", "", "")), List.of(source(file))));
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
            List.of(new ArtifactMapper.Selection("app", "", "", "")), List.of(source(file))));
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
            List.of(selection("app")), List.of(source(temp.resolve("notbuilt.jar")))));
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
            List.of(selection("app")), List.of(new ArtifactMapper.Source(":app:jar", "", "", null))));
        assertEquals(cfg, ArtifactMapper.materialize(cfg, temp, List.of(), List.of(source(file))));
    }

    @Test void rejectsUnsupportedAndMalformedConfigurationSchema() throws Exception {
        Path artifact = write("file.jar", "built");
        String[] invalid = {
            "{}", "[]", "{", "{\"schemaVersion\":\"3\",\"release\":{\"artifacts\":{}}}",
            "{\"schemaVersion\":\"2\",\"release\":{\"artifacts\":[]}}",
            "{\"schemaVersion\":\"1\",\"release\":{\"artifacts\":{}}}",
            "{\"schemaVersion\":\"2\",\"release\":{\"artifacts\":{\"app\":\"bad\"}}}",
            "{\"schemaVersion\":\"1\",\"release\":{\"artifacts\":[\"bad\"]}}"
        };
        for (int i = 0; i < invalid.length; i++) {
            Path cfg = write("bad" + i + ".json", invalid[i]);
            assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
                List.of(selection("app")), List.of(source(artifact))));
        }
        Path large = temp.resolve("large.json");
        Files.write(large, new byte[4 * 1024 * 1024 + 1]);
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(large, temp,
            List.of(selection("app")), List.of(source(artifact))));
    }

    @Test void mapsSchema1AndRejectsUnmarkedOrRepeatedArtifactNames() throws Exception {
        Path file = write("built.jar", "data");
        Path cfg = write("config.json", """
            {"schemaVersion":"1","release":{"artifacts":[{"name":"app","source":"original.jar"},
            {"name":"other","source":"other.jar"}]},"targets":[]}
            """);
        JsonNode tree = JSON.readTree(Files.readAllBytes(ArtifactMapper.materialize(cfg, temp,
            List.of(selection("app")), List.of(source(file)))));
        assertEquals(file.toRealPath().toString(), tree.path("release").path("artifacts").get(0).path("source").asText());
        assertEquals(temp.resolve("other.jar").toString(), tree.path("release").path("artifacts").get(1).path("source").asText());
        Files.writeString(cfg, """
            {"schemaVersion":"1","release":{"artifacts":[{"name":"app","source":"a"},
            {"name":"app","source":"b"}]},"targets":[]}
            """);
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
            List.of(selection("app")), List.of(source(file))));
    }

    @Test void protectsSourceConfigArchiveAndManagedOutputFromSymlinks() throws Exception {
        Path file = write("built.jar", "data");
        Path cfg = write("config.json", schema2());
        if (System.getProperty("os.name").toLowerCase().contains("win")) return;
        Path link = temp.resolve("link.jar");
        Files.createSymbolicLink(link, file);
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
            List.of(selection("app")), List.of(source(link))));
        Path configLink = temp.resolve("config-link.json");
        Files.createSymbolicLink(configLink, cfg);
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(configLink, temp,
            List.of(selection("app")), List.of(source(file))));
        Path managed = temp.resolve(".distroplane-gradle");
        Files.createSymbolicLink(managed, temp);
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
            List.of(selection("app")), List.of(source(file))));
        Files.delete(managed);
        Files.createDirectories(managed);
        Files.createSymbolicLink(managed.resolve("distroplane.json"), cfg);
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
            List.of(selection("app")), List.of(source(file))));
    }

    @Test void rejectsRegularFileWhereManagedConfigurationDirectoryIsExpected() throws Exception {
        Path file = write("built.jar", "data");
        Path cfg = write("config.json", schema2());
        write(".distroplane-gradle", "not a directory");
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, temp,
            List.of(selection("app")), List.of(source(file))));
    }
}
