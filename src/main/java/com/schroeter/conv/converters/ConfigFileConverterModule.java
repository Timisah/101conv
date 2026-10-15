package com.schroeter.conv.converters;

import java.io.IOException;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

import com.schroeter.conv.conversion.ConversionOptionsService;

public class ConfigFileConverterModule implements ConverterModule {

    private static final Pattern PLACEHOLDER = Pattern.compile("\\{([^}]+)}");

    private final ConverterFileDefinition definition;
    private final Map<String, Set<String>> supportedMap;
    private final ConversionOptionsService conversionOptionsService;
    private final BoundedProcessRunner processRunner;

    public ConfigFileConverterModule(
            ConverterFileDefinition definition,
            ConversionOptionsService conversionOptionsService,
            BoundedProcessRunner processRunner) {
        this.definition = definition;
        this.conversionOptionsService = conversionOptionsService;
        this.processRunner = processRunner;
        this.supportedMap = buildSupportedMap(definition);
    }

    @Override
    public String id() {
        return normalize(definition.id());
    }

    @Override
    public String friendlyName() {
        if (definition.friendlyName() == null || definition.friendlyName().isBlank()) {
            return id();
        }
        return definition.friendlyName().trim();
    }

    @Override
    public int priority() {
        return definition.priority() == null ? 100 : definition.priority();
    }

    @Override
    public Map<String, Set<String>> supportedConversions() {
        return supportedMap;
    }

    @Override
    public boolean isAvailable() {
        if (definition.enabled() != null && !definition.enabled()) {
            return false;
        }
        return !id().isBlank() && !supportedMap.isEmpty();
    }

    @Override
    public void convert(ConverterRequest request) throws IOException {
        String source = normalize(request.sourceFormat());
        String target = normalize(request.targetFormat());
        ConverterFileDefinition.RuleDefinition rule = findRule(source, target);
        if (rule == null) {
            throw new IOException("No rule found in config converter '" + id() + "' for " + source + " -> " + target);
        }
        List<String> command = buildCommand(rule, request);
        processRunner.run(command, request.jobId(), request.targetFile().getParent());
    }

    private ConverterFileDefinition.RuleDefinition findRule(String source, String target) {
        if (definition.rules() == null) {
            return null;
        }
        for (ConverterFileDefinition.RuleDefinition rule : definition.rules()) {
            if (rule == null) {
                continue;
            }
            Set<String> sources = normalizeSet(rule.sources());
            Set<String> targets = normalizeSet(rule.targets());
            if (sources.contains(source) && targets.contains(target)) {
                return rule;
            }
        }
        return null;
    }

    private List<String> buildCommand(ConverterFileDefinition.RuleDefinition rule, ConverterRequest request) {
        List<String> command = new ArrayList<>();
        if (rule.command() == null) {
            return command;
        }
        for (String token : rule.command()) {
            if (token == null || token.isBlank()) {
                continue;
            }
            addTokenExpansion(command, token.trim(), request);
        }
        return command;
    }

    private void addTokenExpansion(List<String> command, String token, ConverterRequest request) {
        switch (token) {
            case "{source}" -> command.add(request.sourceFile().toString());
            case "{target}" -> command.add(request.targetFile().toString());
            case "{ffmpegArgs}" -> command.addAll(conversionOptionsService.ffmpegArgsFor(
                    request.targetFormat(),
                    request.options(),
                    request.sourceFile(),
                    resolveFfprobeBinary()));
            default -> {
                String expanded = expandInline(token, request);
                if (expanded != null && !expanded.isBlank()) {
                    command.add(expanded);
                }
            }
        }
    }

    private String resolveFfprobeBinary() {
        if (definition.variables() == null) {
            return "ffprobe";
        }
        String ffmpegBin = definition.variables().get("ffmpegBin");
        if (ffmpegBin == null || ffmpegBin.isBlank()) {
            return "ffprobe";
        }
        String trimmed = ffmpegBin.trim();
        if (trimmed.toLowerCase().endsWith("ffmpeg.exe")) {
            return trimmed.substring(0, trimmed.length() - "ffmpeg.exe".length()) + "ffprobe.exe";
        }
        if (trimmed.toLowerCase().endsWith("ffmpeg")) {
            return trimmed.substring(0, trimmed.length() - "ffmpeg".length()) + "ffprobe";
        }
        return "ffprobe";
    }

    private String expandInline(String raw, ConverterRequest request) {
        Matcher matcher = PLACEHOLDER.matcher(raw);
        StringBuilder result = new StringBuilder();
        int cursor = 0;

        while (matcher.find()) {
            result.append(raw, cursor, matcher.start());
            String replacement = resolvePlaceholder(matcher.group(1), request);
            if (replacement == null) {
                replacement = "";
            }
            result.append(replacement);
            cursor = matcher.end();
        }
        result.append(raw.substring(cursor));
        return result.toString().trim();
    }

    private String resolvePlaceholder(String placeholder, ConverterRequest request) {
        if ("source".equals(placeholder)) {
            return request.sourceFile().toString();
        }
        if ("target".equals(placeholder)) {
            return request.targetFile().toString();
        }
        if (placeholder.startsWith("var:")) {
            String key = placeholder.substring("var:".length());
            return definition.variables() == null ? "" : definition.variables().getOrDefault(key, "");
        }
        if (placeholder.startsWith("optionOr:")) {
            String payload = placeholder.substring("optionOr:".length());
            int split = payload.indexOf(':');
            if (split <= 0) {
                return "";
            }
            String key = payload.substring(0, split);
            String fallback = payload.substring(split + 1);
            String value = request.options().get(key);
            return value == null || value.isBlank() ? fallback : value.trim();
        }
        if (placeholder.startsWith("option:")) {
            String key = placeholder.substring("option:".length());
            String value = request.options().get(key);
            return value == null ? "" : value.trim();
        }
        return "{" + placeholder + "}";
    }

    private static Map<String, Set<String>> buildSupportedMap(ConverterFileDefinition definition) {
        LinkedHashMap<String, Set<String>> map = new LinkedHashMap<>();
        if (definition.rules() == null) {
            return Collections.emptyMap();
        }

        for (ConverterFileDefinition.RuleDefinition rule : definition.rules()) {
            if (rule == null) {
                continue;
            }
            Set<String> sources = normalizeSet(rule.sources());
            Set<String> targets = normalizeSet(rule.targets());
            for (String source : sources) {
                map.computeIfAbsent(source, key -> new LinkedHashSet<>()).addAll(targets);
            }
        }

        LinkedHashMap<String, Set<String>> immutable = new LinkedHashMap<>();
        for (Map.Entry<String, Set<String>> entry : map.entrySet()) {
            immutable.put(entry.getKey(), Collections.unmodifiableSet(new LinkedHashSet<>(entry.getValue())));
        }
        return Collections.unmodifiableMap(immutable);
    }

    private static Set<String> normalizeSet(List<String> values) {
        LinkedHashSet<String> normalized = new LinkedHashSet<>();
        if (values == null) {
            return normalized;
        }
        for (String value : values) {
            String norm = normalize(value);
            if (!norm.isBlank()) {
                normalized.add(norm);
            }
        }
        return normalized;
    }

    private static String normalize(String value) {
        if (value == null) {
            return "";
        }
        String normalized = value.trim().toLowerCase();
        return normalized.startsWith(".") ? normalized.substring(1) : normalized;
    }

}
