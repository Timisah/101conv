package com.schroeter.conv.controller;

import java.io.IOException;
import java.util.LinkedHashMap;
import java.util.Map;

import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import com.schroeter.conv.conversion.ConversionOptionsService;

@RestController
public class ConfigController {

    private static final String PREFIX = "cfg.";

    private final ConversionOptionsService conversionOptionsService;

    public ConfigController(ConversionOptionsService conversionOptionsService) {
        this.conversionOptionsService = conversionOptionsService;
    }

    @GetMapping("/api/config/defaults")
    public ResponseEntity<Map<String, Object>> getDefaults() {
        return ResponseEntity.ok(conversionOptionsService.getDefaultsEditorPayload());
    }

    @PostMapping("/api/config/defaults")
    public ResponseEntity<Map<String, Object>> saveDefaults(@RequestParam Map<String, String> params) {
        Map<String, String> valuesByPropertyKey = new LinkedHashMap<>();
        for (Map.Entry<String, String> entry : params.entrySet()) {
            if (!entry.getKey().startsWith(PREFIX)) {
                continue;
            }
            String propertyKey = entry.getKey().substring(PREFIX.length());
            valuesByPropertyKey.put(propertyKey, entry.getValue());
        }

        Map<String, Object> response = new LinkedHashMap<>();
        try {
            conversionOptionsService.saveDefaults(valuesByPropertyKey);
            response.put("ok", true);
            response.put("message", "Defaults saved.");
            return ResponseEntity.ok(response);
        } catch (IOException e) {
            response.put("ok", false);
            response.put("message", "Could not save defaults: " + e.getMessage());
            return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).body(response);
        }
    }
}
