package com.schroeter.conv.config;

import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Properties;

import org.springframework.beans.factory.annotation.Value;
import org.springframework.stereotype.Service;

@Service
public class UserConfigService {

    private static final String DEFAULT_UPLOAD_DIR = "uploads";
    private static final String DEFAULT_H2_DB_PATH = "./src/main/resources/data/convdb";
    private static final String DEFAULT_CONVERSION_DEFAULTS_FILE = "conversion-defaults.properties";
    private static final String DEFAULT_CONVERTER_CONFIG_DIR = "converter-configs";
    private static final boolean DEFAULT_SHOW_NERD_STATS = false;
    private static final boolean DEFAULT_PRESERVE_OPTIONS_ON_TARGET_CHANGE = true;
    private static final String DEFAULT_CLEANUP_CRON = "0 30 3 * * *";
    private static final int DEFAULT_ANON_RETENTION_DAYS = 1;
    private static final int DEFAULT_AUTH_RETENTION_DAYS = 30;
    private static final int DEFAULT_SESSION_DAYS = 7;
    private static final boolean DEFAULT_ALLOW_REGISTRATION = true;

    private final Path configPath;

    public UserConfigService(@Value("${app.user-config-file:user-config.properties}") String configFile) {
        this.configPath = Path.of(configFile).toAbsolutePath().normalize();
        ensureConfigFile();
    }

    public synchronized int authenticatedRetentionDays() {
        return sanitizeDays(load().getProperty("cleanup.authenticatedRetentionDays"), DEFAULT_AUTH_RETENTION_DAYS);
    }

    public synchronized int anonymousRetentionDays() {
        return sanitizeDays(load().getProperty("cleanup.anonymousRetentionDays"), DEFAULT_ANON_RETENTION_DAYS);
    }

    public synchronized int authSessionDays() {
        return sanitizeDays(load().getProperty("auth.sessionDays"), DEFAULT_SESSION_DAYS);
    }

    public synchronized boolean allowRegistration() {
        String raw = load().getProperty("auth.allowRegistration");
        if (raw == null || raw.isBlank()) {
            return DEFAULT_ALLOW_REGISTRATION;
        }
        return Boolean.parseBoolean(raw.trim());
    }

    public synchronized String uploadDir() {
        return sanitizePath(load().getProperty("app.uploadDir"), DEFAULT_UPLOAD_DIR);
    }

    public synchronized String h2DbPath() {
        return sanitizePath(load().getProperty("db.h2Path"), DEFAULT_H2_DB_PATH);
    }

    public synchronized String conversionDefaultsFile() {
        return sanitizePath(load().getProperty("app.conversionDefaultsFile"), DEFAULT_CONVERSION_DEFAULTS_FILE);
    }

    public synchronized String converterConfigDir() {
        return sanitizePath(load().getProperty("app.converterConfigDir"), DEFAULT_CONVERTER_CONFIG_DIR);
    }

    public synchronized boolean showNerdStatsDefault() {
        String raw = load().getProperty("ui.showNerdStatsDefault");
        if (raw == null || raw.isBlank()) {
            return DEFAULT_SHOW_NERD_STATS;
        }
        return Boolean.parseBoolean(raw.trim());
    }

    public synchronized boolean preserveOptionsOnTargetChange() {
        String raw = load().getProperty("ui.preserveOptionsOnTargetChange");
        if (raw == null || raw.isBlank()) {
            return DEFAULT_PRESERVE_OPTIONS_ON_TARGET_CHANGE;
        }
        return Boolean.parseBoolean(raw.trim());
    }

    public synchronized String cleanupCron() {
        String raw = load().getProperty("cleanup.cron");
        if (raw == null || raw.isBlank()) {
            return DEFAULT_CLEANUP_CRON;
        }
        return raw.trim();
    }

    public synchronized void saveAuthenticatedRetentionDays(int days) {
        Properties props = load();
        props.setProperty("cleanup.authenticatedRetentionDays", String.valueOf(sanitizeDays(days, DEFAULT_AUTH_RETENTION_DAYS)));
        store(props);
    }

    public synchronized Map<String, Object> toMap() {
        Map<String, Object> values = new LinkedHashMap<>();
        values.put("uploadDir", uploadDir());
        values.put("h2DbPath", h2DbPath());
        values.put("conversionDefaultsFile", conversionDefaultsFile());
        values.put("converterConfigDir", converterConfigDir());
        values.put("showNerdStatsDefault", showNerdStatsDefault());
        values.put("preserveOptionsOnTargetChange", preserveOptionsOnTargetChange());
        values.put("cleanupCron", cleanupCron());
        values.put("allowRegistration", allowRegistration());
        values.put("sessionDays", authSessionDays());
        values.put("anonymousRetentionDays", anonymousRetentionDays());
        values.put("authenticatedRetentionDays", authenticatedRetentionDays());
        values.put("configFile", configPath.toString());
        return values;
    }

    private void ensureConfigFile() {
        try {
            Path parent = configPath.getParent();
            if (parent != null) {
                Files.createDirectories(parent);
            }
            if (!Files.exists(configPath)) {
                Files.writeString(configPath, defaultConfigText());
            }
        } catch (IOException e) {
            throw new IllegalStateException("Could not create config file: " + configPath, e);
        }
    }

