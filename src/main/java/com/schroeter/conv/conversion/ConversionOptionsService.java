package com.schroeter.conv.conversion;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Properties;
import java.util.Set;
import java.util.TreeSet;
import java.util.Locale;
import java.util.stream.Collectors;

import org.springframework.stereotype.Service;

import com.schroeter.conv.config.UserConfigService;
import com.schroeter.conv.converters.BoundedProcessRunner;

@Service
public class ConversionOptionsService {

    private static final Set<String> IMAGE_FORMATS = Set.of("jpeg", "png", "webp", "bmp", "gif", "tiff", "jxl");
    private static final Set<String> VIDEO_FORMATS = Set.of("mp4", "avi", "mkv", "mov", "webm");
    private static final Set<String> AUDIO_FORMATS = Set.of("mp3", "wav", "flac", "ogg", "aac", "m4a");
    private static final int DEFAULT_TARGET_SIZE_MB = 8;
    private static final int DEFAULT_VIDEO_AUDIO_BITRATE_K = 128;
    private static final int DEFAULT_CONVERT_AUDIO_BITRATE_K = 128;
    private static final List<String> VIDEO_RESOLUTION_CHOICES = List.of(
            "original", "3840x2160", "2560x1440", "1920x1080", "1280x720", "854x480", "640x360");

    private final UserConfigService userConfigService;
    private final BoundedProcessRunner processRunner;

    public ConversionOptionsService(UserConfigService userConfigService, BoundedProcessRunner processRunner) {
        this.userConfigService = userConfigService;
        this.processRunner = processRunner;
    }

    public Map<String, Object> getOptionsPayload(String targetFormat) {
        return getOptionsPayload(targetFormat, null, null, "default");
    }

    public Map<String, Object> getOptionsPayload(String targetFormat, Integer sourceWidth, Integer sourceHeight) {
        return getOptionsPayload(targetFormat, sourceWidth, sourceHeight, "default");
    }

    public Map<String, Object> getOptionsPayload(String targetFormat, Integer sourceWidth, Integer sourceHeight, String mode) {
        String normalizedTarget = normalizeFormat(targetFormat);
        Map<String, String> defaults = readDefaultsForFormat(normalizedTarget);
        List<OptionDefinition> definitions = optionDefinitionsForMode(normalizedTarget, mode, sourceWidth, sourceHeight);

        List<Map<String, Object>> options = new ArrayList<>();
        for (OptionDefinition definition : definitions) {
            Map<String, Object> option = new LinkedHashMap<>();
            option.put("key", definition.key());
            option.put("label", definition.label());
            option.put("type", definition.type());
            if (definition.min() != null) {
                option.put("min", definition.min());
            }
            if (definition.max() != null) {
                option.put("max", definition.max());
            }
            if (definition.step() != null) {
                option.put("step", definition.step());
            }
            if (!definition.choices().isEmpty()) {
                option.put("choices", definition.choices());
            }
            if ("resolution".equals(definition.key())) {
                option.put("originalLabel", originalResolutionLabel(sourceWidth, sourceHeight));
            }
            option.put("value", defaults.getOrDefault(definition.key(), definition.defaultValue()));
            options.add(option);
        }

        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("ok", true);
        payload.put("targetFormat", normalizedTarget);
        payload.put("options", options);
        return payload;
    }

    public Map<String, String> resolveOptions(String targetFormat, Map<String, String> requestedOptions) {
        String normalizedTarget = normalizeFormat(targetFormat);
        Map<String, String> resolved = new LinkedHashMap<>(readDefaultsForFormat(normalizedTarget));
        Set<String> allowedKeys = optionDefinitionsForFormat(normalizedTarget).stream().map(OptionDefinition::key).collect(java.util.stream.Collectors.toSet());

        for (Map.Entry<String, String> entry : requestedOptions.entrySet()) {
            if (!allowedKeys.contains(entry.getKey())) {
                continue;
            }
            if (entry.getValue() != null && !entry.getValue().isBlank()) {
                resolved.put(entry.getKey(), entry.getValue().trim());
            }
        }
        return resolved;
    }

