package com.schroeter.conv.jobs;

import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Instant;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.TreeMap;
import java.util.UUID;

import org.springframework.beans.factory.annotation.Value;
import org.springframework.dao.DataIntegrityViolationException;
import org.springframework.data.domain.PageRequest;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.transaction.support.TransactionSynchronization;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import org.springframework.web.multipart.MultipartFile;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.schroeter.conv.conversion.ConversionOptionsService;
import com.schroeter.conv.converters.ConverterModule;
import com.schroeter.conv.converters.ConverterRegistry;
import com.schroeter.conv.converters.ConverterRequest;
import com.schroeter.conv.converters.BoundedProcessRunner;

@Service
public class ConversionJobService {

    private final ConversionJobRepository repository;
    private final ConverterRegistry converterRegistry;
    private final ConversionOptionsService optionsService;
    private final BoundedProcessRunner processRunner;
    private final ObjectMapper objectMapper = new ObjectMapper();
    private final Path jobsRoot;
    private final long leaseSeconds;

    public ConversionJobService(
            ConversionJobRepository repository,
            ConverterRegistry converterRegistry,
            ConversionOptionsService optionsService,
            BoundedProcessRunner processRunner,
            @Value("${app.data-dir:./data/101conv}") String dataDir,
            @Value("${app.jobs.lease-seconds:360}") long leaseSeconds) {
        this.repository = repository;
        this.converterRegistry = converterRegistry;
        this.optionsService = optionsService;
        this.processRunner = processRunner;
        this.jobsRoot = Path.of(dataDir).toAbsolutePath().normalize().resolve("jobs");
        this.leaseSeconds = Math.max(30, leaseSeconds);
    }

    @Transactional
    public ConversionJob submit(
            String ownerId,
            MultipartFile file,
            String sourceFormat,
            String targetFormat,
            String preferredConverterId,
            Map<String, String> requestedOptions,
            String rawIdempotencyKey) throws IOException {
        if (file == null || file.isEmpty()) {
            throw new IllegalArgumentException("A non-empty file is required.");
        }
        String normalizedSource = optionsService.normalizeFormat(sourceFormat);
        String normalizedTarget = optionsService.normalizeFormat(targetFormat);
        if (!normalizedSource.matches("[a-z0-9][a-z0-9+_-]{0,31}")
                || !normalizedTarget.matches("[a-z0-9][a-z0-9+_-]{0,31}")) {
            throw new IllegalArgumentException("Source and target formats must be valid format identifiers.");
        }
        List<ConverterModule> candidates = converterRegistry.resolveCandidates(normalizedSource, normalizedTarget,
                preferredConverterId);
        if (candidates.isEmpty()) {
            throw new UnsupportedConversionException(normalizedSource, normalizedTarget);
        }
        ConverterModule converter = candidates.get(0);
        Map<String, String> resolvedOptions = optionsService.resolveOptions(normalizedTarget,
                requestedOptions == null ? Map.of() : requestedOptions);
        String optionsJson = serializeOptions(resolvedOptions);
        String sourceHash = digest(file.getInputStream());
        String fingerprint = fingerprint(sourceHash, normalizedSource, normalizedTarget, converter.id(), optionsJson);
        String idempotencyKey = normalizeIdempotencyKey(rawIdempotencyKey);

        if (idempotencyKey != null) {
            Optional<ConversionJob> existing = repository.findByOwnerIdAndIdempotencyKey(ownerId, idempotencyKey);
            if (existing.isPresent()) {
                if (!existing.get().getRequestFingerprint().equals(fingerprint)) {
                    throw new JobConflictException("This idempotency key was already used for a different request.");
                }
                return existing.get();
            }
        }

        String id = UUID.randomUUID().toString();
        Path jobDir = jobsRoot.resolve(id).normalize();
        Path sourcePath = jobDir.resolve("input").resolve(safeFileName(file.getOriginalFilename())).normalize();
        if (!jobDir.startsWith(jobsRoot) || !sourcePath.startsWith(jobDir)) {
            throw new IOException("Invalid managed storage path.");
        }
        Files.createDirectories(sourcePath.getParent());
        try (InputStream input = file.getInputStream()) {
            Files.copy(input, sourcePath, StandardCopyOption.REPLACE_EXISTING);
        } catch (IOException e) {
            deleteTree(jobDir);
            throw e;
        }

        ConversionJob job = new ConversionJob(id, ownerId, safeFileName(file.getOriginalFilename()), normalizedSource,
                normalizedTarget, converter.id(), sourcePath.toString(), sourceHash, fingerprint, idempotencyKey,
                optionsJson, Instant.now());
        try {
            return repository.saveAndFlush(job);
        } catch (DataIntegrityViolationException e) {
            deleteTree(jobDir);
            throw new JobConflictException("A job with this idempotency key already exists.");
        }
    }

    @Transactional
    public Optional<ConversionJob> claimNext() {
        Instant now = Instant.now();
        List<ConversionJob> available = repository.findClaimable(ConversionJobState.QUEUED,
                ConversionJobState.RUNNING, now, PageRequest.of(0, 1));
        if (available.isEmpty()) {
            return Optional.empty();
        }
        ConversionJob job = available.get(0);
        if (job.isCancelRequested()) {
            job.recoverCanceledLease(now);
            return Optional.empty();
        }
        job.claim(now, now.plusSeconds(leaseSeconds));
        return Optional.of(job);
    }

