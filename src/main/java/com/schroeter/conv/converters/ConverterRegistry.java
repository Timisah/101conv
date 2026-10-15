package com.schroeter.conv.converters;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Collection;
import java.util.Collections;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.Set;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.stereotype.Service;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.schroeter.conv.config.UserConfigService;
import com.schroeter.conv.conversion.ConversionOptionsService;

@Service
public class ConverterRegistry {

    private static final Logger log = LoggerFactory.getLogger(ConverterRegistry.class);
    private final UserConfigService userConfigService;
    private final BoundedProcessRunner processRunner;

    private final ObjectMapper objectMapper;
    private final ConversionOptionsService conversionOptionsService;
    private volatile List<ConverterModule> activeConverters = List.of();
    private volatile Map<String, ConverterModule> byId = Map.of();
    private volatile Map<String, Set<String>> mergedFormats = Map.of();

    public ConverterRegistry(
            UserConfigService userConfigService,
            ConversionOptionsService conversionOptionsService,
            BoundedProcessRunner processRunner) {
        this.userConfigService = userConfigService;
        this.processRunner = processRunner;
        this.objectMapper = new ObjectMapper();
        this.conversionOptionsService = conversionOptionsService;
        refresh();
    }

    public synchronized void refresh() {
        List<ConverterModule> discovered = discoverActiveConverters();
        activeConverters = discovered;
        byId = indexById(discovered);
        mergedFormats = buildMergedFormatMap(discovered);
    }

    public List<ConverterModule> getActiveConverters() {
        return activeConverters;
    }

    public Optional<ConverterModule> findById(String converterId) {
        if (converterId == null || converterId.isBlank()) {
            return Optional.empty();
        }
        return Optional.ofNullable(byId.get(converterId.trim()));
    }

    public Optional<ConverterModule> resolve(String sourceFormat, String targetFormat, String preferredConverterId) {
        List<ConverterModule> candidates = resolveCandidates(sourceFormat, targetFormat, preferredConverterId);
        if (candidates.isEmpty()) {
            return Optional.empty();
        }
        return Optional.of(candidates.get(0));
    }

    public List<ConverterModule> resolveCandidates(String sourceFormat, String targetFormat, String preferredConverterId) {
        String source = normalize(sourceFormat);
        String target = normalize(targetFormat);
        List<ConverterModule> ordered = new ArrayList<>();

        Optional<ConverterModule> preferred = findById(preferredConverterId);
        if (preferred.isPresent() && supports(preferred.get(), source, target)) {
            ordered.add(preferred.get());
        }

        activeConverters.stream()
                .filter(converter -> supports(converter, source, target))
                .sorted(Comparator.comparingInt(ConverterModule::priority).reversed())
                .forEach(converter -> {
                    if (!ordered.contains(converter)) {
                        ordered.add(converter);
                    }
                });

        return List.copyOf(ordered);
    }

    public Set<String> targetFormatsFor(String sourceFormat, String preferredConverterId) {
        String source = normalize(sourceFormat);
        LinkedHashSet<String> targets = new LinkedHashSet<>();
        if (preferredConverterId != null && !preferredConverterId.isBlank()) {
            findById(preferredConverterId).ifPresent(converter -> {
                Set<String> converterTargets = converter.supportedConversions()
                        .getOrDefault(source, Collections.emptySet());
                targets.addAll(converterTargets);
                if (converter.supportedConversions().containsKey(source)) {
                    targets.add(source);
                }
            });
            return targets;
        }
        targets.addAll(mergedFormats.getOrDefault(source, Collections.emptySet()));
        for (ConverterModule converter : activeConverters) {
            if (converter.supportedConversions().containsKey(source)) {
                targets.add(source);
                break;
            }
        }
        return targets;
    }

    public Map<String, Set<String>> mergedFormatMap() {
        return mergedFormats;
    }

