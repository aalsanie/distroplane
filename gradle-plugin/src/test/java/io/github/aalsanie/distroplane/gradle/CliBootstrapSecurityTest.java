package io.github.aalsanie.distroplane.gradle;

import com.fasterxml.jackson.databind.ObjectMapper;
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
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.TreeMap;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.function.Consumer;
import java.util.zip.ZipEntry;
import java.util.zip.ZipOutputStream;

import static org.junit.jupiter.api.Assertions.*;

final class CliBootstrapSecurityTest {
    @TempDir Path temp;
    static final ObjectMapper JSON = new ObjectMapper();
    static final String VERSION = "0.9.3";
    static final CliBootstrap.Platform WINDOWS = new CliBootstrap.Platform("windows", "amd64");
    static final CliBootstrap.Platform LINUX = new CliBootstrap.Platform("linux", "amd64");

    record Pack(CliBootstrap.Platform platform, byte[] archive, byte[] sums, String hash) { }
    record Member(String name, byte[] bytes, int mode, boolean directory) { }

    static String sha(byte[] bytes) throws Exception {
        return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes));
    }

    Pack pack(CliBootstrap.Platform p, Consumer<Map<String,Object>> mutateMetadata,
              String extra, String corruptMember, boolean wrongMode, boolean directory) throws Exception {
        Map<String,byte[]> members = new TreeMap<>();
        List<Map<String,Object>> records = new ArrayList<>();
        for (String name : p.members()) {
            if (name.equals("PACKAGE-METADATA.json")) continue;
            byte[] payload = ("fixture-" + name).getBytes(StandardCharsets.UTF_8);
            members.put(name, payload);
            records.add(new LinkedHashMap<>(Map.of("path", name, "sha256", sha(payload), "size", payload.length, "mode", "0755")));
        }
        Map<String,Object> metadata = new LinkedHashMap<>();
        metadata.put("schemaVersion", "1");
        metadata.put("version", VERSION);
        metadata.put("os", p.os());
        metadata.put("arch", p.arch());
        metadata.put("cgoEnabled", false);
        metadata.put("files", records);
        mutateMetadata.accept(metadata);
        members.put("PACKAGE-METADATA.json", JSON.writeValueAsBytes(metadata));
        if (extra != null) members.put(extra, "extra".getBytes(StandardCharsets.UTF_8));
        if (corruptMember != null) members.put(corruptMember, "bad".getBytes(StandardCharsets.UTF_8));
        ByteArrayOutputStream output = new ByteArrayOutputStream();
        if (p.os().equals("windows")) {
            try (ZipOutputStream zip = new ZipOutputStream(output)) {
                for (var item : members.entrySet()) {
                    zip.putNextEntry(new ZipEntry(item.getKey()));
                    zip.write(item.getValue());
                    zip.closeEntry();
                }
            }
        } else {
            try (GzipCompressorOutputStream gzip = new GzipCompressorOutputStream(output);
                 TarArchiveOutputStream tar = new TarArchiveOutputStream(gzip)) {
                for (var item : members.entrySet()) {
                    TarArchiveEntry entry = new TarArchiveEntry(item.getKey());
                    entry.setMode(wrongMode && !item.getKey().equals("PACKAGE-METADATA.json") ? 0644 :
                        item.getKey().equals("PACKAGE-METADATA.json") ? 0644 : 0755);
                    entry.setSize(item.getValue().length);
                    tar.putArchiveEntry(entry);
                    tar.write(item.getValue());
                    tar.closeArchiveEntry();
                }
                if (directory) {
                    TarArchiveEntry entry = new TarArchiveEntry("unexpected/");
                    tar.putArchiveEntry(entry);
                    tar.closeArchiveEntry();
                }
            }
        }
        byte[] archive = output.toByteArray();
        String hash = sha(archive);
        return new Pack(p, archive, (hash + "  " + p.archive(VERSION) + "\n").getBytes(StandardCharsets.UTF_8), hash);
    }

    CliBootstrap bootstrap(Pack p) {
        return new CliBootstrap((uri, max) -> uri.toString().endsWith("SHA256SUMS") ? p.sums() : p.archive());
    }

    @Test void extractsLinuxTarAndVerifiesEveryOfflineExecutable() throws Exception {
        if (System.getProperty("os.name").toLowerCase().contains("win")) return;
        Pack p = pack(LINUX, meta -> {}, null, null, false, false);
        Path cli = bootstrap(p).resolve(temp, VERSION, LINUX, false, null);
        assertTrue(Files.isExecutable(cli));
        assertEquals(cli, new CliBootstrap((uri,max) -> { throw new AssertionError("Network on offline cache"); })
            .resolve(temp, VERSION, LINUX, true, p.hash()));
        Path parent = cli.getParent().getParent();
        Files.writeString(parent.resolve("libexec/distroplane-provider-npm"), "tampered");
        IOException bad = assertThrows(IOException.class,
            () -> bootstrap(p).resolve(temp, VERSION, LINUX, true, null));
        assertTrue(bad.getMessage().contains("corrupt"));
    }

    @Test void rejectsUnsafeTarEntryModesAndDirectoryTypes() throws Exception {
        if (System.getProperty("os.name").toLowerCase().contains("win")) return;
        assertThrows(IOException.class, () -> bootstrap(pack(LINUX, m -> {}, null, null, true, false))
            .resolve(temp.resolve("mode"), VERSION, LINUX, false, null));
        assertThrows(IOException.class, () -> bootstrap(pack(LINUX, m -> {}, null, null, false, true))
            .resolve(temp.resolve("directory"), VERSION, LINUX, false, null));
        assertThrows(IOException.class, () -> bootstrap(pack(LINUX, m -> {}, "unexpected", null, false, false))
            .resolve(temp.resolve("extra"), VERSION, LINUX, false, null));
    }

    @Test void rejectsAllPackageIdentityMismatchFields() throws Exception {
        List<Consumer<Map<String,Object>>> mutations = List.of(
            m -> m.put("schemaVersion", "2"), m -> m.put("version", "0.9.2"),
            m -> m.put("os", "darwin"), m -> m.put("arch", "arm64"),
            m -> m.put("cgoEnabled", true), m -> m.put("files", List.of()),
            m -> m.put("files", "invalid")
        );
        for (int i = 0; i < mutations.size(); i++) {
            Pack p = pack(WINDOWS, mutations.get(i), null, null, false, false);
            Path destination = temp.resolve("identity" + i);
            assertThrows(IOException.class, () -> bootstrap(p).resolve(destination,
                VERSION, WINDOWS, false, null));
        }
    }

    @SuppressWarnings("unchecked")
    @Test void rejectsMalformedEmbeddedExecutableRecords() throws Exception {
        List<Consumer<Map<String,Object>>> mutations = List.of(
            m -> ((List<Map<String,Object>>)m.get("files")).get(0).put("path", "PACKAGE-METADATA.json"),
            m -> ((List<Map<String,Object>>)m.get("files")).get(0).put("path", "libexec/unknown"),
            m -> ((List<Map<String,Object>>)m.get("files")).get(0).put("mode", "0644"),
            m -> ((List<Map<String,Object>>)m.get("files")).get(0).put("sha256", "bad"),
            m -> ((List<Map<String,Object>>)m.get("files")).get(0).put("sha256", "0".repeat(64)),
            m -> ((List<Map<String,Object>>)m.get("files")).get(0).put("size", -1),
            m -> ((List<Map<String,Object>>)m.get("files")).get(0).put("path",
                ((List<Map<String,Object>>)m.get("files")).get(1).get("path"))
        );
        for (int i = 0; i < mutations.size(); i++) {
            Pack p = pack(WINDOWS, mutations.get(i), null, null, false, false);
            Path destination = temp.resolve("record" + i);
            assertThrows(IOException.class, () -> bootstrap(p).resolve(destination,
                VERSION, WINDOWS, false, null));
        }
    }

    @Test void validatesOfflineCacheMarkersAndUnknownMembers() throws Exception {
        Pack p = pack(WINDOWS, m -> {}, null, null, false, false);
        Path cli = bootstrap(p).resolve(temp, VERSION, WINDOWS, false, null);
        Path root = cli.getParent().getParent();
        Files.writeString(root.resolve("archive.sha256"), "invalid");
        assertThrows(IOException.class, () -> bootstrap(p).resolve(temp, VERSION, WINDOWS, true, p.hash()));
        Files.writeString(root.resolve("archive.sha256"), p.hash());
        Files.writeString(root.resolve("extra.txt"), "unexpected");
        assertThrows(IOException.class, () -> bootstrap(p).resolve(temp, VERSION, WINDOWS, true, p.hash()));
        Files.delete(root.resolve("extra.txt"));
        Files.delete(root.resolve("libexec/distroplane-provider-npm.exe"));
        assertThrows(IOException.class, () -> bootstrap(p).resolve(temp, VERSION, WINDOWS, true, p.hash()));
    }

    @Test void validatesChecksumsAndPinnedArchiveMetadata() throws Exception {
        Pack p = pack(WINDOWS, m -> {}, null, null, false, false);
        List<byte[]> invalid = List.of(
            ("0".repeat(64) + "  wrong.zip\n").getBytes(StandardCharsets.UTF_8),
            ("bad line\n").getBytes(StandardCharsets.UTF_8),
            (new String(p.sums(), StandardCharsets.UTF_8) + new String(p.sums(), StandardCharsets.UTF_8)).getBytes(StandardCharsets.UTF_8),
            new byte[1024 * 1024 + 1]
        );
        for (int i=0;i<invalid.size();i++) {
            byte[] data = invalid.get(i);
            CliBootstrap client = new CliBootstrap((uri,max) -> uri.toString().endsWith("SHA256SUMS") ? data : p.archive());
            Path destination = temp.resolve("checksums" + i);
            assertThrows(IOException.class, () -> client.resolve(destination, VERSION, WINDOWS, false, null));
        }
        assertThrows(IOException.class, () -> bootstrap(p).resolve(temp.resolve("bad-pin"), VERSION, WINDOWS, true, "not-a-sha"));
        assertThrows(IOException.class, () -> bootstrap(p).resolve(temp.resolve("pin-mismatch"), VERSION, WINDOWS, false, "0".repeat(64)));
    }

    @Test void offlineUnpinnedCacheDiscoveryRejectsAmbiguityAndSymlinks() throws Exception {
        Pack p = pack(WINDOWS, m -> {}, null, null, false, false);
        Path cli = bootstrap(p).resolve(temp, VERSION, WINDOWS, false, null);
        assertEquals(cli, bootstrap(p).resolve(temp, VERSION, WINDOWS, true, null));
        Path platform = temp.resolve(VERSION).resolve("windows-amd64");
        Files.createDirectory(platform.resolve("sha256-" + "0".repeat(64)));
        assertThrows(IOException.class, () -> bootstrap(p).resolve(temp, VERSION, WINDOWS, true, null));
        Files.delete(platform.resolve("sha256-" + "0".repeat(64)));
        Files.createDirectory(platform.resolve("sha256-invalid"));
        assertThrows(IOException.class, () -> bootstrap(p).resolve(temp, VERSION, WINDOWS, true, null));
        Files.delete(platform.resolve("sha256-invalid"));
        Path lock = platform.resolve(".bootstrap.lock");
        Files.delete(lock);
        if (!System.getProperty("os.name").toLowerCase().contains("win")) {
            Files.createSymbolicLink(lock, cli);
            assertThrows(IOException.class, () -> bootstrap(p).resolve(temp, VERSION, WINDOWS, true, p.hash()));
        }
    }

    @Test void rejectsUnsupportedPlatformAliasesAndUnsafeCacheRoots() throws Exception {
        assertEquals(new CliBootstrap.Platform("windows", "amd64"), CliBootstrap.Platform.current("Windows 11", "x64"));
        assertEquals(new CliBootstrap.Platform("darwin", "arm64"), CliBootstrap.Platform.current("Mac OS", "aarch64"));
        assertEquals(new CliBootstrap.Platform("linux", "amd64"), CliBootstrap.Platform.current("Linux", "x86_64"));
        assertThrows(IOException.class, () -> CliBootstrap.Platform.current("Haiku", "amd64"));
        assertThrows(IOException.class, () -> CliBootstrap.Platform.current("Linux", "riscv64"));
        assertThrows(IOException.class, () -> bootstrap(pack(WINDOWS, m -> {}, null, null, false, false))
            .resolve(temp.resolve("invalid"), "2.0.0", WINDOWS, false, null));
        if (!System.getProperty("os.name").toLowerCase().contains("win")) {
            Path malicious = temp.resolve("redirect");
            Files.createSymbolicLink(malicious, temp);
            assertThrows(IOException.class, () -> bootstrap(pack(WINDOWS, m -> {}, null, null, false, false))
                .resolve(malicious, VERSION, WINDOWS, false, null));
        }
    }
}
