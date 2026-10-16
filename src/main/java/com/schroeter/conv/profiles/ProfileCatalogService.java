package com.schroeter.conv.profiles;

import static com.schroeter.conv.profiles.ProfileCatalogModels.*;

import java.io.IOException;
import java.io.InputStream;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.ProxySelector;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.file.AtomicMoveNotSupportedException;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.security.MessageDigest;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HexFormat;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.TimeUnit;
import java.util.UUID;
import java.util.regex.Pattern;
import java.util.zip.ZipEntry;
import java.util.zip.ZipInputStream;

import org.springframework.beans.factory.annotation.Value;
import org.springframework.stereotype.Service;
import org.apache.commons.compress.archivers.tar.TarArchiveEntry;
import org.apache.commons.compress.archivers.tar.TarArchiveInputStream;
import org.apache.commons.compress.compressors.xz.XZCompressorInputStream;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.schroeter.conv.config.UserConfigService;
import com.schroeter.conv.converters.ConverterFileDefinition;
import com.schroeter.conv.converters.ConverterRegistry;

@Service
public class ProfileCatalogService {
    private static final String BUILT_IN_CATALOG_ID = "101conv-official";
    private static final String BUILT_IN_CATALOG_NAME = "101conv Official Catalog";
    private static final long MAX_CATALOG_BYTES = 1_000_000;
    private static final long MAX_MANIFEST_BYTES = 256_000;
    private static final long MAX_PACKAGE_BYTES = 512L * 1024 * 1024;
    private static final int MAX_ZIP_ENTRIES = 10_000;
    private static final Pattern SAFE_ID = Pattern.compile("[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}");
    private final ObjectMapper mapper = new ObjectMapper();
    private final Path dataDir;
    private final Path converterConfigDir;
    private final ConverterRegistry registry;
    private final String builtInCatalogUrl;
    private final String applicationVersion;
    private final boolean proxyConfigured;
    private final HttpClient http;