    public synchronized Map<String, Object> getDefaultsEditorPayload() {
        Properties builtIn = defaultDefaultsProperties();
        Properties current = readCurrentPropertiesOrBuiltIn();

        List<Map<String, Object>> formats = new ArrayList<>();
        for (String format : allTargetFormats()) {
            List<OptionDefinition> definitions = optionDefinitionsForFormat(format);
            if (definitions.isEmpty()) {
                continue;
            }

            List<Map<String, Object>> options = new ArrayList<>();
            for (OptionDefinition definition : definitions) {
                String propertyKey = propertyKey(format, definition.key());
                String builtInValue = builtIn.getProperty(propertyKey, definition.defaultValue());
                String currentValue = current.getProperty(propertyKey, builtInValue);

                Map<String, Object> option = new LinkedHashMap<>();
                option.put("key", definition.key());
                option.put("label", definition.label());
                option.put("type", definition.type());
                option.put("currentValue", currentValue);
                option.put("defaultValue", builtInValue);
                if (definition.min() != null) {
                    option.put("min", definition.min());
                }
                if (definition.max() != null) {
                    option.put("max", definition.max());
                }
                if (definition.step() != null) {
                    option.put("step", definition.step());
                }
                if (!definition.choices().isEmpty()) {
                    option.put("choices", definition.choices());
                }
                options.add(option);
            }

            Map<String, Object> formatEntry = new LinkedHashMap<>();
            formatEntry.put("format", format);
            formatEntry.put("options", options);
            formats.add(formatEntry);
        }

        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("ok", true);
        payload.put("defaultsFilePath", defaultsFilePath().toString());
        payload.put("formats", formats);
        return payload;
    }

    public synchronized void saveDefaults(Map<String, String> valuesByPropertyKey) throws IOException {
        Path defaultsFilePath = defaultsFilePath();
        Properties builtIn = defaultDefaultsProperties();
        Properties current = readCurrentPropertiesOrBuiltIn();

        for (String format : allTargetFormats()) {
            for (OptionDefinition definition : optionDefinitionsForFormat(format)) {
                String propertyKey = propertyKey(format, definition.key());
                String incoming = valuesByPropertyKey.get(propertyKey);

                if (incoming == null || incoming.isBlank()) {
                    current.setProperty(propertyKey, builtIn.getProperty(propertyKey, definition.defaultValue()));
                    continue;
                }

                current.setProperty(propertyKey, incoming.trim());
            }
        }

        try (OutputStream out = Files.newOutputStream(defaultsFilePath)) {
            current.store(out, "User-editable conversion defaults");
        }
    }

