package com.schroeter.conv.controller;

import java.io.IOException;
import java.io.InputStream;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.nio.file.StandardCopyOption;
import java.time.LocalDateTime;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.UUID;
import java.util.zip.ZipEntry;
import java.util.zip.ZipOutputStream;
import java.util.stream.Collectors;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.core.io.InputStreamResource;
import org.springframework.core.io.Resource;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.multipart.MultipartFile;

import com.schroeter.conv.converters.ConverterModule;
import com.schroeter.conv.converters.ConverterRegistry;
import com.schroeter.conv.converters.ConverterRequest;
import com.schroeter.conv.conversion.ConversionOptionsService;
import com.schroeter.conv.config.UserConfigService;
import com.schroeter.conv.session.ConversionSession;
import com.schroeter.conv.session.ConversionSessionRepository;
import com.schroeter.conv.upload.UploadRecord;
import com.schroeter.conv.upload.UploadRecordRepository;
import com.schroeter.conv.auth.AuthService;

import jakarta.servlet.http.HttpServletRequest;

@RestController
public class UploadController {

    private static final Logger log = LoggerFactory.getLogger(UploadController.class);

    private final ConversionSessionRepository sessionRepository;
    private final UploadRecordRepository uploadRecordRepository;
    private final ConversionOptionsService conversionOptionsService;
    private final ConverterRegistry converterRegistry;
    private final AuthService authService;
    private final UserConfigService userConfigService;

    public UploadController(
            ConversionSessionRepository sessionRepository,
            UploadRecordRepository uploadRecordRepository,
            ConversionOptionsService conversionOptionsService,
            ConverterRegistry converterRegistry,
            AuthService authService,
            UserConfigService userConfigService) {
        this.sessionRepository = sessionRepository;
        this.uploadRecordRepository = uploadRecordRepository;
        this.conversionOptionsService = conversionOptionsService;
        this.converterRegistry = converterRegistry;
        this.authService = authService;
        this.userConfigService = userConfigService;
    }