    private Properties load() {
        Properties props = new Properties();
        if (!Files.exists(configPath)) {
            return props;
        }
        try (InputStream in = Files.newInputStream(configPath)) {
            props.load(in);
            return props;
        } catch (IOException e) {
            throw new IllegalStateException("Could not read config file: " + configPath, e);
        }
    }

    private void store(Properties props) {
        String content = """
# 101Conv user-facing configuration
# This file is read on every request for most options.
# Some startup-only options (like converter discovery path) may require restart.
#
# Base directories/files:
# Upload sessions and converted files are written under this directory.
app.uploadDir=%s
#
# H2 DB base path. Restart required after changing this.
db.h2Path=%s
#
# Conversion defaults properties file.
app.conversionDefaultsFile=%s
#
# Converter definition directory (JSON converter configs).
app.converterConfigDir=%s
#
# UI defaults:
ui.showNerdStatsDefault=%s
ui.preserveOptionsOnTargetChange=%s
#
# Cleanup schedule (Spring cron, seconds first).
cleanup.cron=%s
#
# Registration:
# true  = users can sign up
# false = signup disabled; existing users can still login
auth.allowRegistration=%s
#
# Auth session lifetime in days.
auth.sessionDays=%s
#
# Cleanup retention for anonymous conversions.
# Anonymous sessions are device-local and should not be stored long-term.
cleanup.anonymousRetentionDays=%s
#
# Cleanup retention for logged-in users.
cleanup.authenticatedRetentionDays=%s
""".formatted(
                pathValue(props, "app.uploadDir", DEFAULT_UPLOAD_DIR),
                pathValue(props, "db.h2Path", DEFAULT_H2_DB_PATH),
                pathValue(props, "app.conversionDefaultsFile", DEFAULT_CONVERSION_DEFAULTS_FILE),
                pathValue(props, "app.converterConfigDir", DEFAULT_CONVERTER_CONFIG_DIR),
                boolValue(props, "ui.showNerdStatsDefault", DEFAULT_SHOW_NERD_STATS),
                boolValue(props, "ui.preserveOptionsOnTargetChange", DEFAULT_PRESERVE_OPTIONS_ON_TARGET_CHANGE),
                stringValue(props, "cleanup.cron", DEFAULT_CLEANUP_CRON),
                boolValue(props, "auth.allowRegistration", DEFAULT_ALLOW_REGISTRATION),
                intValue(props, "auth.sessionDays", DEFAULT_SESSION_DAYS),
                intValue(props, "cleanup.anonymousRetentionDays", DEFAULT_ANON_RETENTION_DAYS),
                intValue(props, "cleanup.authenticatedRetentionDays", DEFAULT_AUTH_RETENTION_DAYS));
        try {
            Files.writeString(configPath, content);
        } catch (IOException e) {
            throw new IllegalStateException("Could not write config file: " + configPath, e);
        }
    }

    private static int sanitizeDays(String raw, int fallback) {
        if (raw == null || raw.isBlank()) {
            return fallback;
        }
        try {
            return sanitizeDays(Integer.parseInt(raw.trim()), fallback);
        } catch (NumberFormatException e) {
            return fallback;
        }
    }

    private static int sanitizeDays(int value, int fallback) {
        int normalized = value <= 0 ? fallback : value;
        return Math.max(1, Math.min(normalized, 3650));
    }

    private static int intValue(Properties props, String key, int fallback) {
        return sanitizeDays(props.getProperty(key), fallback);
    }

    private static String stringValue(Properties props, String key, String fallback) {
        String raw = props.getProperty(key);
        if (raw == null || raw.isBlank()) {
            return fallback;
        }
        return raw.trim();
    }

    private static String pathValue(Properties props, String key, String fallback) {
        return sanitizePath(props.getProperty(key), fallback);
    }

    private static boolean boolValue(Properties props, String key, boolean fallback) {
        String raw = props.getProperty(key);
        if (raw == null || raw.isBlank()) {
            return fallback;
        }
        return Boolean.parseBoolean(raw.trim());
    }

    private static String sanitizePath(String raw, String fallback) {
        String value = raw == null ? "" : raw.trim();
        if (value.isBlank()) {
            value = fallback;
        }
        return value.replace("\\", "/");
    }

    private static String defaultConfigText() {
        return """
# 101Conv user-facing configuration
# This file is read on every request for most options.
# Some startup-only options (like converter discovery path) may require restart.
#
# Base directories/files:
# Upload sessions and converted files are written under this directory.
app.uploadDir=uploads
#
# H2 DB base path. Restart required after changing this.
db.h2Path=./src/main/resources/data/convdb
#
# Conversion defaults properties file.
app.conversionDefaultsFile=conversion-defaults.properties
#
# Converter definition directory (JSON converter configs).
app.converterConfigDir=converter-configs
#
# UI defaults:
ui.showNerdStatsDefault=false
ui.preserveOptionsOnTargetChange=true
#
# Cleanup schedule (Spring cron, seconds first).
cleanup.cron=0 30 3 * * *
#
# Registration:
# true  = users can sign up
# false = signup disabled; existing users can still login
auth.allowRegistration=true
#
# Auth session lifetime in days.
auth.sessionDays=7
#
# Cleanup retention for anonymous conversions.
# Anonymous sessions are device-local and should not be stored long-term.
cleanup.anonymousRetentionDays=1
#
# Cleanup retention for logged-in users.
cleanup.authenticatedRetentionDays=30
""";
    }
}