    public List<String> ffmpegArgsFor(String targetFormat, Map<String, String> options, Path sourceFile, String ffprobeBin) {
        String normalizedTarget = normalizeFormat(targetFormat);
        List<String> args = new ArrayList<>();

        if (VIDEO_FORMATS.contains(normalizedTarget)) {
            String targetSizeMb = options.get("targetSizeMb");
            if (targetSizeMb != null && !targetSizeMb.isBlank()) {
                Integer totalKbps = bitrateForTargetSizeKbps(targetSizeMb, sourceFile, ffprobeBin);
                if (totalKbps != null) {
                    int videoKbps = Math.max(64, totalKbps - DEFAULT_VIDEO_AUDIO_BITRATE_K);
                    addIfPresent(args, "-b:v", withK(String.valueOf(videoKbps)));
                    addIfPresent(args, "-maxrate", withK(String.valueOf(videoKbps)));
                    addIfPresent(args, "-bufsize", withK(String.valueOf(Math.max(videoKbps * 2, 128))));
                    addIfPresent(args, "-b:a", withK(String.valueOf(DEFAULT_VIDEO_AUDIO_BITRATE_K)));
                }
            } else {
                addIfPresent(args, "-crf", options.get("crf"));
                addIfPresent(args, "-preset", options.get("preset"));
                addIfPresent(args, "-b:a", withK(options.get("audioBitrateK")));
            }
            String scaleFilter = scaleFilterArg(options.get("resolution"));
            if (scaleFilter != null) {
                addIfPresent(args, "-vf", scaleFilter);
            }
        } else if (AUDIO_FORMATS.contains(normalizedTarget)) {
            Integer audioKbps = bitrateForTargetSizeKbps(options.get("targetSizeMb"), sourceFile, ffprobeBin);
            if (audioKbps != null) {
                addIfPresent(args, "-b:a", withK(String.valueOf(clamp(audioKbps, 32, 320))));
            }
        } else if (IMAGE_FORMATS.contains(normalizedTarget)) {
            String quality = options.get("quality");
            if (quality != null) {
                if ("jpeg".equals(normalizedTarget)) {
                    addIfPresent(args, "-q:v", jpegQualityToQv(quality));
                } else if ("webp".equals(normalizedTarget)) {
                    addIfPresent(args, "-quality", quality);
                } else if ("png".equals(normalizedTarget)) {
                    addIfPresent(args, "-compression_level", pngQualityToCompressionLevel(quality));
                }
            }
        }
        return args;
    }

    public boolean isVideoFormat(String format) {
        return VIDEO_FORMATS.contains(normalizeFormat(format));
    }

    public String normalizeFormat(String rawFormat) {
        if (rawFormat == null) {
            return "unknown";
        }
        String normalized = rawFormat.trim().toLowerCase();
        if (normalized.startsWith(".")) {
            normalized = normalized.substring(1);
        }
        if (normalized.startsWith("x-")) {
            normalized = normalized.substring(2);
        }
        if ("jpg".equals(normalized)) {
            return "jpeg";
        }
        if ("mpeg".equals(normalized)) {
            return "mp3";
        }
        if ("x-m4a".equals(normalized) || "mp4a-latm".equals(normalized)) {
            return "m4a";
        }
        return normalized;
    }

    private Path defaultsFilePath() {
        return Path.of(userConfigService.conversionDefaultsFile()).toAbsolutePath().normalize();
    }

    private void ensureDefaultsFileExists() {
        Path defaultsFilePath = defaultsFilePath();
        try {
            if (Files.exists(defaultsFilePath)) {
                return;
            }
            Path parent = defaultsFilePath.getParent();
            if (parent != null) {
                Files.createDirectories(parent);
            }
            Properties properties = defaultDefaultsProperties();
            try (OutputStream out = Files.newOutputStream(defaultsFilePath)) {
                properties.store(out, "User-editable conversion defaults");
            }
        } catch (IOException e) {
            throw new IllegalStateException("Could not initialize defaults file: " + defaultsFilePath, e);
        }
    }

    private Properties readCurrentPropertiesOrBuiltIn() {
        Path defaultsFilePath = defaultsFilePath();
        Properties props = defaultDefaultsProperties();
        if (!Files.exists(defaultsFilePath)) {
            return props;
        }
        try (InputStream in = Files.newInputStream(defaultsFilePath)) {
            props.load(in);
        } catch (IOException ignored) {
            return defaultDefaultsProperties();
        }
        return props;
    }

    private Map<String, String> readDefaultsForFormat(String targetFormat) {
        Map<String, String> defaults = new LinkedHashMap<>();
        for (OptionDefinition definition : optionDefinitionsForFormat(targetFormat)) {
            defaults.put(definition.key(), definition.defaultValue());
        }

        Properties props = readCurrentPropertiesOrBuiltIn();

        for (OptionDefinition definition : optionDefinitionsForFormat(targetFormat)) {
            String key = targetFormat + "." + definition.key();
            String value = props.getProperty(key);
            if (value != null && !value.isBlank()) {
                defaults.put(definition.key(), value.trim());
            }
        }
        return defaults;
    }

