package com.schroeter.conv.cleanup;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.LocalDateTime;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.stream.Stream;

import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import com.schroeter.conv.config.GeneralSettingsService;
import com.schroeter.conv.session.ConversionSessionRepository;
import com.schroeter.conv.upload.UploadRecord;
import com.schroeter.conv.upload.UploadRecordRepository;

@Service
public class CleanupService {

    private final UploadRecordRepository uploadRecordRepository;
    private final ConversionSessionRepository sessionRepository;
    private final GeneralSettingsService generalSettingsService;

    public CleanupService(
            UploadRecordRepository uploadRecordRepository,
            ConversionSessionRepository sessionRepository,
            GeneralSettingsService generalSettingsService) {
        this.uploadRecordRepository = uploadRecordRepository;
        this.sessionRepository = sessionRepository;
        this.generalSettingsService = generalSettingsService;
    }

    @Scheduled(cron = "${cleanup.cron:0 30 3 * * *}")
    public void runScheduledCleanup() {
        runCleanup();
    }

    @Transactional
    public CleanupSummary runCleanup() {
        LocalDateTime now = LocalDateTime.now();
        List<UploadRecord> convertedRecords = uploadRecordRepository.findAllConvertedWithSession();
        List<UploadRecord> expired = new ArrayList<>();
        for (UploadRecord record : convertedRecords) {
            int userRetentionDays = generalSettingsService.retentionDaysForUser(record.getSession().getUserId());
            LocalDateTime userCutoff = now.minusDays(userRetentionDays);
            if (record.getConvertedAt() != null && record.getConvertedAt().isBefore(userCutoff)) {
                expired.add(record);
            }
        }
        Set<String> touchedSessionIds = new LinkedHashSet<>();

        int deletedRecords = 0;
        int deletedConvertedFiles = 0;
        int deletedUploadDirs = 0;
        int deletedSessionDirs = 0;
        int deletedSessions = 0;
        List<String> warnings = new ArrayList<>();

        for (UploadRecord record : expired) {
            touchedSessionIds.add(record.getSession().getSessionId());

            deletedConvertedFiles += deletePathIfExists(record.getConvertedFilePath(), warnings);
            deletePathIfExists(record.getStoredFilePath(), warnings);
            deletedUploadDirs += deleteParentDirectory(record.getConvertedFilePath(), record.getStoredFilePath(), warnings);

            uploadRecordRepository.delete(record);
            deletedRecords++;
        }

        for (String sessionId : touchedSessionIds) {
            if (uploadRecordRepository.countBySessionSessionId(sessionId) > 0) {
                continue;
            }
            var sessionOptional = sessionRepository.findById(sessionId);
            if (sessionOptional.isPresent()) {
                Path sessionPath = toPath(sessionOptional.get().getFolderPath());
                if (sessionPath != null && deleteDirectoryRecursively(sessionPath, warnings)) {
                    deletedSessionDirs++;
                }
            }
            sessionRepository.deleteById(sessionId);
            deletedSessions++;
        }

        return new CleanupSummary(
                -1,
                now,
                expired.size(),
                deletedRecords,
                deletedConvertedFiles,
                deletedUploadDirs,
                deletedSessionDirs,
                deletedSessions,
                warnings);
    }

    private int deletePathIfExists(String rawPath, List<String> warnings) {
        Path path = toPath(rawPath);
        if (path == null) {
            return 0;
        }
        try {
            boolean deleted = Files.deleteIfExists(path);
            return deleted ? 1 : 0;
        } catch (IOException e) {
            warnings.add("Could not delete file " + path + ": " + e.getMessage());
            return 0;
        }
    }

    private int deleteParentDirectory(String convertedPathRaw, String originalPathRaw, List<String> warnings) {
        Path candidate = null;
        Path convertedPath = toPath(convertedPathRaw);
        if (convertedPath != null) {
            candidate = convertedPath.getParent();
        }
        if (candidate == null) {
            Path originalPath = toPath(originalPathRaw);
            if (originalPath != null) {
                candidate = originalPath.getParent();
            }
        }
        if (candidate == null) {
            return 0;
        }
        return deleteDirectoryRecursively(candidate, warnings) ? 1 : 0;
    }

    private boolean deleteDirectoryRecursively(Path directory, List<String> warnings) {
        if (directory == null || !Files.exists(directory)) {
            return false;
        }
        try (Stream<Path> stream = Files.walk(directory)) {
            stream.sorted((a, b) -> b.compareTo(a)).forEach(path -> {
                try {
                    Files.deleteIfExists(path);
                } catch (IOException e) {
                    warnings.add("Could not delete path " + path + ": " + e.getMessage());
                }
            });
            return true;
        } catch (IOException e) {
            warnings.add("Could not list directory for deletion " + directory + ": " + e.getMessage());
            return false;
        }
    }

    private Path toPath(String rawPath) {
        if (rawPath == null || rawPath.isBlank()) {
            return null;
        }
        return Path.of(rawPath).toAbsolutePath().normalize();
    }

    public record CleanupSummary(
            int retentionDays,
            LocalDateTime cutoff,
            int matchedRecords,
            int deletedRecords,
            int deletedConvertedFiles,
            int deletedUploadDirs,
            int deletedSessionDirs,
            int deletedSessions,
            List<String> warnings) {
        public Map<String, Object> toMap() {
            Map<String, Object> values = new LinkedHashMap<>();
            values.put("retentionDays", retentionDays);
            values.put("cutoff", cutoff);
            values.put("matchedRecords", matchedRecords);
            values.put("deletedRecords", deletedRecords);
            values.put("deletedConvertedFiles", deletedConvertedFiles);
            values.put("deletedUploadDirs", deletedUploadDirs);
            values.put("deletedSessionDirs", deletedSessionDirs);
            values.put("deletedSessions", deletedSessions);
            values.put("warnings", warnings);
            return values;
        }
    }
}
