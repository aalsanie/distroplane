package io.github.aalsanie.distroplane.maven;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.apache.commons.compress.archivers.tar.TarArchiveEntry;
import org.apache.commons.compress.archivers.tar.TarArchiveInputStream;
import org.apache.commons.compress.compressors.gzip.GzipCompressorInputStream;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.channels.FileChannel;
import java.nio.channels.FileLock;
import java.nio.file.*;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Duration;
import java.util.*;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import java.util.zip.ZipEntry;
import java.util.zip.ZipInputStream;

import static java.nio.file.StandardOpenOption.*;

/** A release archive is not trusted until both its manifest and embedded hashes match. */
final class CliBootstrap {
    private static final ObjectMapper JSON = new ObjectMapper();
    private static final String ORIGIN = "https://github.com/aalsanie/distroplane/releases/download/";
    private static final String CHECKSUMS = "SHA256SUMS";
    private static final long MAX_ARCHIVE = 512L << 20;
    private static final long MAX_EXE = 128L << 20;
    private static final Pattern VERSION = Pattern.compile("0\\.[0-9]+\\.[0-9]+(?:-[0-9A-Za-z.-]+)?|1\\.[0-9]+\\.[0-9]+(?:-[0-9A-Za-z.-]+)?");
    private static final Pattern SHA = Pattern.compile("[a-fA-F0-9]{64}");

    interface Fetcher { byte[] fetch(URI uri, int maximum) throws IOException; }

    private final Fetcher fetcher;
    CliBootstrap() {
        HttpClient client = HttpClient.newBuilder().followRedirects(HttpClient.Redirect.NORMAL)
                .connectTimeout(Duration.ofSeconds(15)).build();
        fetcher = (uri, maximum) -> {
            HttpRequest req = HttpRequest.newBuilder(uri).GET().timeout(Duration.ofMinutes(3)).build();
            HttpResponse<InputStream> response;
            try { response = client.send(req, HttpResponse.BodyHandlers.ofInputStream()); }
            catch (InterruptedException e) { Thread.currentThread().interrupt(); throw new IOException("Interrupted during release download", e); }
            if (response.statusCode() != 200) { response.body().close(); throw new IOException("Release download failed (HTTP " + response.statusCode() + ")"); }
            // GitHub release assets may redirect to GitHub's designated CDN; no request carries credentials.
            try (InputStream in = response.body()) { return in.readNBytes(maximum + 1); }
        };
    }
    CliBootstrap(Fetcher fetcher) { this.fetcher = Objects.requireNonNull(fetcher); }

    record Platform(String os, String arch) {
        static Platform current(String osName, String archName) throws IOException {
            String os = osName.toLowerCase(Locale.ROOT);
            if (os.contains("win")) os = "windows";
            else if (os.contains("mac") || os.contains("darwin")) os = "darwin";
            else if (os.contains("linux")) os = "linux";
            else throw new IOException("Unsupported operating system for Distroplane: " + osName);
            String arch = archName.toLowerCase(Locale.ROOT);
            if (arch.equals("amd64") || arch.equals("x86_64") || arch.equals("x64")) arch = "amd64";
            else if (arch.equals("aarch64") || arch.equals("arm64")) arch = "arm64";
            else throw new IOException("Unsupported CPU architecture for Distroplane: " + archName);
            return new Platform(os, arch);
        }
        String cli() { return "bin/distroplane" + (os.equals("windows") ? ".exe" : ""); }
        Set<String> members() {
            Set<String> result = new HashSet<>(List.of(cli(), "PACKAGE-METADATA.json"));
            for (String provider : List.of("npm", "sdkman", "homebrew", "winget"))
                result.add("libexec/distroplane-provider-" + provider + (os.equals("windows") ? ".exe" : ""));
            return result;
        }
        String archive(String version) {
            return "distroplane_" + version + "_" + os + "_" + arch + (os.equals("windows") ? ".zip" : ".tar.gz");
        }
    }

