package com.schroeter.conv.profiles;

import java.util.List;
import java.util.Map;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;

public final class ProfileCatalogModels {
    private ProfileCatalogModels() {}

    @JsonIgnoreProperties(ignoreUnknown = true)
    public record Catalog(int version, List<Entry> profiles) {}

    @JsonIgnoreProperties(ignoreUnknown = true)
    public record Entry(String id, String version, String name, String description, String manifestUrl) {}

    @JsonIgnoreProperties(ignoreUnknown = true)
    public record Manifest(
            int schemaVersion, String id, String version, String name, String description,
            String author, String license, String homepage, Artifact artifact, Converter converter) {}

    @JsonIgnoreProperties(ignoreUnknown = true)
    public record Artifact(String url, String sha256, String format, String platform, String executable) {}

    @JsonIgnoreProperties(ignoreUnknown = true)
    public record Converter(
            String id, String friendlyName, Integer priority, List<Rule> rules) {}

    @JsonIgnoreProperties(ignoreUnknown = true)
    public record Rule(List<String> sources, List<String> targets, List<String> command) {}

    @JsonIgnoreProperties(ignoreUnknown = true)
    public record Installed(String id, String version, String name, String license, String sha256) {}

    public record Source(String id, String url) {}
    public record CatalogView(List<Source> sources, List<Entry> profiles, List<Installed> installed) {}
}