    @PostMapping("/api/session/start")
    public ResponseEntity<Map<String, Object>> startSession(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            HttpServletRequest request) {
        userId = resolveUserId(userId, request);
        Map<String, Object> response = new LinkedHashMap<>();

        String sessionId = newSessionId();
        String uploadDir = userConfigService.uploadDir();
        Path destinationDir = Path.of(uploadDir).toAbsolutePath().normalize().resolve(sessionId).normalize();

        try {
            Files.createDirectories(destinationDir);

            ConversionSession session = new ConversionSession(
                    sessionId,
                    LocalDateTime.now(),
                    userId,
                    destinationDir.toString());
            sessionRepository.save(session);

            response.put("ok", true);
            response.put("sessionId", sessionId);
            response.put("sessionStartTime", session.getSessionStartTime());
            response.put("userId", session.getUserId());
            response.put("folderPath", session.getFolderPath());
            log.info("session-start user={} sessionId={} folder={}", userId, sessionId, destinationDir);
            return ResponseEntity.ok(response);
        } catch (IOException e) {
            response.put("ok", false);
            response.put("message", "Session creation failed: " + e.getMessage());
            log.warn("session-start failed user={} sessionId={} reason={}", userId, sessionId, e.getMessage());
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).body(response);
        }
    }

    @PostMapping("/api/upload")
    public ResponseEntity<Map<String, Object>> upload(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam("sessionId") String sessionId,
            @RequestParam("targetFormat") String targetFormat,
            @RequestParam(value = "converterId", required = false) String converterId,
            @RequestParam Map<String, String> requestParams,
            @RequestParam("file") MultipartFile file,
            HttpServletRequest request) {
        userId = resolveUserId(userId, request);
        Map<String, Object> response = new LinkedHashMap<>();

        if (file.isEmpty()) {
            response.put("ok", false);
            response.put("message", "No file selected.");
            return ResponseEntity.badRequest().body(response);
        }

        Optional<ConversionSession> sessionOptional = sessionRepository.findBySessionIdAndUserId(sessionId, userId);
        if (sessionOptional.isEmpty()) {
            response.put("ok", false);
            response.put("message", "Unknown sessionId for user.");
            return ResponseEntity.status(HttpStatus.FORBIDDEN).body(response);
        }

        ConversionSession session = sessionOptional.get();
        String requestedTargetFormat = conversionOptionsService.normalizeFormat(targetFormat);

        try {
            Map<String, String> resolvedOptions = conversionOptionsService.resolveOptions(
                    requestedTargetFormat,
                    extractConversionOptions(requestParams));
            ConversionResult result = processSingleUpload(session, file, requestedTargetFormat, resolvedOptions, converterId);

            response.put("ok", true);
            response.put("message", "File uploaded and converted successfully.");
            response.put("sessionId", sessionId);
            response.put("uploadId", result.uploadId());
            response.put("convertedId", result.uploadId());
            response.put("fileName", result.fileName());
            response.put("fileType", result.sourceFormat());
            response.put("targetFormat", requestedTargetFormat);
            response.put("contentType", file.getContentType());
            response.put("bytes", file.getSize());
            response.put("savedTo", result.savedTo());
            response.put("convertedTo", result.convertedTo());
            response.put("appliedOptions", resolvedOptions);
            response.put("converterId", result.converterId());
            response.put("converterName", result.converterName());
            response.put("downloadUrl", "/api/download?sessionId="
                    + urlEncode(sessionId)
                    + "&uploadId="
                    + urlEncode(result.uploadId())
                    + "&userId="
                    + urlEncode(userId));
            log.info("convert-single ok user={} sessionId={} uploadId={} file={} {}->{} converter={}",
                    userId, sessionId, result.uploadId(), result.fileName(), result.sourceFormat(), requestedTargetFormat, result.converterId());
            return ResponseEntity.ok(response);
        } catch (IOException e) {
            response.put("ok", false);
            response.put("message", "Upload failed: " + e.getMessage());
            log.warn("convert-single failed user={} sessionId={} file={} target={} reason={}",
                    userId, sessionId, file.getOriginalFilename(), requestedTargetFormat, e.getMessage());
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).body(response);
        }
    }

    @PostMapping("/api/upload/batch")
    public ResponseEntity<Map<String, Object>> uploadBatch(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam("sessionId") String sessionId,
            @RequestParam("sourceFormat") String sourceFormat,
            @RequestParam("targetFormat") String targetFormat,
            @RequestParam(value = "converterId", required = false) String converterId,
            @RequestParam Map<String, String> requestParams,
            @RequestParam("files") MultipartFile[] files,
            HttpServletRequest request) {
        userId = resolveUserId(userId, request);
        Map<String, Object> response = new LinkedHashMap<>();

        if (files == null || files.length == 0) {
            response.put("ok", false);
            response.put("message", "No files selected.");
            return ResponseEntity.badRequest().body(response);
        }

        Optional<ConversionSession> sessionOptional = sessionRepository.findBySessionIdAndUserId(sessionId, userId);
        if (sessionOptional.isEmpty()) {
            response.put("ok", false);
            response.put("message", "Unknown sessionId for user.");
            return ResponseEntity.status(HttpStatus.FORBIDDEN).body(response);
        }

        ConversionSession session = sessionOptional.get();
        String normalizedSourceFormat = conversionOptionsService.normalizeFormat(sourceFormat);
        String requestedTargetFormat = conversionOptionsService.normalizeFormat(targetFormat);
        Map<String, String> resolvedOptions;

        try {
            resolvedOptions = conversionOptionsService.resolveOptions(
                    requestedTargetFormat,
                    extractConversionOptions(requestParams));
        } catch (Exception e) {
            response.put("ok", false);
            response.put("message", "Invalid conversion options: " + e.getMessage());
            return ResponseEntity.badRequest().body(response);
        }

        List<ConversionResult> successes = new ArrayList<>();
        List<String> failures = new ArrayList<>();

        for (MultipartFile file : files) {
            if (file == null || file.isEmpty()) {
                continue;
            }

            String originalName = file.getOriginalFilename();
            String safeName = originalName == null ? "uploaded-file" : Path.of(originalName).getFileName().toString();
            String detectedFormat = detectFileType(safeName, file.getContentType());
            if (!normalizedSourceFormat.equals(detectedFormat)) {
                continue;
            }

            try {
                ConversionResult result = processSingleUpload(session, file, requestedTargetFormat, resolvedOptions, converterId);
                successes.add(result);
            } catch (IOException e) {
                failures.add(safeName + ": " + e.getMessage());
            }
        }

        if (successes.isEmpty()) {
            response.put("ok", false);
            response.put("message", "No files were converted.");
            response.put("failures", failures);
            return ResponseEntity.badRequest().body(response);
        }

        String batchUploadId = newUploadId();
        try {
            writeBatchManifest(session, batchUploadId, successes);
        } catch (IOException e) {
            response.put("ok", false);
            response.put("message", "Could not prepare batch download: " + e.getMessage());
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).body(response);
        }

        response.put("ok", true);
        response.put("message", "Batch conversion finished.");
        response.put("sessionId", sessionId);
        response.put("uploadId", batchUploadId);
        response.put("convertedId", batchUploadId);
        response.put("sourceFormat", normalizedSourceFormat);
        response.put("targetFormat", requestedTargetFormat);
        response.put("successful", successes.size());
        response.put("failed", failures.size());
        response.put("failures", failures);
        response.put("downloadUrl", "/api/download/batch?sessionId="
                + urlEncode(sessionId)
                + "&uploadId="
                + urlEncode(batchUploadId)
                + "&userId="
                + urlEncode(userId));
        log.info("convert-batch ok user={} sessionId={} source={} target={} success={} failed={}",
                userId, sessionId, normalizedSourceFormat, requestedTargetFormat, successes.size(), failures.size());
        return ResponseEntity.ok(response);
    }

    @PostMapping("/api/batch/finalize")
    public ResponseEntity<Map<String, Object>> finalizeBatch(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam("sessionId") String sessionId,
            @RequestParam("sourceFormat") String sourceFormat,
            @RequestParam("targetFormat") String targetFormat,
            @RequestParam("uploadIds") List<String> uploadIds,
            HttpServletRequest request) {
        userId = resolveUserId(userId, request);
        Map<String, Object> response = new LinkedHashMap<>();

        Optional<ConversionSession> sessionOptional = sessionRepository.findBySessionIdAndUserId(sessionId, userId);
        if (sessionOptional.isEmpty()) {
            response.put("ok", false);
            response.put("message", "Unknown sessionId for user.");
            return ResponseEntity.status(HttpStatus.FORBIDDEN).body(response);
        }

        if (uploadIds == null || uploadIds.isEmpty()) {
            response.put("ok", false);
            response.put("message", "No upload IDs provided.");
            return ResponseEntity.badRequest().body(response);
        }

        ConversionSession session = sessionOptional.get();
        String normalizedSourceFormat = conversionOptionsService.normalizeFormat(sourceFormat);
        String normalizedTargetFormat = conversionOptionsService.normalizeFormat(targetFormat);

        List<ConversionResult> results = new ArrayList<>();
        List<String> missing = new ArrayList<>();

        for (String uploadId : uploadIds) {
            Optional<UploadRecord> recordOptional = uploadRecordRepository.findByUploadIdAndSessionSessionIdAndSessionUserId(uploadId, sessionId, userId);
            if (recordOptional.isEmpty()) {
                missing.add(uploadId);
                continue;
            }

            UploadRecord record = recordOptional.get();
            if (record.getConvertedFilePath() == null) {
                missing.add(uploadId);
                continue;
            }

            results.add(new ConversionResult(
                    record.getUploadId(),
                    record.getOriginalFileName(),
                    record.getOriginalFileType(),
                    record.getStoredFilePath(),
                    record.getConvertedFilePath(),
                    "",
                    ""));
        }

        if (results.isEmpty()) {
            response.put("ok", false);
            response.put("message", "No converted files available for ZIP.");
            response.put("missingUploadIds", missing);
            return ResponseEntity.badRequest().body(response);
        }

        String batchUploadId = newUploadId();
        try {
            writeBatchManifest(session, batchUploadId, results);
        } catch (IOException e) {
            response.put("ok", false);
            response.put("message", "Could not prepare batch download: " + e.getMessage());
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).body(response);
        }

        response.put("ok", true);
        response.put("message", "Batch finalized.");
        response.put("sessionId", sessionId);
        response.put("uploadId", batchUploadId);
        response.put("convertedId", batchUploadId);
        response.put("sourceFormat", normalizedSourceFormat);
        response.put("targetFormat", normalizedTargetFormat);
        response.put("successful", results.size());
        response.put("missingUploadIds", missing);
        response.put("downloadUrl", "/api/download/batch?sessionId="
                + urlEncode(sessionId)
                + "&uploadId="
                + urlEncode(batchUploadId)
                + "&userId="
                + urlEncode(userId));
        return ResponseEntity.ok(response);
    }

    @GetMapping("/api/formats")
    public ResponseEntity<Map<String, Object>> supportedFormats(
            @RequestParam("sourceFormat") String sourceFormat,
            @RequestParam(value = "converterId", required = false) String converterId) {
        Map<String, Object> response = new LinkedHashMap<>();
        String normalizedSourceFormat = conversionOptionsService.normalizeFormat(sourceFormat);
        List<String> targets = new ArrayList<>(converterRegistry.targetFormatsFor(normalizedSourceFormat, converterId));

        response.put("ok", true);
        response.put("sourceFormat", normalizedSourceFormat);
        response.put("targetFormats", targets);
        return ResponseEntity.ok(response);
    }

    @GetMapping("/api/options")
    public ResponseEntity<Map<String, Object>> conversionOptions(
            @RequestParam("targetFormat") String targetFormat,
            @RequestParam(value = "sourceWidth", required = false) Integer sourceWidth,
            @RequestParam(value = "sourceHeight", required = false) Integer sourceHeight,
            @RequestParam(value = "mode", required = false, defaultValue = "default") String mode) {
        String normalizedTargetFormat = conversionOptionsService.normalizeFormat(targetFormat);
        return ResponseEntity.ok(conversionOptionsService.getOptionsPayload(normalizedTargetFormat, sourceWidth, sourceHeight, mode));
    }

    @GetMapping("/api/download")
    public ResponseEntity<Resource> downloadConvertedFile(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam("sessionId") String sessionId,
            @RequestParam("uploadId") String uploadId,
            HttpServletRequest request) throws IOException {
        userId = resolveUserId(userId, request);
        Optional<UploadRecord> uploadOptional = uploadRecordRepository.findByUploadIdAndSessionSessionIdAndSessionUserId(uploadId, sessionId, userId);
        if (uploadOptional.isEmpty()) {
            return ResponseEntity.notFound().build();
        }

        UploadRecord uploadRecord = uploadOptional.get();
        if (uploadRecord.getConvertedFilePath() == null) {
            return ResponseEntity.status(HttpStatus.BAD_REQUEST).build();
        }

        Path convertedPath = Path.of(uploadRecord.getConvertedFilePath()).toAbsolutePath().normalize();
        if (!Files.exists(convertedPath) || !Files.isRegularFile(convertedPath)) {
            return ResponseEntity.notFound().build();
        }

        InputStreamResource resource = new InputStreamResource(Files.newInputStream(convertedPath));
        return ResponseEntity.ok()
                .header(HttpHeaders.CONTENT_DISPOSITION,
                        "attachment; filename=\"" + uploadRecord.getConvertedFileName() + "\"")
                .contentType(MediaType.APPLICATION_OCTET_STREAM)
                .body(resource);
    }

    @GetMapping("/api/download/batch")
    public ResponseEntity<Resource> downloadBatchZip(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam("sessionId") String sessionId,
            @RequestParam("uploadId") String uploadId,
            HttpServletRequest request) throws IOException {
        userId = resolveUserId(userId, request);
        Optional<ConversionSession> sessionOptional = sessionRepository.findBySessionIdAndUserId(sessionId, userId);
        if (sessionOptional.isEmpty()) {
            return ResponseEntity.notFound().build();
        }

        Path sessionDir = Path.of(sessionOptional.get().getFolderPath()).toAbsolutePath().normalize();
        Path zipPath = sessionDir.resolve("batch-" + uploadId + ".zip").normalize();
        if (!zipPath.startsWith(sessionDir)) {
            return ResponseEntity.notFound().build();
        }

        if (!Files.exists(zipPath)) {
            Path manifestPath = sessionDir.resolve("batch-" + uploadId + ".manifest").normalize();
            if (!manifestPath.startsWith(sessionDir) || !Files.exists(manifestPath) || !Files.isRegularFile(manifestPath)) {
                return ResponseEntity.notFound().build();
            }

            createBatchZipFromManifest(zipPath, manifestPath, sessionDir);
        }

        if (!Files.isRegularFile(zipPath)) {
            return ResponseEntity.notFound().build();
        }

        InputStreamResource resource = new InputStreamResource(Files.newInputStream(zipPath));
        return ResponseEntity.ok()
                .header(HttpHeaders.CONTENT_DISPOSITION, "attachment; filename=\"batch-" + uploadId + ".zip\"")
                .contentType(MediaType.APPLICATION_OCTET_STREAM)
                .body(resource);
    }

    @GetMapping("/api/ui-config")
    public ResponseEntity<Map<String, Object>> uiConfig() {
        Map<String, Object> response = new LinkedHashMap<>();
        response.put("ok", true);
        response.put("showNerdStatsDefault", userConfigService.showNerdStatsDefault());
        response.put("preserveOptionsOnTargetChange", userConfigService.preserveOptionsOnTargetChange());
        List<Map<String, Object>> converters = new ArrayList<>();
        for (ConverterModule converter : converterRegistry.getActiveConverters()) {
            Map<String, Object> entry = new LinkedHashMap<>();
            entry.put("id", converter.id());
            entry.put("friendlyName", converter.friendlyName());
            converters.add(entry);
        }
        response.put("converters", converters);
        response.put("defaultConverterId", converters.isEmpty() ? "" : converters.get(0).get("id"));
        return ResponseEntity.ok(response);
    }

    private String newSessionId() {
        String sessionId;
        do {
            sessionId = UUID.randomUUID().toString();
        } while (sessionRepository.existsById(sessionId));
        return sessionId;
    }

    private String newUploadId() {
        String uploadId;
        do {
            uploadId = UUID.randomUUID().toString();
        } while (uploadRecordRepository.existsById(uploadId));
        return uploadId;
    }

    private String detectFileType(String fileName, String contentType) {
        int lastDot = fileName.lastIndexOf('.');
        if (lastDot >= 0 && lastDot < fileName.length() - 1) {
            return conversionOptionsService.normalizeFormat(fileName.substring(lastDot + 1));
        }
        if (contentType != null && !contentType.isBlank()) {
            int slash = contentType.indexOf('/');
            if (slash >= 0 && slash < contentType.length() - 1) {
                return conversionOptionsService.normalizeFormat(contentType.substring(slash + 1));
            }
        }
        return "unknown";
    }

    private boolean isValidFormat(String sourceFormat, String targetFormat) {
        return converterRegistry.targetFormatsFor(sourceFormat, null).contains(targetFormat);
    }

    private String buildConvertedFileName(String originalFileName, String targetFormat) {
        int lastDot = originalFileName.lastIndexOf('.');
        String base = lastDot > 0 ? originalFileName.substring(0, lastDot) : originalFileName;
        String originalExt = lastDot > 0 && lastDot < originalFileName.length() - 1
                ? conversionOptionsService.normalizeFormat(originalFileName.substring(lastDot + 1))
                : "";
        String normalizedTarget = conversionOptionsService.normalizeFormat(targetFormat);

        if (!originalExt.isBlank() && originalExt.equals(normalizedTarget)) {
            return base + "-compressed." + normalizedTarget;
        }
        return base + "." + normalizedTarget;
    }

    private ConversionResult processSingleUpload(
            ConversionSession session,
            MultipartFile file,
            String requestedTargetFormat,
            Map<String, String> resolvedOptions,
            String preferredConverterId) throws IOException {
        String uploadId = newUploadId();
        String originalName = file.getOriginalFilename();
        String safeName = originalName == null ? "uploaded-file" : Path.of(originalName).getFileName().toString();
        String sourceFormat = detectFileType(safeName, file.getContentType());

        if (!isValidFormat(sourceFormat, requestedTargetFormat)) {
            throw new IOException("Invalid target format for source format: " + sourceFormat);
        }

        Path sessionDir = Path.of(session.getFolderPath()).toAbsolutePath().normalize();
        Path uploadDirPath = sessionDir.resolve(uploadId).normalize();
        Path destinationFile = uploadDirPath.resolve(safeName).normalize();
        Path convertedFile = uploadDirPath.resolve(buildConvertedFileName(safeName, requestedTargetFormat)).normalize();

        if (!uploadDirPath.startsWith(sessionDir)
                || !destinationFile.startsWith(uploadDirPath)
                || !convertedFile.startsWith(uploadDirPath)) {
            throw new IOException("Invalid destination path.");
        }

        Files.createDirectories(uploadDirPath);
        try (InputStream inputStream = file.getInputStream()) {
            Files.copy(inputStream, destinationFile, StandardCopyOption.REPLACE_EXISTING);
        }

        UploadRecord uploadRecord = new UploadRecord(
                uploadId,
                session,
                safeName,
                sourceFormat,
                file.getContentType(),
                file.getSize(),
                LocalDateTime.now(),
                safeName,
                destinationFile.toString());

        List<ConverterModule> candidates = converterRegistry.resolveCandidates(sourceFormat, requestedTargetFormat, preferredConverterId);
        if (candidates.isEmpty()) {
            throw new IOException("No converter available for " + sourceFormat + " -> " + requestedTargetFormat);
        }
        IOException lastError = null;
        ConverterModule successfulConverter = null;
        for (ConverterModule candidate : candidates) {
            try {
                candidate.convert(new ConverterRequest(
                        destinationFile,
                        convertedFile,
                        sourceFormat,
                        requestedTargetFormat,
                        resolvedOptions));
                successfulConverter = candidate;
                break;
            } catch (IOException e) {
                lastError = e;
                log.warn("converter-attempt failed converter={} source={} target={} file={} reason={}",
                        candidate.id(), sourceFormat, requestedTargetFormat, safeName, e.getMessage());
            }
        }

        if (successfulConverter == null) {
            String attempted = candidates.stream().map(ConverterModule::id).collect(Collectors.joining(", "));
            String details = lastError == null ? "unknown" : lastError.getMessage();
            throw new IOException("All converters failed (" + attempted + "): " + details);
        }

        try {
            Files.deleteIfExists(destinationFile);
        } catch (IOException e) {
            throw new IOException("Converted file created, but original upload could not be deleted: " + e.getMessage(), e);
        }
        long convertedSizeBytes = Files.size(convertedFile);
        uploadRecord.markConverted(
                uploadId,
                requestedTargetFormat,
                convertedFile.getFileName().toString(),
                convertedFile.toString(),
                convertedSizeBytes,
                LocalDateTime.now());
        uploadRecordRepository.save(uploadRecord);

        return new ConversionResult(uploadId, safeName, sourceFormat, destinationFile.toString(), convertedFile.toString(), successfulConverter.id(), successfulConverter.friendlyName());
    }

    private void createBatchZip(Path batchZipPath, List<ConversionResult> results) throws IOException {
        Files.createDirectories(batchZipPath.getParent());

        try (ZipOutputStream zip = new ZipOutputStream(Files.newOutputStream(batchZipPath))) {
            for (ConversionResult result : results) {
                Path convertedPath = Path.of(result.convertedTo()).toAbsolutePath().normalize();
                String entryName = result.uploadId() + "_" + convertedPath.getFileName();
                zip.putNextEntry(new ZipEntry(entryName));
                Files.copy(convertedPath, zip);
                zip.closeEntry();
            }
        }
    }

    private void writeBatchManifest(ConversionSession session, String batchUploadId, List<ConversionResult> results) throws IOException {
        Path sessionDir = Path.of(session.getFolderPath()).toAbsolutePath().normalize();
        Path manifestPath = sessionDir.resolve("batch-" + batchUploadId + ".manifest").normalize();
        if (!manifestPath.startsWith(sessionDir)) {
            throw new IOException("Invalid manifest path.");
        }

        List<String> lines = new ArrayList<>();
        for (ConversionResult result : results) {
            lines.add(result.uploadId() + "|" + result.convertedTo());
        }

        Files.write(
                manifestPath,
                lines,
                StandardCharsets.UTF_8,
                StandardOpenOption.CREATE,
                StandardOpenOption.TRUNCATE_EXISTING,
                StandardOpenOption.WRITE);
    }

    private void createBatchZipFromManifest(Path zipPath, Path manifestPath, Path sessionDir) throws IOException {
        List<String> lines = Files.readAllLines(manifestPath, StandardCharsets.UTF_8);
        List<ConversionResult> results = new ArrayList<>();

        for (String line : lines) {
            if (line == null || line.isBlank()) {
                continue;
            }
            String[] parts = line.split("\\|", 2);
            if (parts.length != 2) {
                continue;
            }
            String uploadId = parts[0];
            Path convertedPath = Path.of(parts[1]).toAbsolutePath().normalize();
            if (!convertedPath.startsWith(sessionDir) || !Files.exists(convertedPath) || !Files.isRegularFile(convertedPath)) {
                continue;
            }
            results.add(new ConversionResult(uploadId, convertedPath.getFileName().toString(), "", "", convertedPath.toString(), "", ""));
        }

        if (results.isEmpty()) {
            throw new IOException("No converted files found for this batch.");
        }

        createBatchZip(zipPath, results);
    }

    private String urlEncode(String value) {
        return URLEncoder.encode(value, StandardCharsets.UTF_8);
    }

    private Map<String, String> extractConversionOptions(Map<String, String> requestParams) {
        Map<String, String> options = new HashMap<>();
        for (Map.Entry<String, String> entry : requestParams.entrySet()) {
            if (entry.getKey().startsWith("option_")) {
                options.put(entry.getKey().substring("option_".length()), entry.getValue());
            }
        }
        return options;
    }

    private String resolveUserId(String requestedUserId, HttpServletRequest request) {
        return authService.resolveUserId(request)
                .orElseGet(() -> {
                    String normalized = requestedUserId == null ? "" : requestedUserId.trim();
                    return normalized.isBlank() ? "anonymous" : normalized;
                });
    }

    private record ConversionResult(
            String uploadId,
            String fileName,
            String sourceFormat,
            String savedTo,
            String convertedTo,
            String converterId,
            String converterName) {
    }
}
