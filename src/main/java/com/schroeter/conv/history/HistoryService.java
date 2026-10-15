package com.schroeter.conv.history;

import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.LocalDateTime;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.stream.Stream;
import java.util.zip.ZipEntry;
import java.util.zip.ZipOutputStream;

import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import com.schroeter.conv.session.ConversionSession;
import com.schroeter.conv.session.ConversionSessionRepository;
import com.schroeter.conv.upload.UploadRecord;
import com.schroeter.conv.upload.UploadRecordRepository;

@Service
public class HistoryService {

    private final UploadRecordRepository uploadRecordRepository;
    private final ConversionSessionRepository sessionRepository;

    public HistoryService(
            UploadRecordRepository uploadRecordRepository,
            ConversionSessionRepository sessionRepository) {
        this.uploadRecordRepository = uploadRecordRepository;
        this.sessionRepository = sessionRepository;
    }

    public Optional<Path> buildSessionArchive(String userId, String sessionId) throws IOException {
        Optional<ConversionSession> sessionOptional = sessionRepository.findBySessionIdAndUserId(sessionId, userId);
        if (sessionOptional.isEmpty()) {
            return Optional.empty();
        }

        ConversionSession session = sessionOptional.get();
        Path sessionDir = Path.of(session.getFolderPath()).toAbsolutePath().normalize();
        List<UploadRecord> records = uploadRecordRepository.findConvertedBySessionIdAndUserId(sessionId, userId);
        if (records.isEmpty()) {
            return Optional.empty();
        }

        List<UploadRecord> existing = new ArrayList<>();
        for (UploadRecord record : records) {
            if (record.getConvertedFilePath() == null || record.getConvertedFilePath().isBlank()) {
                continue;
            }
            Path converted = Path.of(record.getConvertedFilePath()).toAbsolutePath().normalize();
            if (!Files.exists(converted) || !Files.isRegularFile(converted)) {
                continue;
            }
            existing.add(record);
        }

        if (existing.isEmpty()) {
            return Optional.empty();
        }

        Files.createDirectories(sessionDir);
        Path archivePath = sessionDir.resolve("session-" + sessionId + "-converted.zip").normalize();
        if (!archivePath.startsWith(sessionDir)) {
            throw new IOException("Invalid archive path.");
        }

        try (ZipOutputStream zip = new ZipOutputStream(Files.newOutputStream(archivePath))) {
            for (UploadRecord record : existing) {
                Path converted = Path.of(record.getConvertedFilePath()).toAbsolutePath().normalize();
                String convertedName = record.getConvertedFileName() == null || record.getConvertedFileName().isBlank()
                        ? converted.getFileName().toString()
                        : record.getConvertedFileName();
                String entryName = record.getUploadId() + "_" + convertedName;
                zip.putNextEntry(new ZipEntry(entryName));
                try (InputStream in = Files.newInputStream(converted)) {
                    in.transferTo(zip);
                }
                zip.closeEntry();
            }
        }

        return Optional.of(archivePath);
    }

    public List<Map<String, Object>> listSessionEntries(String userId, String sessionId) {
        List<UploadRecord> records = uploadRecordRepository.findConvertedBySessionIdAndUserId(sessionId, userId);
        List<Map<String, Object>> entries = new ArrayList<>();
        for (UploadRecord record : records) {
            boolean available = false;
            Long resolvedConvertedSizeBytes = record.getConvertedSizeBytes();
            if (record.getConvertedFilePath() != null && !record.getConvertedFilePath().isBlank()) {
                Path converted = Path.of(record.getConvertedFilePath()).toAbsolutePath().normalize();
                available = Files.exists(converted) && Files.isRegularFile(converted);
                if (available && resolvedConvertedSizeBytes == null) {
                    try {
                        resolvedConvertedSizeBytes = Files.size(converted);
                    } catch (IOException ignored) {
                        // keep null when size cannot be resolved
                    }
                }
            }

            Map<String, Object> entry = new LinkedHashMap<>();
            entry.put("sessionId", record.getSession().getSessionId());
            entry.put("uploadId", record.getUploadId());
            entry.put("sourceFileName", record.getOriginalFileName());
            entry.put("sourceFormat", record.getOriginalFileType());
            entry.put("targetFormat", record.getConvertedFileFormat());
            entry.put("convertedFileName", record.getConvertedFileName());
            entry.put("convertedAt", record.getConvertedAt());
            entry.put("originalSizeBytes", record.getOriginalSizeBytes());
            entry.put("convertedSizeBytes", resolvedConvertedSizeBytes);
            entry.put("available", available);
            entry.put("downloadUrl", "/api/download?sessionId=" + record.getSession().getSessionId() + "&uploadId=" + record.getUploadId() + "&userId=" + urlEncode(userId));
            entry.put("previewUrl", "/api/preview?sessionId=" + record.getSession().getSessionId() + "&uploadId=" + record.getUploadId() + "&userId=" + urlEncode(userId));
            entries.add(entry);
        }
        return entries;
    }

