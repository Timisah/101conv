package com.schroeter.conv.controller;

import java.util.LinkedHashMap;
import java.util.Map;

import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import com.schroeter.conv.auth.AuthService;
import com.schroeter.conv.cleanup.CleanupService;
import com.schroeter.conv.config.GeneralSettingsService;
import com.schroeter.conv.config.UserConfigService;

import jakarta.servlet.http.HttpServletRequest;

@RestController
public class GeneralSettingsController {

    private final GeneralSettingsService generalSettingsService;
    private final CleanupService cleanupService;
    private final AuthService authService;
    private final UserConfigService userConfigService;

    public GeneralSettingsController(
            GeneralSettingsService generalSettingsService,
            CleanupService cleanupService,
            AuthService authService,
            UserConfigService userConfigService) {
        this.generalSettingsService = generalSettingsService;
        this.cleanupService = cleanupService;
        this.authService = authService;
        this.userConfigService = userConfigService;
    }

    @GetMapping("/api/settings/general")
    public ResponseEntity<Map<String, Object>> getSettings(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            HttpServletRequest request) {
        userId = resolveUserId(userId, request);
        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("ok", true);
        payload.put("settings", generalSettingsService.getSettings(userId).toMap());
        payload.put("userConfig", userConfigService.toMap());
        return ResponseEntity.ok(payload);
    }

    @PostMapping("/api/settings/general")
    public ResponseEntity<Map<String, Object>> saveSettings(
            @RequestParam(value = "userId", defaultValue = "anonymous") String userId,
            @RequestParam(value = "retentionDays") int retentionDays,
            HttpServletRequest request) {
        userId = resolveUserId(userId, request);
        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("ok", true);
        payload.put("settings", generalSettingsService.saveSettings(userId, retentionDays).toMap());
        payload.put("userConfig", userConfigService.toMap());
        payload.put("message", "General settings saved.");
        return ResponseEntity.ok(payload);
    }

    @PostMapping("/api/settings/cleanup/run")
    public ResponseEntity<Map<String, Object>> runCleanupNow() {
        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("ok", true);
        payload.put("cleanup", cleanupService.runCleanup().toMap());
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