    private List<OptionDefinition> optionDefinitionsForFormat(String targetFormat) {
        if (VIDEO_FORMATS.contains(targetFormat)) {
            return List.of(
                    new OptionDefinition("crf", "Video Quality (CRF, lower is better)", "number", 0, 51, 1, List.of(), "23"),
                    new OptionDefinition("preset", "Encoding Preset", "select", null, null, null,
                            List.of("ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"),
                            "medium"),
                    new OptionDefinition("audioBitrateK", "Audio Bitrate (kbps)", "number", 32, 512, 1, List.of(), String.valueOf(DEFAULT_CONVERT_AUDIO_BITRATE_K)),
                    new OptionDefinition("targetSizeMb", "Target Size (MB)", "number", 1, 10240, 1, List.of(), String.valueOf(DEFAULT_TARGET_SIZE_MB)),
                    new OptionDefinition("resolution", "Resolution", "select", null, null, null, VIDEO_RESOLUTION_CHOICES, "original"));
        }
        if (AUDIO_FORMATS.contains(targetFormat)) {
            return List.of(new OptionDefinition("targetSizeMb", "Target Size (MB)", "number", 1, 10240, 1, List.of(), String.valueOf(DEFAULT_TARGET_SIZE_MB)));
        }
        if (IMAGE_FORMATS.contains(targetFormat)) {
            return List.of(new OptionDefinition("quality", "Image Quality (1-100)", "number", 1, 100, 1, List.of(), "85"));
        }
        return List.of();
    }

    private Properties defaultDefaultsProperties() {
        Properties props = new Properties();
        props.setProperty("jpeg.quality", "85");
        props.setProperty("png.quality", "90");
        props.setProperty("webp.quality", "80");
        props.setProperty("bmp.quality", "90");
        props.setProperty("gif.quality", "90");
        props.setProperty("tiff.quality", "90");
        props.setProperty("jxl.quality", "85");
        props.setProperty("mp4.crf", "23");
        props.setProperty("mp4.preset", "medium");
        props.setProperty("mp4.audioBitrateK", String.valueOf(DEFAULT_CONVERT_AUDIO_BITRATE_K));
        props.setProperty("mp4.targetSizeMb", String.valueOf(DEFAULT_TARGET_SIZE_MB));
        props.setProperty("mp4.resolution", "original");
        props.setProperty("avi.crf", "23");
        props.setProperty("avi.preset", "medium");
        props.setProperty("avi.audioBitrateK", String.valueOf(DEFAULT_CONVERT_AUDIO_BITRATE_K));
        props.setProperty("avi.targetSizeMb", String.valueOf(DEFAULT_TARGET_SIZE_MB));
        props.setProperty("avi.resolution", "original");
        props.setProperty("mkv.crf", "23");
        props.setProperty("mkv.preset", "medium");
        props.setProperty("mkv.audioBitrateK", String.valueOf(DEFAULT_CONVERT_AUDIO_BITRATE_K));
        props.setProperty("mkv.targetSizeMb", String.valueOf(DEFAULT_TARGET_SIZE_MB));
        props.setProperty("mkv.resolution", "original");
        props.setProperty("mov.crf", "22");
        props.setProperty("mov.preset", "medium");
        props.setProperty("mov.audioBitrateK", "192");
        props.setProperty("mov.targetSizeMb", String.valueOf(DEFAULT_TARGET_SIZE_MB));
        props.setProperty("mov.resolution", "original");
        props.setProperty("webm.crf", "30");
        props.setProperty("webm.preset", "medium");
        props.setProperty("webm.audioBitrateK", String.valueOf(DEFAULT_CONVERT_AUDIO_BITRATE_K));
        props.setProperty("webm.targetSizeMb", String.valueOf(DEFAULT_TARGET_SIZE_MB));
        props.setProperty("webm.resolution", "original");
        props.setProperty("mp3.targetSizeMb", String.valueOf(DEFAULT_TARGET_SIZE_MB));
        props.setProperty("aac.targetSizeMb", String.valueOf(DEFAULT_TARGET_SIZE_MB));
        props.setProperty("m4a.targetSizeMb", String.valueOf(DEFAULT_TARGET_SIZE_MB));
        props.setProperty("ogg.targetSizeMb", String.valueOf(DEFAULT_TARGET_SIZE_MB));
        props.setProperty("wav.targetSizeMb", String.valueOf(DEFAULT_TARGET_SIZE_MB));
        props.setProperty("flac.targetSizeMb", String.valueOf(DEFAULT_TARGET_SIZE_MB));
        return props;
    }