    public Optional<PreviewResource> loadPreview(String userId, String sessionId, String uploadId) {
        Optional<UploadRecord> recordOptional = uploadRecordRepository.findByUploadIdAndSessionSessionIdAndSessionUserId(uploadId, sessionId, userId);
        if (recordOptional.isEmpty()) {
            return Optional.empty();
        }
        UploadRecord record = recordOptional.get();
        if (record.getConvertedFilePath() == null || record.getConvertedFilePath().isBlank()) {
            return Optional.empty();
        }
        Path converted = Path.of(record.getConvertedFilePath()).toAbsolutePath().normalize();
        if (!Files.exists(converted) || !Files.isRegularFile(converted)) {
            return Optional.empty();
        }

        String contentType = probeType(converted, record.getConvertedFileFormat());
        return Optional.of(new PreviewResource(converted, contentType));
    }

    @Transactional
    public boolean deleteSessionEntry(String userId, String sessionId, String uploadId) {
        Optional<UploadRecord> recordOptional = uploadRecordRepository.findByUploadIdAndSessionSessionIdAndSessionUserId(uploadId, sessionId, userId);
        if (recordOptional.isEmpty()) {
            return false;
        }

        UploadRecord record = recordOptional.get();
        deleteIfPresent(record.getStoredFilePath());
        deleteIfPresent(record.getConvertedFilePath());

        Path uploadDir = null;
        if (record.getConvertedFilePath() != null && !record.getConvertedFilePath().isBlank()) {
            uploadDir = Path.of(record.getConvertedFilePath()).toAbsolutePath().normalize().getParent();
        } else if (record.getStoredFilePath() != null && !record.getStoredFilePath().isBlank()) {
            uploadDir = Path.of(record.getStoredFilePath()).toAbsolutePath().normalize().getParent();
        }
        if (uploadDir != null) {
            deleteDirectoryIfEmpty(uploadDir);
        }

        uploadRecordRepository.delete(record);
        return true;
    }

    @Transactional
    public boolean deleteSession(String userId, String sessionId) {
        Optional<ConversionSession> sessionOptional = sessionRepository.findBySessionIdAndUserId(sessionId, userId);
        if (sessionOptional.isEmpty()) {
            return false;
        }

        List<UploadRecord> records = uploadRecordRepository.findConvertedBySessionIdAndUserId(sessionId, userId);
        for (UploadRecord record : records) {
            deleteIfPresent(record.getStoredFilePath());
            deleteIfPresent(record.getConvertedFilePath());

            Path uploadDir = null;
            if (record.getConvertedFilePath() != null && !record.getConvertedFilePath().isBlank()) {
                uploadDir = Path.of(record.getConvertedFilePath()).toAbsolutePath().normalize().getParent();
            } else if (record.getStoredFilePath() != null && !record.getStoredFilePath().isBlank()) {
                uploadDir = Path.of(record.getStoredFilePath()).toAbsolutePath().normalize().getParent();
            }
            if (uploadDir != null) {
                deleteDirectoryIfEmpty(uploadDir);
            }

            uploadRecordRepository.delete(record);
        }

        Path sessionDir = Path.of(sessionOptional.get().getFolderPath()).toAbsolutePath().normalize();
        deleteDirectoryRecursively(sessionDir);
        sessionRepository.deleteById(sessionId);
        return true;
    }

