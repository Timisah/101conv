package com.schroeter.conv.config;

import java.util.LinkedHashMap;
import java.util.Map;

import org.springframework.stereotype.Service;

@Service
public class GeneralSettingsService {

    private final UserConfigService userConfigService;

    public GeneralSettingsService(UserConfigService userConfigService) {
        this.userConfigService = userConfigService;
    }

    public synchronized Settings getSettings(String userId) {
        String normalizedUserId = normalizeUserId(userId);
        return new Settings(normalizedUserId, userConfigService.authenticatedRetentionDays());
    }

    public synchronized Settings saveSettings(String userId, int retentionDays) {
        String normalizedUserId = normalizeUserId(userId);
        int sanitized = sanitizeRetentionDays(retentionDays);
        userConfigService.saveAuthenticatedRetentionDays(sanitized);
        return new Settings(normalizedUserId, sanitized);
    }

    public int retentionDaysForUser(String userId) {
        return isAnonymous(userId)
                ? userConfigService.anonymousRetentionDays()
                : userConfigService.authenticatedRetentionDays();
    }

    public String normalizeUserId(String userId) {
        if (userId == null || userId.isBlank()) {
            return "anonymous";
        }
        return userId.trim();
    }

    private int sanitizeRetentionDays(int value) {
        return Math.max(1, Math.min(value, 3650));
    }

    private boolean isAnonymous(String userId) {
        String normalized = normalizeUserId(userId).toLowerCase();
        return normalized.equals("anonymous") || normalized.startsWith("anonymous_");
    }

    public record Settings(String userId, int retentionDays) {
        public Map<String, Object> toMap() {
            Map<String, Object> values = new LinkedHashMap<>();
            values.put("userId", userId);
            values.put("retentionDays", retentionDays);
            return values;
        }
    }
}
