package io.github.aalsanie.distroplane.maven;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.*;

final class ArtifactMapperTest {
    @TempDir Path root;
    private final ObjectMapper json = new ObjectMapper();
    @Test void mapsTwoReactorModulesAndClassifierExactly() throws Exception {
        Path first = root.resolve("alpha.jar"); Path second = root.resolve("beta-sources.jar");
        Files.writeString(first, "alpha"); Files.writeString(second, "beta");
        Path cfg = root.resolve("distroplane.json");
        Files.writeString(cfg, """
            {"schemaVersion":"2","release":{"id":"test","artifacts":{"a":{"source":"unmapped"},"b":{"source":"unmapped"}}},"destinations":{}}
            """);
        Path produced = ArtifactMapper.materialize(cfg, root,
            List.of(new ArtifactMapper.Selection("a", "g:alpha", null, "jar"),
                    new ArtifactMapper.Selection("b", "g:beta", "sources", "jar")),
            List.of(new ArtifactMapper.Source("g:alpha", "", "jar", first),
                    new ArtifactMapper.Source("g:beta", "sources", "jar", second)));
        JsonNode document = json.readTree(Files.readAllBytes(produced));
        assertEquals(first.toRealPath().toString(), document.path("release").path("artifacts").path("a").path("source").asText());
        assertEquals(second.toRealPath().toString(), document.path("release").path("artifacts").path("b").path("source").asText());
        assertEquals("unmapped", json.readTree(Files.readAllBytes(cfg)).path("release").path("artifacts").path("a").path("source").asText());
        assertEquals(produced, ArtifactMapper.materialize(cfg, root,
            List.of(new ArtifactMapper.Selection("a", "g:alpha", null, "jar"),
                    new ArtifactMapper.Selection("b", "g:beta", "sources", "jar")),
            List.of(new ArtifactMapper.Source("g:alpha", "", "jar", first),
                    new ArtifactMapper.Source("g:beta", "sources", "jar", second))));
    }
    @Test void failsClosedForAmbiguousAndMissingMappings() throws Exception {
        Path file = root.resolve("file.jar"); Files.writeString(file, "data");
        Path cfg = root.resolve("distroplane.json");
        Files.writeString(cfg, """
            {"schemaVersion":"1","release":{"id":"r","artifacts":[{"name":"a","source":"x"}]},"targets":[]}
            """);
        var selection = List.of(new ArtifactMapper.Selection("a", "group:child", "", "jar"));
        var source = new ArtifactMapper.Source("group:child", "", "jar", file);
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, root, selection, List.of(source, source)));
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, root, selection, List.of()));
        assertThrows(IOException.class, () -> ArtifactMapper.materialize(cfg, root,
            List.of(new ArtifactMapper.Selection("notdeclared", "group:child", "", "jar")), List.of(source)));
        Path okay = ArtifactMapper.materialize(cfg, root, selection, List.of(source));
        assertEquals(file.toRealPath().toString(), json.readTree(Files.readAllBytes(okay)).path("release").path("artifacts").get(0).path("source").asText());
    }
}