    Path resolve(Path cacheRoot, String version, Platform platform, boolean offline, String pinnedHash) throws IOException {
        if (!VERSION.matcher(version).matches()) throw new IOException("Unsupported CLI version format");
        if (pinnedHash != null && !pinnedHash.isBlank() && !SHA.matcher(pinnedHash).matches())
            throw new IOException("Invalid pinned CLI archive SHA-256");
        Path root = canonicalSystemCachePath(cacheRoot).resolve(version).resolve(platform.os() + "-" + platform.arch());
        mkdirsNoLinks(root);
        Path lock = root.resolve(".bootstrap.lock");
        if (Files.isSymbolicLink(lock)) throw new IOException("Unsafe bootstrap lock");
        try (FileChannel channel = FileChannel.open(lock, CREATE, WRITE); FileLock ignored = channel.lock()) {
            String name = platform.archive(version);
            String expected = null;
            if (!offline) {
                byte[] checksums = fetcher.fetch(URI.create(ORIGIN + "v" + version + "/" + CHECKSUMS), 1024 * 1024);
                if (checksums.length > 1024 * 1024) throw new IOException("Checksum manifest too large");
                expected = checksum(checksums, name);
            }
            if (pinnedHash != null && !pinnedHash.isBlank()) {
                if (expected != null && !expected.equalsIgnoreCase(pinnedHash)) throw new IOException("Pinned archive checksum does not match release manifest");
                expected = pinnedHash.toLowerCase(Locale.ROOT);
            }
            if (expected == null) {
                try (DirectoryStream<Path> cached = Files.newDirectoryStream(root, "sha256-*")) {
                    for (Path p : cached) {
                        String hash = p.getFileName().toString().substring(7);
                        if (!SHA.matcher(hash).matches() || !Files.isDirectory(p, LinkOption.NOFOLLOW_LINKS)) throw new IOException("Unsafe cached release directory");
                        if (expected != null) throw new IOException("Ambiguous offline cached Distroplane release; pin archiveSha256");
                        expected = hash;
                    }
                }
                if (expected == null) throw new IOException("Maven offline mode requires a previously verified cached CLI package");
            }
            Path destination = root.resolve("sha256-" + expected);
            if (Files.exists(destination, LinkOption.NOFOLLOW_LINKS)) {
                try { verifyCached(destination, version, platform, expected); return destination.resolve(platform.cli()); }
                catch (IOException corrupt) { throw new IOException("Cached Distroplane CLI is corrupt; remove the cache manually before retrying", corrupt); }
            }
            if (offline) throw new IOException("Requested Distroplane package is not present in the verified offline cache");
            byte[] archive = fetcher.fetch(URI.create(ORIGIN + "v" + version + "/" + name), (int) MAX_ARCHIVE);
            if (archive.length > MAX_ARCHIVE || !digest(archive).equals(expected)) throw new IOException("Distroplane archive SHA-256 mismatch");
            Path staging = Files.createTempDirectory(root, ".staging-");
            try {
                extract(archive, staging, platform);
                verifyPackage(staging, version, platform);
                Files.writeString(staging.resolve("archive.sha256"), expected, CREATE_NEW);
                try { Files.move(staging, destination, StandardCopyOption.ATOMIC_MOVE); }
                catch (AtomicMoveNotSupportedException e) { throw new IOException("Filesystem does not support atomic bootstrap publication", e); }
            } finally { deleteTree(staging); }
            return destination.resolve(platform.cli());
        }
    }

    // macOS exposes /var through the OS-owned /private/var symlink. Normalize
    // exactly that well-known root before enforcing no-symlink checks on all
    // cache directories; user-created redirects are still rejected.
    private static Path canonicalSystemCachePath(Path cache) throws IOException {
        Path absolute = cache.toAbsolutePath().normalize();
        Path var = Path.of("/var");
        if (absolute.startsWith(var) && Files.isSymbolicLink(var) &&
            var.toRealPath().equals(Path.of("/private/var"))) {
            return Path.of("/private").resolve(Path.of("/").relativize(absolute));
        }
        return absolute;
    }