    private List<String> allTargetFormats() {
        TreeSet<String> formats = new TreeSet<>();
        formats.addAll(IMAGE_FORMATS);
        formats.addAll(VIDEO_FORMATS);
        formats.addAll(AUDIO_FORMATS);
        return new ArrayList<>(formats);
    }

    private String propertyKey(String format, String optionKey) {
        return format + "." + optionKey;
    }

    private static void addIfPresent(List<String> args, String flag, String value) {
        if (value == null || value.isBlank()) {
            return;
        }
        args.add(flag);
        args.add(value);
    }

    private static String withK(String value) {
        if (value == null || value.isBlank()) {
            return null;
        }
        String trimmed = value.trim().toLowerCase();
        return trimmed.endsWith("k") ? trimmed : trimmed + "k";
    }

    private static String scaleFilterArg(String resolution) {
        int[] box = parseResolutionBox(resolution);
        if (box == null) {
            return null;
        }
        int width = box[0];
        int height = box[1];
        return "scale=w=" + width + ":h=" + height + ":force_original_aspect_ratio=decrease:force_divisible_by=2";
    }

    private static int[] parseResolutionBox(String value) {
        if (value == null) {
            return null;
        }
        String normalized = value.trim().toLowerCase(Locale.ROOT);
        if (normalized.isBlank() || normalized.startsWith("original")) {
            return null;
        }
        String[] parts = normalized.split("x", 2);
        if (parts.length != 2) {
            return null;
        }
        try {
            int width = Integer.parseInt(parts[0].trim());
            int height = Integer.parseInt(parts[1].trim());
            if (width <= 0 || height <= 0) {
                return null;
            }
            return new int[] { width, height };
        } catch (NumberFormatException e) {
            return null;
        }
    }

    private static String originalResolutionLabel(Integer sourceWidth, Integer sourceHeight) {
        if (sourceWidth == null || sourceHeight == null || sourceWidth <= 0 || sourceHeight <= 0) {
            return "Original";
        }
        return "Original (" + sourceWidth + " x " + sourceHeight + " resolution)";
    }

    private List<OptionDefinition> optionDefinitionsForMode(String targetFormat, String mode, Integer sourceWidth, Integer sourceHeight) {
        List<OptionDefinition> base = optionDefinitionsForFormat(targetFormat);
        if (!VIDEO_FORMATS.contains(targetFormat)) {
            return base;
        }
        String normalizedMode = mode == null ? "default" : mode.trim().toLowerCase(Locale.ROOT);
        boolean compressMode = "compress".equals(normalizedMode);

        List<OptionDefinition> filtered = base.stream()
                .filter(def -> compressMode
                        ? Set.of("targetSizeMb", "resolution").contains(def.key())
                        : !Set.of("targetSizeMb").contains(def.key()))
                .collect(Collectors.toCollection(ArrayList::new));

        if (compressMode) {
            for (int i = 0; i < filtered.size(); i++) {
                OptionDefinition def = filtered.get(i);
                if (!"resolution".equals(def.key())) {
                    continue;
                }
                List<String> filteredChoices = filterResolutionChoicesForSource(def.choices(), sourceWidth, sourceHeight);
                filtered.set(i, new OptionDefinition(
                        def.key(),
                        def.label(),
                        def.type(),
                        def.min(),
                        def.max(),
                        def.step(),
                        filteredChoices,
                        def.defaultValue()));
            }
        }
        return filtered;
    }

