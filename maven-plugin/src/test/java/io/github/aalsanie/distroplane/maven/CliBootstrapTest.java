package io.github.aalsanie.distroplane.maven;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.MessageDigest;
import java.util.*;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.zip.ZipEntry;
import java.util.zip.ZipOutputStream;

import static org.junit.jupiter.api.Assertions.*;

final class CliBootstrapTest {
    @TempDir Path temp;
    private static final CliBootstrap.Platform WINDOWS = new CliBootstrap.Platform("windows", "amd64");
    private static final String VERSION = "0.9.2";
    private static final ObjectMapper JSON = new ObjectMapper();

    private static String hash(byte[] bytes) {
        try { return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes)); }
        catch (Exception e) { throw new AssertionError(e); }
    }
    private record Fixture(byte[] archive, byte[] sums, String hash) { }
    private Fixture fixture(boolean tamperMetadata, String extra) throws Exception {
        Map<String,byte[]> contents = new TreeMap<>();
        for (String name : WINDOWS.members()) if (!name.equals("PACKAGE-METADATA.json")) contents.put(name, ("binary-" + name).getBytes(StandardCharsets.UTF_8));
        List<Map<String,Object>> records = new ArrayList<>();
        for (var entry : contents.entrySet()) records.add(Map.of("path",entry.getKey(),"sha256",hash(entry.getValue()),"size",entry.getValue().length,"mode","0755"));
        Map<String,Object> metadata = new LinkedHashMap<>();
        metadata.put("schemaVersion", "1"); metadata.put("version", tamperMetadata ? "0.9.1" : VERSION);
        metadata.put("os", "windows"); metadata.put("arch", "amd64"); metadata.put("cgoEnabled", false); metadata.put("files", records);
        contents.put("PACKAGE-METADATA.json", JSON.writeValueAsBytes(metadata));
        if (extra != null) contents.put(extra, "INJECTION".getBytes(StandardCharsets.UTF_8));
        ByteArrayOutputStream data = new ByteArrayOutputStream();
        try (ZipOutputStream zip = new ZipOutputStream(data)) {
            for (var entry : contents.entrySet()) {
                zip.putNextEntry(new ZipEntry(entry.getKey())); zip.write(entry.getValue()); zip.closeEntry();
            }
        }
        byte[] archive = data.toByteArray();
        return new Fixture(archive, (hash(archive) + "  " + WINDOWS.archive(VERSION) + "\n").getBytes(StandardCharsets.UTF_8), hash(archive));
    }
    private CliBootstrap bootstrap(Fixture f, AtomicInteger downloads) {
        return new CliBootstrap((uri, max) -> {
            downloads.incrementAndGet();
            if (uri.toString().endsWith("SHA256SUMS")) return f.sums;
            if (uri.toString().endsWith(WINDOWS.archive(VERSION))) return f.archive;
            throw new IOException("Unexpected release download");
        });
    }
    @Test void verifiesBootstrapCacheAndOfflineWithoutNetwork() throws Exception {
        Fixture f = fixture(false, null); AtomicInteger calls = new AtomicInteger();
        Path cli = bootstrap(f, calls).resolve(temp, VERSION, WINDOWS, false, f.hash);
        assertTrue(Files.isRegularFile(cli));
        assertEquals(2, calls.get());
        Path cached = new CliBootstrap((uri, max) -> { throw new AssertionError("Offline made an HTTP request"); })
            .resolve(temp, VERSION, WINDOWS, true, f.hash);
        assertEquals(cli, cached);
        assertEquals(2, calls.get());
        Files.writeString(cli, "tampered");
        IOException error = assertThrows(IOException.class, () -> bootstrap(f, calls).resolve(temp, VERSION, WINDOWS, true, f.hash));
        assertTrue(error.getMessage().contains("corrupt"));
    }
    @Test void rejectsTamperedArchiveAndWrongPin() throws Exception {
        Fixture f = fixture(false, null);
        assertThrows(IOException.class, () -> bootstrap(f, new AtomicInteger()).resolve(temp, VERSION, WINDOWS, false, "0".repeat(64)));
        var corrupted = new Fixture((f.archive.clone()), f.sums, f.hash);
        corrupted.archive()[5] ^= 1;
        assertThrows(IOException.class, () -> bootstrap(corrupted, new AtomicInteger()).resolve(temp, VERSION, WINDOWS, false, null));
    }
    @Test void rejectsMismatchedIdentityAndUnexpectedOrTraversingMembers() throws Exception {
        assertThrows(IOException.class, () -> bootstrap(fixture(true, null), new AtomicInteger()).resolve(temp, VERSION, WINDOWS, false, null));
        assertThrows(IOException.class, () -> bootstrap(fixture(false, "../outside"), new AtomicInteger()).resolve(temp, VERSION, WINDOWS, false, null));
        assertThrows(IOException.class, () -> bootstrap(fixture(false, "libexec/extra.exe"), new AtomicInteger()).resolve(temp, VERSION, WINDOWS, false, null));
    }
    @Test void offlineRequiresVerifiedCacheAndRejectsCacheSymlink() throws Exception {
        assertThrows(IOException.class, () -> bootstrap(fixture(false, null), new AtomicInteger()).resolve(temp, VERSION, WINDOWS, true, null));
        if (!System.getProperty("os.name").toLowerCase().contains("win")) {
            Path cache = temp.resolve("malicious"); Files.createSymbolicLink(cache, temp);
            assertThrows(IOException.class, () -> bootstrap(fixture(false, null), new AtomicInteger()).resolve(cache, VERSION, WINDOWS, true, null));
        }
    }
    @Test void rejectsUnsupportedPlatformsAndVersions() {
        assertThrows(IOException.class, () -> CliBootstrap.Platform.current("solaris", "x86_64"));
        assertThrows(IOException.class, () -> CliBootstrap.Platform.current("Linux", "sparc"));
        assertThrows(IOException.class, () -> bootstrap(new Fixture(new byte[0],new byte[0],""), new AtomicInteger()).resolve(temp, "evil/../version", WINDOWS, false, null));
    }
}
