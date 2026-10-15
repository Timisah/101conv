package com.schroeter.conv.converters;

import java.util.List;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;

@JsonIgnoreProperties(ignoreUnknown = true)
public record ConverterFileDefinition(
        String id,
        String friendlyName,
        Integer priority,
        Boolean enabled,
        Map<String, String> variables,
        List<RuleDefinition> rules) {

    @JsonIgnoreProperties(ignoreUnknown = true)
    public record RuleDefinition(
            List<String> sources,
            List<String> targets,
            List<String> command) {
    }
}