    @Transactional(readOnly = true)
    public ConversionJob findOwned(String ownerId, String id) {
        return repository.findByIdAndOwnerId(id, ownerId).orElseThrow(JobNotFoundException::new);
    }

    @Transactional
    public boolean cancel(String ownerId, String id) {
        ConversionJob job = repository.findByIdAndOwnerId(id, ownerId).orElseThrow(JobNotFoundException::new);
        if (job.getState() == ConversionJobState.RUNNING) {
            job.requestCancellation(Instant.now());
            String jobId = job.getId();
            TransactionSynchronizationManager.registerSynchronization(new TransactionSynchronization() {
                @Override
                public void afterCommit() {
                    processRunner.cancel(jobId);
                }
            });
        } else if (job.getState() == ConversionJobState.QUEUED) {
            job.requestCancellation(Instant.now());
            deleteTree(Path.of(job.getSourcePath()).getParent().getParent());
        }
        return job.getState() == ConversionJobState.CANCELED;
    }

    @Transactional(readOnly = true)
    public Path resultPath(String ownerId, String id) {
        ConversionJob job = findOwned(ownerId, id);
        if (job.getState() != ConversionJobState.SUCCEEDED || job.getResultPath() == null) {
            throw new JobConflictException("A result is available only after the job succeeds.");
        }
        Path path = Path.of(job.getResultPath()).toAbsolutePath().normalize();
        if (!path.startsWith(jobsRoot) || Files.isSymbolicLink(path) || !Files.isRegularFile(path)) {
            throw new JobNotFoundException();
        }
        return path;
    }

    @Transactional
    public boolean succeed(String id, Path result, long size) {
        ConversionJob job = repository.findById(id).orElseThrow(JobNotFoundException::new);
        job.succeed(result.toString(), size, Instant.now());
        return job.getState() == ConversionJobState.SUCCEEDED;
    }

    @Transactional
    public void fail(String id, String code, String message) {
        ConversionJob job = repository.findById(id).orElseThrow(JobNotFoundException::new);
        job.fail(code, message, Instant.now());
    }

    public Map<String, String> options(ConversionJob job) throws JsonProcessingException {
        return objectMapper.readValue(job.getOptionsJson(), objectMapper.getTypeFactory()
                .constructMapType(LinkedHashMap.class, String.class, String.class));
    }

    public ConverterModule converterFor(ConversionJob job) {
        return converterRegistry.findById(job.getConverterId())
                .orElseThrow(() -> new UnsupportedConversionException(job.getSourceFormat(), job.getTargetFormat()));
    }

    public Path attemptDirectory(ConversionJob job) throws IOException {
        Path base = jobsRoot.resolve(job.getId()).resolve("attempts").resolve(Integer.toString(job.getAttempt()))
                .toAbsolutePath().normalize();
        if (!base.startsWith(jobsRoot)) {
            throw new IOException("Invalid attempt storage path.");
        }
        Files.createDirectories(base.resolve("output"));
        return base;
    }

    public Path jobsRoot() {
        return jobsRoot;
    }

    private String serializeOptions(Map<String, String> options) {
        try {
            return objectMapper.writeValueAsString(new TreeMap<>(options));
        } catch (JsonProcessingException e) {
            throw new IllegalArgumentException("Conversion options could not be stored.", e);
        }
    }

    private static String normalizeIdempotencyKey(String value) {
        if (value == null || value.isBlank()) {
            return null;
        }
        String key = value.trim();
        if (key.length() > 200 || key.chars().anyMatch(Character::isISOControl)) {
            throw new IllegalArgumentException("Idempotency-Key must be at most 200 printable characters.");
        }
        return key;
    }

    private static String safeFileName(String original) {
        String raw = original == null ? "upload" : original.replace('\\', '/');
        String name = raw.substring(raw.lastIndexOf('/') + 1).replaceAll("[\\p{Cntrl}]", "_").trim();
        if (name.isBlank() || name.equals(".") || name.equals("..")) {
            name = "upload";
        }
        return name.length() <= 255 ? name : name.substring(name.length() - 255);
    }

    private static String fingerprint(String fileHash, String source, String target, String converter, String options) {
        return digest((fileHash + "\n" + source + "\n" + target + "\n" + converter + "\n" + options)
                .getBytes(java.nio.charset.StandardCharsets.UTF_8));
    }

    private static String digest(InputStream input) throws IOException {
        try (input) {
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            byte[] bytes = new byte[8192];
            int count;
            while ((count = input.read(bytes)) >= 0) {
                digest.update(bytes, 0, count);
            }
            return HexFormat.of().formatHex(digest.digest());
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException("SHA-256 is unavailable.", e);
        }
    }

    private static String digest(byte[] value) {
        try {
            return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(value));
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException("SHA-256 is unavailable.", e);
        }
    }

    private static void deleteTree(Path root) {
        if (!Files.exists(root)) {
            return;
        }
        try (var paths = Files.walk(root)) {
            paths.sorted(java.util.Comparator.reverseOrder()).forEach(path -> {
                try {
                    Files.deleteIfExists(path);
                } catch (IOException ignored) {
                }
            });
        } catch (IOException ignored) {
        }
    }
}
