package com.schroeter.conv.converters;

import java.nio.file.Path;
import java.util.Map;

public record ConverterRequest(
        Path sourceFile,
        Path targetFile,
        String sourceFormat,
        String targetFormat,
        Map<String, String> options,
        String jobId) {

    public ConverterRequest(Path sourceFile, Path targetFile, String sourceFormat, String targetFormat,
            Map<String, String> options) {
        this(sourceFile, targetFile, sourceFormat, targetFormat, options, null);
    }
}
