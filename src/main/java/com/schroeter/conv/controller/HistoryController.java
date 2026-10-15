package com.schroeter.conv.controller;

import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.nio.file.Files;
import java.nio.file.Path;

import org.springframework.data.domain.PageRequest;
import org.springframework.core.io.InputStreamResource;
import org.springframework.core.io.Resource;
import org.springframework.http.HttpHeaders;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import com.schroeter.conv.auth.AuthService;
import com.schroeter.conv.history.HistoryService;
import com.schroeter.conv.upload.UploadRecord;
import com.schroeter.conv.upload.UploadRecordRepository;
import com.schroeter.conv.upload.SessionSummaryProjection;

import jakarta.servlet.http.HttpServletRequest;

@RestController
public class HistoryController {

    private final UploadRecordRepository uploadRecordRepository;
    private final HistoryService historyService;
    private final AuthService authService;

    public HistoryController(
            UploadRecordRepository uploadRecordRepository,
            HistoryService historyService,
            AuthService authService) {
        this.uploadRecordRepository = uploadRecordRepository;
        this.historyService = historyService;
        this.authService = authService;
    }

    @GetMapping("/api/history")
    public ResponseEntity<Map<String, Object>> history(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam(value = "limit", defaultValue = "200") int limit,
            HttpServletRequest request) {
        userId = resolveUserId(userId, request);
        int sanitizedLimit = Math.max(1, Math.min(limit, 1000));

        List<UploadRecord> records = uploadRecordRepository.findConvertedWithSessionByUserId(userId, PageRequest.of(0, sanitizedLimit));
        List<Map<String, Object>> entries = new ArrayList<>();
        for (UploadRecord record : records) {
            boolean available = false;
            Long convertedSizeBytes = record.getConvertedSizeBytes();
            if (record.getConvertedFilePath() != null && !record.getConvertedFilePath().isBlank()) {
                Path convertedPath = Path.of(record.getConvertedFilePath()).toAbsolutePath().normalize();
                available = Files.exists(convertedPath) && Files.isRegularFile(convertedPath);
                if (available && convertedSizeBytes == null) {
                    try {
                        convertedSizeBytes = Files.size(convertedPath);
                    } catch (java.io.IOException ignored) {
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
            entry.put("convertedSizeBytes", convertedSizeBytes);
            entry.put("available", available);
            entry.put("downloadUrl", "/api/download?sessionId=" + record.getSession().getSessionId() + "&uploadId=" + record.getUploadId() + "&userId=" + userId);
            entry.put("previewUrl", "/api/preview?sessionId=" + record.getSession().getSessionId() + "&uploadId=" + record.getUploadId() + "&userId=" + userId);
            entries.add(entry);
        }

        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("ok", true);
        payload.put("count", entries.size());
        payload.put("entries", entries);
        return ResponseEntity.ok(payload);
    }

    @GetMapping("/api/history/sessions")
    public ResponseEntity<Map<String, Object>> sessions(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam(value = "limit", defaultValue = "200") int limit,
            HttpServletRequest request) {
        userId = resolveUserId(userId, request);
        int sanitizedLimit = Math.max(1, Math.min(limit, 1000));
        List<SessionSummaryProjection> rows = uploadRecordRepository.findSessionSummariesByUserId(userId, PageRequest.of(0, sanitizedLimit));

        List<Map<String, Object>> sessions = new ArrayList<>();
        for (SessionSummaryProjection row : rows) {
            Map<String, Object> session = new HistoryService.SessionSummary(
                    row.getSessionId(),
                    row.getUserId(),
                    row.getConvertedCount(),
                    row.getLatestConvertedAt()).toMap();

            LinkedHashSet<String> formats = new LinkedHashSet<>();
            long totalOriginalBytes = 0L;
            long totalConvertedBytes = 0L;
            for (Map<String, Object> entry : historyService.listSessionEntries(userId, row.getSessionId())) {
                Object targetFormat = entry.get("targetFormat");
                if (targetFormat != null) {
                    String normalized = String.valueOf(targetFormat).trim().toLowerCase();
                    if (!normalized.isBlank()) {
                        formats.add(normalized);
                    }
                }
                Object originalSize = entry.get("originalSizeBytes");
                if (originalSize instanceof Number n) {
                    totalOriginalBytes += Math.max(0L, n.longValue());
                }
                Object convertedSize = entry.get("convertedSizeBytes");
                if (convertedSize instanceof Number n) {
                    totalConvertedBytes += Math.max(0L, n.longValue());
                }
            }
            session.put("targetFormats", List.copyOf(formats));
            session.put("totalOriginalBytes", totalOriginalBytes);
            session.put("totalConvertedBytes", totalConvertedBytes);
            session.put("savedBytes", totalOriginalBytes - totalConvertedBytes);
            sessions.add(session);
        }

        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("ok", true);
        payload.put("count", sessions.size());
        payload.put("sessions", sessions);
        return ResponseEntity.ok(payload);
    }

    @GetMapping("/api/history/session")
    public ResponseEntity<Map<String, Object>> sessionEntries(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam("sessionId") String sessionId,
            HttpServletRequest request) {
        userId = resolveUserId(userId, request);
        List<Map<String, Object>> entries = historyService.listSessionEntries(userId, sessionId);
        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("ok", true);
        payload.put("sessionId", sessionId);
        payload.put("userId", userId);
        payload.put("count", entries.size());
        payload.put("entries", entries);
        return ResponseEntity.ok(payload);
    }

    @GetMapping("/api/history/session/download")
    public ResponseEntity<Resource> downloadSession(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam("sessionId") String sessionId,
            HttpServletRequest request) throws java.io.IOException {
        userId = resolveUserId(userId, request);
        var archiveOptional = historyService.buildSessionArchive(userId, sessionId);
        if (archiveOptional.isEmpty()) {
            return ResponseEntity.notFound().build();
        }
        var archivePath = archiveOptional.get();
        InputStreamResource resource = new InputStreamResource(java.nio.file.Files.newInputStream(archivePath));
        return ResponseEntity.ok()
                .header(HttpHeaders.CONTENT_DISPOSITION, "attachment; filename=\"" + archivePath.getFileName() + "\"")
                .contentType(MediaType.APPLICATION_OCTET_STREAM)
                .body(resource);
    }

    @GetMapping("/api/preview")
    public ResponseEntity<Resource> preview(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam("sessionId") String sessionId,
            @RequestParam("uploadId") String uploadId,
            HttpServletRequest request) throws java.io.IOException {
        userId = resolveUserId(userId, request);
        var previewOptional = historyService.loadPreview(userId, sessionId, uploadId);
        if (previewOptional.isEmpty()) {
            return ResponseEntity.notFound().build();
        }
        var preview = previewOptional.get();
        InputStreamResource resource = new InputStreamResource(java.nio.file.Files.newInputStream(preview.path()));
        MediaType mediaType;
        try {
            mediaType = MediaType.parseMediaType(preview.contentType());
        } catch (Exception e) {
            mediaType = MediaType.APPLICATION_OCTET_STREAM;
        }
        return ResponseEntity.ok()
                .contentType(mediaType)
                .body(resource);
    }

    @PostMapping("/api/history/session/entry/delete")
    public ResponseEntity<Map<String, Object>> deleteSessionEntry(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam("sessionId") String sessionId,
            @RequestParam("uploadId") String uploadId,
            HttpServletRequest request) {
        userId = resolveUserId(userId, request);
        boolean deleted = historyService.deleteSessionEntry(userId, sessionId, uploadId);
        if (!deleted) {
            return ResponseEntity.notFound().build();
        }
        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("ok", true);
        payload.put("sessionId", sessionId);
        payload.put("uploadId", uploadId);
        return ResponseEntity.ok(payload);
    }

    @PostMapping("/api/history/session/delete")
    public ResponseEntity<Map<String, Object>> deleteSession(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam("sessionId") String sessionId,
            HttpServletRequest request) {
        userId = resolveUserId(userId, request);
        boolean deleted = historyService.deleteSession(userId, sessionId);
        if (!deleted) {
            return ResponseEntity.notFound().build();
        }
        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("ok", true);
        payload.put("sessionId", sessionId);
        return ResponseEntity.ok(payload);
    }

    @PostMapping("/api/history/sessions/delete")
    public ResponseEntity<Map<String, Object>> deleteSessions(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam(value = "sessionIds") List<String> sessionIds,
            HttpServletRequest request) {
        userId = resolveUserId(userId, request);
        int deletedCount = historyService.deleteSessions(userId, sessionIds);
        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("ok", true);
        payload.put("deletedCount", deletedCount);
        payload.put("requestedCount", sessionIds == null ? 0 : sessionIds.size());
        return ResponseEntity.ok(payload);
    }

    private String resolveUserId(String requestedUserId, HttpServletRequest request) {
        return authService.resolveUserId(request)
                .orElseGet(() -> {
                    String normalized = requestedUserId == null ? "" : requestedUserId.trim();
                    return normalized.isBlank() ? "anonymous" : normalized;
                });
    }
}