    private static void mkdirsNoLinks(Path path) throws IOException {
        Path p = path.getRoot();
        for (Path part : path) {
            p = p.resolve(part);
            if (Files.exists(p, LinkOption.NOFOLLOW_LINKS)) {
                if (!Files.isDirectory(p, LinkOption.NOFOLLOW_LINKS) || Files.isSymbolicLink(p)) throw new IOException("Unsafe bootstrap cache path: " + p);
            } else Files.createDirectory(p);
        }
    }
    private static String checksum(byte[] bytes, String name) throws IOException {
        String found = null;
        String data = new String(bytes, java.nio.charset.StandardCharsets.UTF_8);
        for (String line : data.split("\\r?\\n")) {
            if (line.isBlank()) continue;
            Matcher m = Pattern.compile("^([0-9a-fA-F]{64})  ([^/\\\\]+)$").matcher(line);
            if (!m.matches()) throw new IOException("Invalid release checksum manifest entry");
            if (m.group(2).equals(name)) {
                if (found != null) throw new IOException("Duplicate release asset checksum");
                found = m.group(1).toLowerCase(Locale.ROOT);
            }
        }
        if (found == null) throw new IOException("Required Distroplane archive missing from checksum manifest");
        return found;
    }
    private static void extract(byte[] archive, Path dir, Platform platform) throws IOException {
        Set<String> found = new HashSet<>();
        if (platform.os().equals("windows")) {
            try (ZipInputStream zip = new ZipInputStream(new java.io.ByteArrayInputStream(archive))) {
                ZipEntry entry;
                while ((entry = zip.getNextEntry()) != null) {
                    writeMember(zip, entry.getName(), entry.isDirectory(), dir, platform, found);
                    zip.closeEntry();
                }
            }
        } else {
            try (TarArchiveInputStream tar = new TarArchiveInputStream(new GzipCompressorInputStream(new java.io.ByteArrayInputStream(archive)))) {
                TarArchiveEntry entry;
                while ((entry = tar.getNextEntry()) != null) {
                    if (!entry.isFile()) throw new IOException("Unsafe tar member type");
                    int permitted = entry.getName().equals("PACKAGE-METADATA.json") ? 0644 : 0755;
                    if ((entry.getMode() & 07777) != permitted) throw new IOException("Unsafe tar executable permissions");
                    writeMember(tar, entry.getName(), false, dir, platform, found);
                }
            }
        }
        if (!found.equals(platform.members())) throw new IOException("Missing Distroplane archive members");
    }
    private static void writeMember(InputStream in, String name, boolean directory, Path dir, Platform platform, Set<String> found) throws IOException {
        if (directory || !platform.members().contains(name) || !found.add(name)) throw new IOException("Unexpected or duplicate Distroplane archive member");
        Path target = dir.resolve(name).normalize();
        if (!target.startsWith(dir)) throw new IOException("Unsafe archive member path");
        Files.createDirectories(target.getParent());
        long limit = name.equals("PACKAGE-METADATA.json") ? 1L << 20 : MAX_EXE;
        try (OutputStream out = Files.newOutputStream(target, CREATE_NEW, WRITE)) {
            byte[] buffer = new byte[64 * 1024];
            long count = 0; int n;
            while ((n = in.read(buffer)) != -1) {
                count += n;
                if (count > limit) throw new IOException("Oversized Distroplane archive member");
                out.write(buffer, 0, n);
            }
        }
        if (!name.equals("PACKAGE-METADATA.json") && !platform.os().equals("windows") && !target.toFile().setExecutable(true, true))
            throw new IOException("Could not make packaged executable runnable");
    }
    private static void verifyCached(Path dir, String version, Platform platform, String sha) throws IOException {
        if (!Files.isDirectory(dir, LinkOption.NOFOLLOW_LINKS) || Files.isSymbolicLink(dir)) throw new IOException("Cache directory replaced");
        Path marker = dir.resolve("archive.sha256");
        if (!Files.isRegularFile(marker, LinkOption.NOFOLLOW_LINKS) || !Files.readString(marker).equals(sha)) throw new IOException("Cached release checksum marker is invalid");
        verifyPackage(dir, version, platform);
    }
    private static void verifyPackage(Path dir, String version, Platform platform) throws IOException {
        for (String name : platform.members()) {
            Path file = dir.resolve(name);
            if (!Files.isRegularFile(file, LinkOption.NOFOLLOW_LINKS) || Files.isSymbolicLink(file)) throw new IOException("Missing or unsafe cached Distroplane member: " + name);
        }
        // Never trust a marker alone: every cache read re-verifies embedded hashes.
        if (Files.size(dir.resolve("PACKAGE-METADATA.json")) > (1L << 20)) throw new IOException("Cached metadata is oversized");
        JsonNode meta = JSON.readTree(Files.readAllBytes(dir.resolve("PACKAGE-METADATA.json")));
        if (!meta.path("schemaVersion").asText().equals("1") || !meta.path("version").asText().equals(version) ||
            !meta.path("os").asText().equals(platform.os()) || !meta.path("arch").asText().equals(platform.arch()) ||
            meta.path("cgoEnabled").asBoolean(true) || !meta.path("files").isArray() || meta.path("files").size() != 5)
            throw new IOException("Distroplane release package identity does not match target");
        Set<String> verified = new HashSet<>();
        for (JsonNode record : meta.path("files")) {
            String name = record.path("path").asText();
            if (!platform.members().contains(name) || name.equals("PACKAGE-METADATA.json") || !verified.add(name) ||
                !record.path("mode").asText().equals("0755") || !SHA.matcher(record.path("sha256").asText()).matches())
                throw new IOException("Invalid Distroplane package metadata");
            Path file = dir.resolve(name);
            if (!Files.isRegularFile(file, LinkOption.NOFOLLOW_LINKS) || Files.size(file) != record.path("size").asLong(-1) ||
                !shaFile(file).equalsIgnoreCase(record.path("sha256").asText())) throw new IOException("Distroplane package executable checksum mismatch: " + name);
        }
        if (verified.size() != 5) throw new IOException("Missing executable metadata records");
        if (!platform.os().equals("windows") && !Files.isExecutable(dir.resolve(platform.cli()))) throw new IOException("Cached Distroplane CLI is not executable");
        // Files other than the six expected members and the own checksum marker are not permitted.
        try (var paths = Files.walk(dir)) {
            Set<String> allowed = new HashSet<>(platform.members());
            allowed.add("archive.sha256"); allowed.add("bin"); allowed.add("libexec"); allowed.add("");
            for (Path p : paths.toList()) {
                String rel = dir.relativize(p).toString().replace('\\', '/');
                if (!allowed.contains(rel) || Files.isSymbolicLink(p)) throw new IOException("Unexpected Distroplane cache entry: " + rel);
            }
        }
    }
    private static String shaFile(Path path) throws IOException {
        try (InputStream in = Files.newInputStream(path)) { return digest(in); }
    }
    private static String digest(byte[] bytes) { return HexFormat.of().formatHex(hash(bytes)); }
    private static byte[] hash(byte[] bytes) {
        try { return MessageDigest.getInstance("SHA-256").digest(bytes); }
        catch (NoSuchAlgorithmException e) { throw new IllegalStateException(e); }
    }
    private static String digest(InputStream in) throws IOException {
        try {
            MessageDigest md = MessageDigest.getInstance("SHA-256");
            byte[] b = new byte[65536]; int n;
            while ((n = in.read(b)) != -1) md.update(b, 0, n);
            return HexFormat.of().formatHex(md.digest());
        } catch (NoSuchAlgorithmException e) { throw new IllegalStateException(e); }
    }
    private static void deleteTree(Path dir) throws IOException {
        if (!Files.exists(dir, LinkOption.NOFOLLOW_LINKS)) return;
        try (var paths = Files.walk(dir)) {
            for (Path p : paths.sorted(Comparator.reverseOrder()).toList()) Files.deleteIfExists(p);
        }
    }
}
