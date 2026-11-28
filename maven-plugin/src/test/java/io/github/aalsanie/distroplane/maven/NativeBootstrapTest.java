package io.github.aalsanie.distroplane.maven;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.apache.maven.plugin.logging.SystemStreamLog;
import org.apache.commons.compress.archivers.tar.TarArchiveEntry;
import org.apache.commons.compress.archivers.tar.TarArchiveOutputStream;
import org.apache.commons.compress.compressors.gzip.GzipCompressorOutputStream;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.List;
import java.util.Map;
import java.util.TreeMap;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.zip.ZipEntry;
import java.util.zip.ZipOutputStream;

import static org.junit.jupiter.api.Assertions.*;

final class NativeBootstrapTest {
    @TempDir Path temp;
    private static final String VERSION = "0.9.3";

    private static String sha(byte[] bytes) throws Exception {
        return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes));
    }

    private byte[] archive(CliBootstrap.Platform platform, byte[] executable) throws Exception {
        var members = new TreeMap<String, byte[]>();
        var records = new ArrayList<Map<String, Object>>();
        for (String member : platform.members()) {
            if (member.equals("PACKAGE-METADATA.json")) continue;
            members.put(member, executable);
            records.add(Map.of("path", member, "sha256", sha(executable), "size", executable.length, "mode", "0755"));
        }
        members.put("PACKAGE-METADATA.json", new ObjectMapper().writeValueAsBytes(Map.of(
            "schemaVersion", "1", "version", VERSION, "os", platform.os(), "arch", platform.arch(),
            "cgoEnabled", false, "files", records)));
        ByteArrayOutputStream output = new ByteArrayOutputStream();
        if (platform.os().equals("windows")) {
            try (ZipOutputStream zip = new ZipOutputStream(output)) {
                for (var entry : members.entrySet()) {
                    zip.putNextEntry(new ZipEntry(entry.getKey()));
                    zip.write(entry.getValue());
                    zip.closeEntry();
                }
            }
        } else {
            try (GzipCompressorOutputStream gzip = new GzipCompressorOutputStream(output);
                 TarArchiveOutputStream tar = new TarArchiveOutputStream(gzip)) {
                for (var entry : members.entrySet()) {
                    TarArchiveEntry member = new TarArchiveEntry(entry.getKey());
                    member.setSize(entry.getValue().length);
                    member.setMode(entry.getKey().equals("PACKAGE-METADATA.json") ? 0644 : 0755);
                    tar.putArchiveEntry(member);
                    tar.write(entry.getValue());
                    tar.closeArchiveEntry();
                }
            }
        }
        return output.toByteArray();
    }

    @Test void executesColdCacheHitAndOfflineVerifiedNativePackage() throws Exception {
        byte[] executable = Files.readAllBytes(NativeFixture.binary());
        var platform = CliBootstrap.Platform.current(System.getProperty("os.name"), System.getProperty("os.arch"));
        byte[] archive = archive(platform, executable);
        String hash = sha(archive);
        byte[] sums = (hash + "  " + platform.archive(VERSION) + "\n").getBytes(StandardCharsets.UTF_8);
        AtomicInteger manifestRequests = new AtomicInteger(), archiveRequests = new AtomicInteger();
        var bootstrap = new CliBootstrap((uri, max) -> {
            if (uri.toString().endsWith("/v" + VERSION + "/SHA256SUMS")) {
                manifestRequests.incrementAndGet();
                return sums;
            }
            if (uri.toString().endsWith("/v" + VERSION + "/" + platform.archive(VERSION))) {
                archiveRequests.incrementAndGet();
                return archive;
            }
            throw new IOException("Unexpected native package URL");
        });
        Path cache = temp.resolve("native cache with spaces");
        Path workspace = Files.createDirectory(temp.resolve("working directory with spaces"));
        Path cold = bootstrap.resolve(cache, VERSION, platform, false, hash);
        assertArrayEquals(executable, Files.readAllBytes(cold));
        assertEquals(1, manifestRequests.get());
        assertEquals(1, archiveRequests.get());
        assertEquals(cold, bootstrap.resolve(cache, VERSION, platform, false, hash));
        assertEquals(2, manifestRequests.get());
        assertEquals(1, archiveRequests.get());
        var offline = new CliBootstrap((uri, max) -> { throw new AssertionError("Offline download"); });
        Path cached = offline.resolve(cache, VERSION, platform, true, hash);
        assertEquals(cold, cached);
        assertEquals(cold, offline.resolve(cache, VERSION, platform, true, null));
        for (Path candidate : List.of(cold, cached)) {
            var result = CliRunner.run(candidate, workspace, List.of("fixture-process", "ok"),
                Map.of(), Duration.ofSeconds(10), new SystemStreamLog());
            assertEquals(0, result.exitCode());
            assertEquals("ok", result.stdout().strip());
        }
        assertThrows(IOException.class, () -> bootstrap.resolve(cache, VERSION, platform, false, "0".repeat(64)));
        assertThrows(IOException.class, () -> offline.resolve(temp.resolve("empty"), VERSION, platform, true, hash));
        Path packageRoot = cold.getParent().getParent();
        Path provider = packageRoot.resolve(platform.members().stream()
            .filter(name -> name.startsWith("libexec/") && name.contains("provider-npm")).findFirst().orElseThrow());
        Files.writeString(provider, "corrupt");
        assertThrows(IOException.class, () -> offline.resolve(cache, VERSION, platform, true, hash));
        assertFalse(Files.exists(workspace.resolve(".distroplane")));
        Files.write(provider, executable);
        Path metadata = packageRoot.resolve("PACKAGE-METADATA.json");
        Files.writeString(metadata, "{}");
        assertThrows(IOException.class, () -> offline.resolve(cache, VERSION, platform, true, hash));
    }
}