    public ProfileCatalogService(
            @Value("${app.data-dir:./data/101conv}") String dataDir,
            @Value("${app.profiles.catalog-url:https://raw.githubusercontent.com/Timisah/101conv/main/catalog/index.json}") String builtInCatalogUrl,
            UserConfigService userConfigService,
            ConverterRegistry registry) {
        this.dataDir = Path.of(dataDir).toAbsolutePath().normalize();
        this.converterConfigDir = Path.of(userConfigService.converterConfigDir()).toAbsolutePath().normalize();
        this.registry = registry;
        this.builtInCatalogUrl = builtInCatalogUrl.trim();
        String implementationVersion = ProfileCatalogService.class.getPackage().getImplementationVersion();
        this.applicationVersion = implementationVersion == null || implementationVersion.isBlank() ? "0.0.1-SNAPSHOT" : implementationVersion;
        ProxySelector proxySelector = configuredProxySelector();
        this.proxyConfigured = proxySelector != null;
        HttpClient.Builder client = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(15))
                .followRedirects(HttpClient.Redirect.NEVER);
        if (proxySelector != null) client.proxy(proxySelector);
        this.http = client.build();
    }

    public synchronized List<Source> sources() {
        List<Source> all = new ArrayList<>();
        all.add(new Source(BUILT_IN_CATALOG_ID, BUILT_IN_CATALOG_NAME, builtInCatalogUrl, true));
        all.addAll(loadSources());
        return List.copyOf(all);
    }

    public synchronized List<Source> addSource(String rawUrl) {
        URI uri = safeHttps(rawUrl);
        List<Source> sources = new ArrayList<>(loadSources());
        if (sources().stream().noneMatch(s -> s.url().equals(uri.toString()))) {
            sources.add(new Source(UUID.randomUUID().toString(), uri.getHost(), uri.toString(), false));
            saveSources(sources);
        }
        return sources();
    }

    public synchronized CatalogResponse catalog(String sourceId) {
        Catalog catalog = fetchCatalog(source(sourceId).url());
        if (catalog == null || catalog.version() != 1 || catalog.profiles() == null) {
            throw new IllegalArgumentException("Catalog must use version 1 and contain a profiles list.");
        }
        validateVersionRange(catalog.minimumAppVersion(), catalog.maximumAppVersionExclusive());
        requireApplicationCompatibility(catalog.minimumAppVersion(), catalog.maximumAppVersionExclusive());
        List<Entry> entries = catalog.profiles().stream().map(this::validateEntry).toList();
        List<Entry> compatible = entries.stream().filter(this::supportsApplication).toList();
        return new CatalogResponse(compatible, entries.size() - compatible.size(), applicationVersion);
    }

    public synchronized Manifest profile(String sourceId, String profileId, String version) {
        Entry entry = findEntry(sourceId, profileId, version);
        Manifest manifest = fetchJson(entry.manifestUrl(), MAX_MANIFEST_BYTES, Manifest.class);
        validateManifest(manifest, entry);
        requireApplicationCompatibility(manifest.minimumAppVersion(), manifest.maximumAppVersionExclusive());
        return manifest;
    }

    public synchronized List<Installed> installed() {
        Path root = dataDir.resolve("profiles");
        if (!Files.isDirectory(root)) return List.of();
        try (var paths = Files.list(root)) {
            return paths.filter(Files::isDirectory).map(path -> {
                Path file = path.resolve("profile.json");
                try { return mapper.readValue(file.toFile(), Installed.class); }
                catch (Exception ignored) { return null; }
            }).filter(java.util.Objects::nonNull).toList();
        } catch (IOException e) {
            throw new IllegalStateException("Could not read installed profiles.", e);
        }
    }

    public synchronized Installed install(String sourceId, String profileId, String version) {
        Entry entry = findEntry(sourceId, profileId, version);
        Manifest manifest = fetchJson(entry.manifestUrl(), MAX_MANIFEST_BYTES, Manifest.class);
        validateManifest(manifest, entry);
        requireApplicationCompatibility(manifest.minimumAppVersion(), manifest.maximumAppVersionExclusive());
        Path installedRoot = dataDir.resolve("profiles").resolve(manifest.id()).resolve(manifest.version()).normalize();
        Path profileRoot = dataDir.resolve("profiles").resolve(manifest.id()).normalize();
        if (!installedRoot.startsWith(profileRoot)) {
            throw new IllegalArgumentException("Invalid profile installation path.");
        }
        if (Files.exists(profileRoot, LinkOption.NOFOLLOW_LINKS)) {
            if (Files.isSymbolicLink(profileRoot) || !Files.isDirectory(profileRoot, LinkOption.NOFOLLOW_LINKS)) {
                throw new IllegalArgumentException("The installed profile path is invalid.");
            }
            try {
                Installed previous = mapper.readValue(profileRoot.resolve("profile.json").toFile(), Installed.class);
                if (previous.id().equals(manifest.id()) && previous.version().equals(manifest.version())
                        && previous.sha256().equalsIgnoreCase(manifest.artifact().sha256())
                        && Files.isDirectory(installedRoot, LinkOption.NOFOLLOW_LINKS)) {
                    Map<String, String> executablePaths = executablePaths(manifest.artifact());
                    validateInstalledExecutables(installedRoot, executablePaths);
                    writeConverterConfig(manifest, executablePaths, installedRoot);
                    registry.refresh();
                    return previous;
                }
            } catch (IOException e) {
                throw new IllegalStateException("Could not refresh the installed profile configuration.", e);
            }
            throw new IllegalArgumentException("A different version of this profile is already installed. Remove it before replacing it.");
        }
        Path work = dataDir.resolve(".profile-install-" + UUID.randomUUID()).normalize();
        try {
            Files.createDirectories(work);
            Path archive = work.resolve("package.zip");
            download(manifest.artifact().url(), archive, MAX_PACKAGE_BYTES);
            String digest = sha256(archive);
            if (!digest.equalsIgnoreCase(manifest.artifact().sha256())) {
                throw new IllegalArgumentException("Package checksum does not match the profile manifest.");
            }
            Path unpacked = work.resolve("unpacked");
            extractArchive(archive, unpacked, manifest.artifact().format());
            Map<String, String> executablePaths = executablePaths(manifest.artifact());
            validateInstalledExecutables(unpacked, executablePaths);
            Files.createDirectories(profileRoot);
            Files.move(unpacked, installedRoot, StandardCopyOption.ATOMIC_MOVE);
            writeConverterConfig(manifest, executablePaths, installedRoot);
            Installed installed = new Installed(manifest.id(), manifest.version(), manifest.name(), manifest.license(), digest);
            writeAtomic(profileRoot.resolve("profile.json"), mapper.writeValueAsBytes(installed));
            registry.refresh();
            return installed;
        } catch (IOException e) {
            deleteTree(work);
            deleteTree(profileRoot);
            throw new IllegalStateException("Profile package could not be installed.", e);
        } catch (RuntimeException e) {
            deleteTree(work);
            deleteTree(profileRoot);
            throw e;
        } finally {
            deleteTree(work);
        }
    }

    private void writeConverterConfig(Manifest manifest, Map<String, String> executablePaths, Path installedRoot) throws IOException {
        Converter c = manifest.converter();
        Map<String, String> variables = new java.util.HashMap<>();
        executablePaths.forEach((name, path) -> variables.put(name, installedRoot.resolve(path).toString()));
        ConverterFileDefinition definition = new ConverterFileDefinition(
                c.id(), c.friendlyName(), c.priority(), true, variables,
                c.rules().stream().map(r -> new ConverterFileDefinition.RuleDefinition(r.sources(), r.targets(), r.command())).toList());
        for (var rule : definition.rules()) {
            if (rule.command() == null || rule.command().isEmpty()
                    || !executablePaths.containsKey(variableName(rule.command().get(0)))) {
                throw new IllegalArgumentException("Every conversion rule must launch one of its declared native executables as its first command argument.");
            }
        }
        Files.createDirectories(converterConfigDir);
        Path config = converterConfigDir.resolve("profile-" + manifest.id() + ".json");
        writeAtomic(config, mapper.writeValueAsBytes(definition));
    }

    private static void validateInstalledExecutables(Path root, Map<String, String> executablePaths) throws IOException {
        for (String executablePath : executablePaths.values()) {
            Path executable = root.resolve(executablePath).normalize();
            if (!executable.startsWith(root) || !Files.isRegularFile(executable, LinkOption.NOFOLLOW_LINKS)
                    || Files.isSymbolicLink(executable)) {
                throw new IllegalArgumentException("A profile executable is missing or outside the package.");
            }
            rejectScript(executable);
        }
    }

    private Entry findEntry(String sourceId, String profileId, String version) {
        return catalog(sourceId).profiles().stream().filter(e -> e.id().equals(profileId) && e.version().equals(version))
                .findFirst().orElseThrow(() -> new IllegalArgumentException("Profile is not listed in this catalog."));
    }

    private Entry validateEntry(Entry entry) {
        if (entry == null || !safeId(entry.id()) || entry.version() == null || entry.version().isBlank()
                || entry.name() == null || entry.name().isBlank()) throw new IllegalArgumentException("Catalog contains an invalid profile entry.");
        safeHttps(entry.manifestUrl());
        validateVersionRange(entry.minimumAppVersion(), entry.maximumAppVersionExclusive());
        return entry;
    }

    private void validateManifest(Manifest m, Entry e) {
        if (m == null || m.schemaVersion() != 1 || !e.id().equals(m.id()) || !e.version().equals(m.version())
                || !safeId(m.id()) || !safeId(m.version()) || m.name() == null || m.name().isBlank()
                || m.license() == null || m.license().isBlank() || m.artifact() == null || m.converter() == null
                || m.converter().rules() == null || m.converter().rules().isEmpty()) {
            throw new IllegalArgumentException("Profile manifest is incomplete or does not match the catalog entry.");
        }
        if (m.artifact().format() == null || !Set.of("zip", "tar.xz", "tar.lz").contains(m.artifact().format().toLowerCase(Locale.ROOT))) {
            throw new IllegalArgumentException("Only ZIP, tar.xz and tar.lz packages are supported in this release.");
        }
        if (!platform().equalsIgnoreCase(m.artifact().platform())) throw new IllegalArgumentException("This profile package is not compatible with the running server (" + platform() + ").");
        safeHttps(m.artifact().url());
        validateVersionRange(m.minimumAppVersion(), m.maximumAppVersionExclusive());
        if (m.artifact().sha256() == null || !m.artifact().sha256().matches("(?i)[0-9a-f]{64}")) throw new IllegalArgumentException("Profile must provide a SHA-256 checksum.");
        Map<String, String> executables = executablePaths(m.artifact());
        if (!safeId(m.converter().id())) throw new IllegalArgumentException("Converter id is invalid.");
        for (Rule rule : m.converter().rules()) {
            if (rule == null || rule.sources() == null || rule.sources().isEmpty() || rule.targets() == null || rule.targets().isEmpty()
                    || rule.command() == null || rule.command().isEmpty()) throw new IllegalArgumentException("Profile contains an invalid conversion rule.");
            if (!executables.containsKey(variableName(rule.command().get(0)))) throw new IllegalArgumentException("Every conversion rule must launch one of its declared native executables as its first command argument.");
        }
    }

    private Catalog fetchCatalog(String url) { return fetchJson(url, MAX_CATALOG_BYTES, Catalog.class); }

    private <T> T fetchJson(String url, long limit, Class<T> type) {
        URI uri = safeHttps(url);
        HttpResponse<InputStream> response = request(uri);
        try (InputStream in = response.body()) {
            if (response.statusCode() != 200) throw new IllegalArgumentException("Catalog server returned HTTP " + response.statusCode() + ".");
            byte[] bytes = readBounded(in, limit);
            return mapper.readValue(bytes, type);
        } catch (IOException e) { throw new IllegalArgumentException("Could not read catalog data.", e); }
    }

    private void download(String url, Path target, long limit) throws IOException {
        HttpResponse<InputStream> response = request(safeHttps(url));
        try (InputStream in = response.body()) {
            if (response.statusCode() != 200) throw new IllegalArgumentException("Package server returned HTTP " + response.statusCode() + ".");
            long contentLength = response.headers().firstValueAsLong("content-length").orElse(-1);
            if (contentLength > limit) throw new IllegalArgumentException("Package exceeds the allowed download size.");
            try (var out = Files.newOutputStream(target)) {
                byte[] buffer = new byte[64 * 1024];
                long downloaded = 0;
                int n;
                while ((n = in.read(buffer)) >= 0) {
                    downloaded += n;
                    if (downloaded > limit) throw new IllegalArgumentException("Package exceeds the allowed download size.");
                    out.write(buffer, 0, n);
                }
            } catch (RuntimeException | IOException e) {
                Files.deleteIfExists(target);
                throw e;
            }
        }
    }

    private HttpResponse<InputStream> request(URI uri) {
        try {
            URI current = uri;
            for (int redirects = 0; redirects <= 3; redirects++) {
                HttpResponse<InputStream> response = http.send(HttpRequest.newBuilder(current).timeout(Duration.ofMinutes(3)).GET().build(), HttpResponse.BodyHandlers.ofInputStream());
                if (response.statusCode() < 300 || response.statusCode() >= 400) return response;
                String location = response.headers().firstValue("location").orElse(null);
                response.body().close();
                if (location == null || redirects == 3) throw new IllegalArgumentException("Catalog source redirected too many times or omitted a destination.");
                current = safeHttps(current.resolve(location).toString());
            }
            throw new IllegalArgumentException("Catalog source redirected too many times.");
        } catch (Exception e) { throw new IllegalArgumentException("Could not retrieve data from the configured HTTPS source.", e); }
    }

    private URI safeHttps(String raw) {
        try {
            URI uri = URI.create(raw == null ? "" : raw.trim());
            String host = uri.getHost();
            if (!"https".equalsIgnoreCase(uri.getScheme()) || host == null || uri.getUserInfo() != null
                    || (uri.getPort() != -1 && uri.getPort() != 443) || uri.getFragment() != null) throw new IllegalArgumentException("Profile sources and packages must use public HTTPS URLs on port 443.");
            String h = host.toLowerCase(Locale.ROOT);
            if (h.equals("localhost") || h.endsWith(".localhost") || h.endsWith(".local") || h.endsWith(".internal")) throw new IllegalArgumentException("Private network URLs are not allowed.");
            if (!proxyConfigured || isAddressLiteral(host)) {
                for (InetAddress address : InetAddress.getAllByName(host)) {
                    if (!isPublicAddress(address)) throw new IllegalArgumentException("Private or reserved network URLs are not allowed.");
                }
            }
            return uri;
        } catch (IOException | IllegalArgumentException e) {
            if (e instanceof IllegalArgumentException iae) throw iae;
            throw new IllegalArgumentException("Could not resolve the HTTPS source host.", e);
        }
    }

    private List<Source> loadSources() {
        Path file = dataDir.resolve("profile-catalogs.json");
        if (!Files.exists(file)) return List.of();
        try { return List.of(mapper.readValue(file.toFile(), Source[].class)); }
        catch (IOException e) { throw new IllegalStateException("Profile catalog settings are unreadable.", e); }
    }

    private Source source(String id) {
        return sources().stream().filter(s -> s.id().equals(id)).findFirst().orElseThrow(() -> new IllegalArgumentException("Catalog source not found."));
    }

    private void saveSources(List<Source> sources) {
        try { Files.createDirectories(dataDir); writeAtomic(dataDir.resolve("profile-catalogs.json"), mapper.writeValueAsBytes(sources.stream().filter(s -> !s.builtIn()).toList())); }
        catch (IOException e) { throw new IllegalStateException("Could not save profile catalogs.", e); }
    }

    private static byte[] readBounded(InputStream in, long max) throws IOException {
        byte[] buffer = new byte[8192];
        var out = new java.io.ByteArrayOutputStream();
        int read;
        while ((read = in.read(buffer)) >= 0) { if ((long) out.size() + read > max) throw new IllegalArgumentException("Remote data exceeds the allowed size."); out.write(buffer, 0, read); }
        return out.toByteArray();
    }

    private static String sha256(Path file) throws IOException {
        try (InputStream in = Files.newInputStream(file)) {
            MessageDigest digest = MessageDigest.getInstance("SHA-256"); byte[] b = new byte[64 * 1024]; int n;
            while ((n = in.read(b)) >= 0) digest.update(b, 0, n);
            return HexFormat.of().formatHex(digest.digest());
        } catch (Exception e) { if (e instanceof IOException io) throw io; throw new IllegalStateException(e); }
    }

    static void extractArchive(Path archive, Path destination, String format) throws IOException {
        if ("tar.xz".equalsIgnoreCase(format)) {
            extractTarXz(archive, destination);
            return;
        }
        if ("tar.lz".equalsIgnoreCase(format)) {
            extractTarLzip(archive, destination);
            return;
        }
        extractZip(archive, destination);
    }

    private static void extractTarLzip(Path archive, Path destination) throws IOException {
        Path tarFile = archive.resolveSibling("package.tar");
        Path errorFile = archive.resolveSibling("lzip-errors.txt");
        Process process;
        try {
            process = new ProcessBuilder("xz", "--memlimit-decompress=512MiB", "--format=lzip", "--decompress", "--stdout", archive.toString())
                    .redirectError(errorFile.toFile()).start();
        } catch (IOException e) {
            throw new IllegalArgumentException("The xz-utils package is required to install this profile.", e);
        }
        try (InputStream in = process.getInputStream(); var out = Files.newOutputStream(tarFile)) {
            byte[] buffer = new byte[64 * 1024];
            long expanded = 0;
            int n;
            while ((n = in.read(buffer)) >= 0) {
                expanded += n;
                if (expanded > MAX_PACKAGE_BYTES * 4) {
                    process.destroyForcibly();
                    throw new IllegalArgumentException("Expanded package exceeds the allowed size.");
                }
                out.write(buffer, 0, n);
            }
            if (!process.waitFor(2, TimeUnit.MINUTES)) {
                process.destroyForcibly();
                throw new IllegalArgumentException("LZIP package decompression timed out.");
            }
            if (process.exitValue() != 0) {
                String detail = "";
                if (Files.exists(errorFile)) {
                    try (InputStream errors = Files.newInputStream(errorFile)) {
                        detail = new String(readBounded(errors, 4096), java.nio.charset.StandardCharsets.UTF_8);
                    }
                }
                throw new IllegalArgumentException("Could not decompress the LZIP package." + (detail.isBlank() ? "" : " " + detail.strip()));
            }
            extractTar(tarFile, destination);
        } catch (InterruptedException e) {
            process.destroyForcibly();
            Thread.currentThread().interrupt();
            throw new IOException("LZIP package decompression was interrupted.", e);
        } finally {
            if (process.isAlive()) process.destroyForcibly();
            Files.deleteIfExists(tarFile);
            Files.deleteIfExists(errorFile);
        }
    }

    private static void extractZip(Path archive, Path destination) throws IOException {
        Files.createDirectories(destination);
        int count = 0; long expanded = 0;
        try (ZipInputStream zip = new ZipInputStream(Files.newInputStream(archive))) {
            ZipEntry entry;
            while ((entry = zip.getNextEntry()) != null) {
                if (++count > MAX_ZIP_ENTRIES) throw new IllegalArgumentException("Package contains too many files.");
                String name = entry.getName();
                if (name.startsWith("/") || name.contains("\\") || name.contains("..") || name.isBlank()) throw new IllegalArgumentException("Package contains an unsafe path.");
                Path target = destination.resolve(name).normalize();
                if (!target.startsWith(destination)) throw new IllegalArgumentException("Package contains an unsafe path.");
                if (entry.isDirectory()) Files.createDirectories(target);
                else {
                    Files.createDirectories(target.getParent());
                    try (var out = Files.newOutputStream(target)) {
                        byte[] buffer = new byte[8192]; int n;
                        while ((n = zip.read(buffer)) >= 0) { expanded += n; if (expanded > MAX_PACKAGE_BYTES * 4) throw new IllegalArgumentException("Expanded package exceeds the allowed size."); out.write(buffer, 0, n); }
                    }
                }
                zip.closeEntry();
            }
        }
    }

    private static void extractTarXz(Path archive, Path destination) throws IOException {
        Files.createDirectories(destination);
        try (var compressed = new XZCompressorInputStream(Files.newInputStream(archive))) {
            extractTar(compressed, destination);
        }
    }

    private static void extractTar(Path archive, Path destination) throws IOException {
        Files.createDirectories(destination);
        try (InputStream in = Files.newInputStream(archive)) {
            extractTar(in, destination);
        }
    }

    private static void extractTar(InputStream input, Path destination) throws IOException {
        Files.createDirectories(destination);
        int count = 0;
        long expanded = 0;
        try (var tar = new TarArchiveInputStream(input)) {
            TarArchiveEntry entry;
            while ((entry = tar.getNextEntry()) != null) {
                if (++count > MAX_ZIP_ENTRIES) throw new IllegalArgumentException("Package contains too many files.");
                if (!tar.canReadEntryData(entry) || entry.isSymbolicLink() || entry.isLink()) {
                    throw new IllegalArgumentException("Package contains an unsupported link or entry type.");
                }
                String name = entry.getName();
                if (name.startsWith("/") || name.contains("\\") || name.contains("..") || name.isBlank()) {
                    throw new IllegalArgumentException("Package contains an unsafe path.");
                }
                Path target = destination.resolve(name).normalize();
                if (!target.startsWith(destination)) throw new IllegalArgumentException("Package contains an unsafe path.");
                if (entry.isDirectory()) {
                    Files.createDirectories(target);
                } else if (entry.isFile()) {
                    Files.createDirectories(target.getParent());
                    try (var out = Files.newOutputStream(target)) {
                        byte[] buffer = new byte[8192];
                        int n;
                        while ((n = tar.read(buffer)) >= 0) {
                            expanded += n;
                            if (expanded > MAX_PACKAGE_BYTES * 4) throw new IllegalArgumentException("Expanded package exceeds the allowed size.");
                            out.write(buffer, 0, n);
                        }
                    }
                } else {
                    throw new IllegalArgumentException("Package contains an unsupported entry type.");
                }
            }
        }
    }

    private static Map<String, String> executablePaths(Artifact artifact) {
        Map<String, String> paths = new java.util.LinkedHashMap<>();
        validateExecutablePath(artifact.executable());
        paths.put("executable", artifact.executable());
        if (artifact.executables() != null) {
            artifact.executables().forEach((name, path) -> {
                if (!safeId(name) || name.equals("executable")) throw new IllegalArgumentException("Profile executable binding is invalid.");
                validateExecutablePath(path);
                paths.put(name, path);
            });
        }
        return paths;
    }

    private static void validateExecutablePath(String path) {
        if (path == null || path.isBlank() || path.startsWith("/") || path.contains("\\") || path.contains("..")) {
            throw new IllegalArgumentException("Profile executable path is unsafe.");
        }
    }

    private static String variableName(String command) {
        if (command == null || !command.matches("\\{var:[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}}")) return "";
        return command.substring(5, command.length() - 1);
    }

    private static void rejectScript(Path executable) throws IOException {
        try (InputStream in = Files.newInputStream(executable)) {
            if (in.read() == '#' && in.read() == '!') throw new IllegalArgumentException("Executable scripts are not supported; profiles must use native packages.");
        }
        executable.toFile().setExecutable(true, false);
    }

    private static boolean safeId(String s) { return s != null && SAFE_ID.matcher(s).matches(); }

    private boolean supportsApplication(Entry entry) {
        return AppVersion.supports(applicationVersion, entry.minimumAppVersion(), entry.maximumAppVersionExclusive());
    }

    private void requireApplicationCompatibility(String minimum, String maximumExclusive) {
        if (!AppVersion.supports(applicationVersion, minimum, maximumExclusive)) {
            throw new IllegalArgumentException("This profile does not support 101conv " + applicationVersion + ".");
        }
    }

    private static void validateVersionRange(String minimum, String maximumExclusive) {
        if ((minimum == null || minimum.isBlank()) && (maximumExclusive == null || maximumExclusive.isBlank())) return;
        if (minimum != null && !minimum.isBlank()) AppVersion.parse(minimum);
        if (maximumExclusive != null && !maximumExclusive.isBlank()) AppVersion.parse(maximumExclusive);
        if (minimum != null && !minimum.isBlank() && maximumExclusive != null && !maximumExclusive.isBlank()
                && AppVersion.parse(minimum).compareTo(AppVersion.parse(maximumExclusive)) >= 0) {
            throw new IllegalArgumentException("Profile app-version range must have a maximum greater than its minimum.");
        }
    }

    private static boolean isPublicAddress(InetAddress address) {
        byte[] b = address.getAddress();
        if (address.isAnyLocalAddress() || address.isLoopbackAddress() || address.isLinkLocalAddress()
                || address.isSiteLocalAddress() || address.isMulticastAddress()) return false;
        if (b.length == 4) {
            int a = b[0] & 0xff, second = b[1] & 0xff, third = b[2] & 0xff;
            return a != 0 && a != 10 && a != 127 && a < 224
                    && !(a == 100 && second >= 64 && second <= 127)
                    && !(a == 169 && second == 254)
                    && !(a == 172 && second >= 16 && second <= 31)
                    && !(a == 192 && (second == 0 || second == 168))
                    && !(a == 192 && second == 0 && third == 2)
                    && !(a == 198 && (second == 18 || second == 19 || second == 51))
                    && !(a == 203 && second == 0 && third == 113);
        }
        // Accept IPv6 global unicast only; reject ULA, link-local, mapped and documentation ranges.
        return b.length == 16 && (b[0] & 0xe0) == 0x20
                && !((b[0] & 0xff) == 0x20 && (b[1] & 0xff) == 0x01 && (b[2] & 0xff) == 0x0d && (b[3] & 0xff) == 0xb8);
    }

    private static String platform() {
        String os = System.getProperty("os.name", "").toLowerCase(Locale.ROOT);
        String arch = System.getProperty("os.arch", "").toLowerCase(Locale.ROOT);
        if (os.contains("linux") && Set.of("amd64", "x86_64", "x64").contains(arch)) return "linux-amd64";
        if (os.contains("linux") && Set.of("aarch64", "arm64").contains(arch)) return "linux-arm64";
        return "unsupported";
    }

    private static ProxySelector configuredProxySelector() {
        String raw = firstNonBlank(System.getenv("HTTPS_PROXY"), System.getenv("https_proxy"),
                System.getenv("HTTP_PROXY"), System.getenv("http_proxy"));
        if (raw == null) return null;
        try {
            URI proxy = URI.create(raw.trim());
            if (proxy.getHost() == null || proxy.getUserInfo() != null || (proxy.getPort() != -1 && proxy.getPort() <= 0)) {
                return null;
            }
            int port = proxy.getPort() < 0 ? 80 : proxy.getPort();
            return ProxySelector.of(new InetSocketAddress(proxy.getHost(), port));
        } catch (IllegalArgumentException e) {
            return null;
        }
    }

    private static boolean isAddressLiteral(String host) {
        return host.indexOf(':') >= 0 || host.matches("[0-9.]+");
    }

    private static String firstNonBlank(String... candidates) {
        for (String candidate : candidates) if (candidate != null && !candidate.isBlank()) return candidate;
        return null;
    }

    private static void writeAtomic(Path target, byte[] bytes) throws IOException {
        Files.createDirectories(target.getParent()); Path tmp = target.resolveSibling(target.getFileName() + ".tmp-" + UUID.randomUUID());
        Files.write(tmp, bytes);
        try { Files.move(tmp, target, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING); }
        catch (AtomicMoveNotSupportedException e) { Files.move(tmp, target, StandardCopyOption.REPLACE_EXISTING); }
    }

    private static void deleteTree(Path path) {
        if (!Files.exists(path)) return;
        try (var walk = Files.walk(path)) { walk.sorted(java.util.Comparator.reverseOrder()).forEach(p -> { try { Files.deleteIfExists(p); } catch (IOException ignored) {} }); }
        catch (IOException ignored) {}
    }
}
