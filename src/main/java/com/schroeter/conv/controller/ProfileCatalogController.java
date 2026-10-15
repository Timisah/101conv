package com.schroeter.conv.controller;

import java.util.LinkedHashMap;
import java.util.Map;

import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

import com.schroeter.conv.auth.AuthService;
import com.schroeter.conv.profiles.ProfileCatalogModels.Installed;
import com.schroeter.conv.profiles.ProfileCatalogModels.Manifest;
import com.schroeter.conv.profiles.ProfileCatalogModels.Source;
import com.schroeter.conv.profiles.ProfileCatalogService;

import jakarta.servlet.http.HttpServletRequest;

@RestController
@RequestMapping("/api/v1/profiles")
public class ProfileCatalogController {
    private final ProfileCatalogService profiles;
    private final AuthService auth;

    public ProfileCatalogController(ProfileCatalogService profiles, AuthService auth) {
        this.profiles = profiles;
        this.auth = auth;
    }

    @GetMapping
    public ResponseEntity<?> list(HttpServletRequest request) {
        if (!admin(request)) return unauthorized();
        return ResponseEntity.ok(Map.of("sources", profiles.sources(), "installed", profiles.installed()));
    }

    @PostMapping("/catalogs")
    public ResponseEntity<?> addCatalog(@RequestBody AddCatalog body, HttpServletRequest request) {
        if (!admin(request)) return unauthorized();
        try { return ResponseEntity.ok(Map.of("sources", profiles.addSource(body.url()))); }
        catch (IllegalArgumentException e) { return badRequest(e); }
    }

    @GetMapping("/catalogs/{sourceId}")
    public ResponseEntity<?> catalog(@PathVariable String sourceId, HttpServletRequest request) {
        if (!admin(request)) return unauthorized();
        try { return ResponseEntity.ok(Map.of("profiles", profiles.catalog(sourceId))); }
        catch (IllegalArgumentException e) { return badRequest(e); }
    }

    @GetMapping("/catalogs/{sourceId}/{profileId}/{version}")
    public ResponseEntity<?> profile(@PathVariable String sourceId, @PathVariable String profileId,
            @PathVariable String version, HttpServletRequest request) {
        if (!admin(request)) return unauthorized();
        try {
            Manifest manifest = profiles.profile(sourceId, profileId, version);
            return ResponseEntity.ok(manifest);
        } catch (IllegalArgumentException e) { return badRequest(e); }
    }

    @PostMapping("/catalogs/{sourceId}/{profileId}/{version}/install")
    public ResponseEntity<?> install(@PathVariable String sourceId, @PathVariable String profileId,
            @PathVariable String version, HttpServletRequest request) {
        if (!admin(request)) return unauthorized();
        try {
            Installed installed = profiles.install(sourceId, profileId, version);
            return ResponseEntity.status(HttpStatus.CREATED).body(installed);
        } catch (IllegalArgumentException e) { return badRequest(e); }
        catch (IllegalStateException e) { return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).body(Map.of("message", e.getMessage())); }
    }

    private boolean admin(HttpServletRequest request) {
        return auth.currentUser(request).map(user -> user.isAdmin()).orElse(false);
    }

    private static ResponseEntity<Map<String, Object>> unauthorized() {
        return ResponseEntity.status(HttpStatus.FORBIDDEN).body(Map.of("message", "Administrator sign-in is required to manage converter profiles."));
    }

    private static ResponseEntity<Map<String, Object>> badRequest(IllegalArgumentException e) {
        Map<String, Object> body = new LinkedHashMap<>(); body.put("message", e.getMessage());
        return ResponseEntity.badRequest().body(body);
    }

    public record AddCatalog(String url) {}
}