    private List<ConverterModule> discoverActiveConverters() {
        Path configDir = configDir();
        logDiscoveredConfigFiles();
        List<ConverterModule> discovered = new ArrayList<>();
        List<Path> files = loadConfigFiles();

        for (Path file : files) {
            try {
                ConverterFileDefinition definition = objectMapper.readValue(file.toFile(), ConverterFileDefinition.class);
                ConverterModule converter = new ConfigFileConverterModule(definition, conversionOptionsService, processRunner);
                if (!converter.isAvailable()) {
                    log.info("Skipping unavailable converter '{}' from {}", converter.id(), file.getFileName());
                    continue;
                }
                discovered.add(converter);
            } catch (Exception ex) {
                log.warn("Could not initialize converter config {}", file, ex);
            }
        }

        discovered.sort(Comparator.comparingInt(ConverterModule::priority).reversed());
        List<ConverterModule> immutable = List.copyOf(discovered);
        log.info("Initialized {} converter(s) from {}: {}", immutable.size(), configDir, immutable.stream().map(ConverterModule::id).toList());
        return immutable;
    }

    private void logDiscoveredConfigFiles() {
        Path configDir = configDir();
        if (!Files.isDirectory(configDir)) {
            log.info("Converter config directory not found: {}", configDir);
            return;
        }
        try {
            List<String> files;
            try (var stream = Files.list(configDir)) {
                files = stream
                        .filter(Files::isRegularFile)
                        .map(Path::getFileName)
                        .map(Path::toString)
                        .filter(name -> name.endsWith(".json"))
                        .sorted()
                        .toList();
            }
            log.info("Converter config scan found {} file(s): {}", files.size(), files);
        } catch (IOException ex) {
            log.warn("Could not scan converter config directory {}", configDir, ex);
        }
    }

    private List<Path> loadConfigFiles() {
        Path configDir = configDir();
        if (!Files.isDirectory(configDir)) {
            return List.of();
        }
        try (var stream = Files.list(configDir)) {
            return stream
                    .filter(Files::isRegularFile)
                    .filter(path -> path.getFileName().toString().toLowerCase().endsWith(".json"))
                    .sorted()
                    .toList();
        } catch (IOException ex) {
            log.warn("Could not read converter config files from {}", configDir, ex);
            return List.of();
        }
    }

    private static Map<String, ConverterModule> indexById(Collection<ConverterModule> converters) {
        LinkedHashMap<String, ConverterModule> map = new LinkedHashMap<>();
        for (ConverterModule converter : converters) {
            map.put(converter.id(), converter);
        }
        return Collections.unmodifiableMap(map);
    }

    private static Map<String, Set<String>> buildMergedFormatMap(List<ConverterModule> converters) {
        LinkedHashMap<String, Set<String>> map = new LinkedHashMap<>();
        for (ConverterModule converter : converters) {
            for (Map.Entry<String, Set<String>> entry : converter.supportedConversions().entrySet()) {
                String source = normalize(entry.getKey());
                Set<String> values = map.computeIfAbsent(source, key -> new LinkedHashSet<>());
                for (String target : entry.getValue()) {
                    values.add(normalize(target));
                }
            }
        }

        LinkedHashMap<String, Set<String>> immutable = new LinkedHashMap<>();
        for (Map.Entry<String, Set<String>> entry : map.entrySet()) {
            immutable.put(entry.getKey(), Collections.unmodifiableSet(new LinkedHashSet<>(entry.getValue())));
        }
        return Collections.unmodifiableMap(immutable);
    }

    private static boolean supports(ConverterModule converter, String source, String target) {
        if (converter.supportedConversions()
                .getOrDefault(source, Collections.emptySet())
                .contains(target)) {
            return true;
        }
        return source.equals(target) && converter.supportedConversions().containsKey(source);
    }

    private static String normalize(String value) {
        if (value == null) {
            return "";
        }
        String normalized = value.trim().toLowerCase();
        return normalized.startsWith(".") ? normalized.substring(1) : normalized;
    }

    private Path configDir() {
        return Path.of(userConfigService.converterConfigDir()).toAbsolutePath().normalize();
    }
}