    private static List<String> filterResolutionChoicesForSource(List<String> choices, Integer sourceWidth, Integer sourceHeight) {
        if (sourceWidth == null || sourceHeight == null || sourceWidth <= 0 || sourceHeight <= 0) {
            return choices;
        }
        List<String> filtered = new ArrayList<>();
        for (String choice : choices) {
            int[] parsed = parseResolutionBox(choice);
            if (parsed == null) {
                filtered.add(choice);
                continue;
            }
            if (parsed[0] <= sourceWidth && parsed[1] <= sourceHeight) {
                filtered.add(choice);
            }
        }
        if (filtered.stream().noneMatch(value -> "original".equalsIgnoreCase(value))) {
            filtered.add(0, "original");
        }
        return filtered;
    }

    private Integer bitrateForTargetSizeKbps(String targetSizeMb, Path sourceFile, String ffprobeBin) {
        Long targetBytes = parseTargetSizeBytes(targetSizeMb);
        if (targetBytes == null || sourceFile == null) {
            return null;
        }
        Double durationSeconds = probeDurationSeconds(sourceFile, ffprobeBin);
        if (durationSeconds == null || durationSeconds <= 0.1d) {
            return null;
        }
        double bits = targetBytes * 8d;
        double kbps = bits / durationSeconds / 1000d;
        if (!Double.isFinite(kbps) || kbps <= 0d) {
            return null;
        }
        return (int) Math.floor(kbps);
    }

    private static Long parseTargetSizeBytes(String rawValue) {
        if (rawValue == null || rawValue.isBlank()) {
            return (long) DEFAULT_TARGET_SIZE_MB * 1024L * 1024L;
        }
        try {
            double valueMb = Double.parseDouble(rawValue.trim());
            if (!Double.isFinite(valueMb) || valueMb <= 0d) {
                return null;
            }
            double bytes = valueMb * 1024d * 1024d;
            if (!Double.isFinite(bytes) || bytes < 1d || bytes > Long.MAX_VALUE) {
                return null;
            }
            return (long) Math.floor(bytes);
        } catch (NumberFormatException e) {
            return null;
        }
    }

    private Double probeDurationSeconds(Path sourceFile, String ffprobeBin) {
        String binary = (ffprobeBin == null || ffprobeBin.isBlank()) ? "ffprobe" : ffprobeBin.trim();
        List<String> command = List.of(
                binary,
                "-v", "error",
                "-show_entries", "format=duration",
                "-of", "default=noprint_wrappers=1:nokey=1",
                sourceFile.toString());
        try {
            String raw = processRunner.run(command, null, sourceFile.toAbsolutePath().getParent()).output().trim();
            if (raw.isBlank()) {
                return null;
            }
            return Double.parseDouble(raw.replace(',', '.'));
        } catch (IOException | NumberFormatException e) {
            return null;
        }
    }

    private static String jpegQualityToQv(String quality) {
        int q = clamp(parseIntOrDefault(quality, 85), 1, 100);
        int qv = 2 + ((100 - q) * 29 / 99);
        return String.valueOf(clamp(qv, 2, 31));
    }

    private static String pngQualityToCompressionLevel(String quality) {
        int q = clamp(parseIntOrDefault(quality, 90), 1, 100);
        int level = (100 - q) * 9 / 99;
        return String.valueOf(clamp(level, 0, 9));
    }

    private static int parseIntOrDefault(String value, int defaultValue) {
        try {
            return Integer.parseInt(value);
        } catch (NumberFormatException e) {
            return defaultValue;
        }
    }

    private static int clamp(int value, int min, int max) {
        return Math.max(min, Math.min(max, value));
    }

    private record OptionDefinition(
            String key,
            String label,
            String type,
            Integer min,
            Integer max,
            Integer step,
            List<String> choices,
            String defaultValue) {
    }
}