    @Transactional
    public int deleteSessions(String userId, List<String> sessionIds) {
        if (sessionIds == null || sessionIds.isEmpty()) {
            return 0;
        }
        Set<String> normalized = new LinkedHashSet<>();
        for (String value : sessionIds) {
            if (value == null) {
                continue;
            }
            String trimmed = value.trim();
            if (!trimmed.isBlank()) {
                normalized.add(trimmed);
            }
        }
        int deleted = 0;
        for (String sessionId : normalized) {
            if (deleteSession(userId, sessionId)) {
                deleted++;
            }
        }
        return deleted;
    }

    private String probeType(Path converted, String format) {
        try {
            String probed = Files.probeContentType(converted);
            if (probed != null && !probed.isBlank()) {
                return probed;
            }
        } catch (IOException ignored) {
            // fallback below
        }

        String normalized = format == null ? "" : format.trim().toLowerCase();
        return switch (normalized) {
            case "jpeg" -> "image/jpeg";
            case "png" -> "image/png";
            case "webp" -> "image/webp";
            case "gif" -> "image/gif";
            case "bmp" -> "image/bmp";
            case "tiff" -> "image/tiff";
            case "jxl" -> "image/jxl";
            case "mp4" -> "video/mp4";
            case "mkv" -> "video/x-matroska";
            case "mov" -> "video/quicktime";
            case "webm" -> "video/webm";
            case "avi" -> "video/x-msvideo";
            case "mp3" -> "audio/mpeg";
            case "wav" -> "audio/wav";
            case "flac" -> "audio/flac";
            case "ogg" -> "audio/ogg";
            case "aac" -> "audio/aac";
            case "m4a" -> "audio/mp4";
            default -> "application/octet-stream";
        };
    }

    public record PreviewResource(Path path, String contentType) {
    }

    public record SessionSummary(String sessionId, String userId, long convertedCount, LocalDateTime latestConvertedAt) {
        public Map<String, Object> toMap() {
            Map<String, Object> value = new LinkedHashMap<>();
            value.put("sessionId", sessionId);
            value.put("userId", userId);
            value.put("convertedCount", convertedCount);
            value.put("latestConvertedAt", latestConvertedAt);
            value.put("sessionDownloadUrl", "/api/history/session/download?sessionId=" + sessionId + "&userId=" + urlEncode(userId));
            value.put("sessionDetailsUrl", "/html/session.html?sessionId=" + sessionId + "&userId=" + urlEncode(userId));
            return value;
        }
    }

    private static String urlEncode(String value) {
        return java.net.URLEncoder.encode(value, java.nio.charset.StandardCharsets.UTF_8);
    }

    private static void deleteIfPresent(String rawPath) {
        if (rawPath == null || rawPath.isBlank()) {
            return;
        }
        try {
            Files.deleteIfExists(Path.of(rawPath).toAbsolutePath().normalize());
        } catch (IOException ignored) {
            // best-effort cleanup
        }
    }

    private static void deleteDirectoryIfEmpty(Path dir) {
        try {
            if (!Files.isDirectory(dir)) {
                return;
            }
            try (var entries = Files.list(dir)) {
                if (entries.findAny().isPresent()) {
                    return;
                }
            }
            Files.deleteIfExists(dir);
        } catch (IOException ignored) {
            // best-effort cleanup
        }
    }

    private static void deleteDirectoryRecursively(Path dir) {
        if (dir == null || !Files.exists(dir)) {
            return;
        }
        try (Stream<Path> stream = Files.walk(dir)) {
            stream.sorted((a, b) -> b.compareTo(a)).forEach(path -> {
                try {
                    Files.deleteIfExists(path);
                } catch (IOException ignored) {
                    // best-effort cleanup
                }
            });
        } catch (IOException ignored) {
            // best-effort cleanup
        }
    }
}
