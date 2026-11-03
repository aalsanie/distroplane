package io.github.aalsanie.distroplane.gradle;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.util.*;

/** Materializes explicit Gradle artifact mappings. */
final class ArtifactMapper {
    private static final ObjectMapper JSON = new ObjectMapper();
    record Selection(String name, String module, String classifier, String type) { }
    record Source(String module, String classifier, String type, Path path) { }

    static Path materialize(Path config, Path workingDir, List<Selection> mappings, List<Source> sources) throws IOException {
        if (mappings == null || mappings.isEmpty()) return config;
        if (!Files.isRegularFile(config, LinkOption.NOFOLLOW_LINKS) || Files.isSymbolicLink(config)) throw new IOException("Distroplane config is not a regular file");
        if (Files.size(config) > 4 << 20) throw new IOException("Distroplane configuration is too large");
        JsonNode tree = JSON.readTree(Files.readAllBytes(config));
        if (!(tree instanceof ObjectNode root)) throw new IOException("Distroplane config must be a JSON object");
        String schema = root.path("schemaVersion").asText();
        if (!schema.equals("1") && !schema.equals("2")) throw new IOException("Unsupported Distroplane config schema for Gradle mapping");
        JsonNode artifactContainer = root.path("release").path("artifacts");
        if (!(artifactContainer.isArray() && schema.equals("1")) && !(artifactContainer.isObject() && schema.equals("2")))
            throw new IOException("Distroplane release artifacts do not match configuration schema");
        // Rebase relative artifact and provider paths for the generated configuration.
        for (JsonNode entry : artifactContainer) {
            if (!(entry instanceof ObjectNode object)) throw new IOException("Malformed Distroplane artifact declaration");
            anchor(object, "source", config.getParent());
        }
        if (schema.equals("2")) {
            for (JsonNode provider : root.path("destinations")) {
                if (provider instanceof ObjectNode dest) rebaseDestination(dest, config.getParent());
                else if (provider.isArray()) for (JsonNode nested : provider) {
                    if (nested instanceof ObjectNode dest) rebaseDestination(dest, config.getParent());
                }
            }
        }
        Set<String> names = new HashSet<>();
        for (Selection mapping : mappings) {
            if (mapping == null || mapping.name() == null || !mapping.name().matches("[A-Za-z0-9][A-Za-z0-9._-]{0,127}") ||
                mapping.module() == null || mapping.module().isBlank() || !names.add(mapping.name()))
                throw new IOException("Invalid or duplicate Gradle artifact mapping");
            List<Source> matches = new ArrayList<>();
            for (Source source : sources) {
                if (source.module().equals(mapping.module()) && Objects.equals(norm(source.classifier()), norm(mapping.classifier())) &&
                    (norm(mapping.type()).isEmpty() || norm(mapping.type()).equals(source.type()))) matches.add(source);
            }
            if (matches.size() != 1) throw new IOException("Gradle artifact mapping " + mapping.name() + " is missing or ambiguous; specify module, classifier and type");
            Path file = matches.get(0).path();
            if (file == null || !Files.isRegularFile(file, LinkOption.NOFOLLOW_LINKS) || Files.isSymbolicLink(file))
                throw new IOException("Mapped Gradle artifact has not been packaged as a regular file: " + mapping.name());
            Path absolute = file.toRealPath();
            ObjectNode dest = null;
            if (schema.equals("2")) {
                JsonNode entry = artifactContainer.path(mapping.name());
                if (entry instanceof ObjectNode object) dest = object;
            } else {
                for (JsonNode entry : artifactContainer) {
                    if (mapping.name().equals(entry.path("name").asText())) {
                        if (dest != null) throw new IOException("Duplicate artifact name in config");
                        if (entry instanceof ObjectNode object) dest = object;
                    }
                }
            }
            if (dest == null) throw new IOException("Gradle artifact mapping refers to undeclared Distroplane artifact: " + mapping.name());
            dest.put("source", absolute.toString());
        }
        Path managedDir = workingDir.resolve(".distroplane-gradle");
        if (Files.exists(managedDir, LinkOption.NOFOLLOW_LINKS) &&
            (!Files.isDirectory(managedDir, LinkOption.NOFOLLOW_LINKS) || Files.isSymbolicLink(managedDir)))
            throw new IOException("Unsafe Gradle managed configuration directory");
        Files.createDirectories(managedDir);
        Path dest = managedDir.resolve("distroplane.json");
        if (Files.isSymbolicLink(dest)) throw new IOException("Unsafe generated Gradle configuration");
        byte[] bytes = JSON.writeValueAsBytes(root);
        Path stage = Files.createTempFile(managedDir, ".config-", ".tmp");
        try {
            Files.write(stage, bytes);
            Files.move(stage, dest, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
        } finally { Files.deleteIfExists(stage); }
        return dest;
    }
    private static void anchor(ObjectNode node, String field, Path base) {
        JsonNode value = node.get(field);
        if (value != null && value.isTextual() && !value.asText().isBlank()) {
            Path path = Path.of(value.asText());
            if (!path.isAbsolute()) node.put(field, base.resolve(path).normalize().toString());
        }
    }
    private static void rebaseDestination(ObjectNode destination, Path base) {
        anchor(destination, "executable", base);
        if (!(destination.path("options") instanceof ObjectNode options)) return;
        anchor(options, "packagePath", base);
        if (options.path("installer") instanceof ObjectNode installer) anchor(installer, "archivePath", base);
    }
    private static String norm(String s) { return s == null ? "" : s; }

}
