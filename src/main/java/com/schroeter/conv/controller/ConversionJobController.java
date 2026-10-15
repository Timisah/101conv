package com.schroeter.conv.controller;

import java.io.IOException;
import java.nio.file.Path;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Optional;

import org.springframework.core.io.FileSystemResource;
import org.springframework.core.io.Resource;
import org.springframework.http.ContentDisposition;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestHeader;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.server.ResponseStatusException;
import org.springframework.web.multipart.MultipartFile;

import com.schroeter.conv.auth.AuthService;
import com.schroeter.conv.jobs.ConversionJob;
import com.schroeter.conv.jobs.ConversionJobService;
import com.schroeter.conv.jobs.ConversionJobView;
import com.schroeter.conv.jobs.JobConflictException;

import jakarta.servlet.http.HttpServletRequest;

@RestController
@RequestMapping("/api/v1/jobs")
public class ConversionJobController {

    private final ConversionJobService jobs;
    private final AuthService authService;

    public ConversionJobController(ConversionJobService jobs, AuthService authService) {
        this.jobs = jobs;
        this.authService = authService;
    }

    @PostMapping(consumes = MediaType.MULTIPART_FORM_DATA_VALUE)
    public ResponseEntity<ConversionJobView> submit(
            @RequestParam("file") MultipartFile file,
            @RequestParam("sourceFormat") String sourceFormat,
            @RequestParam("targetFormat") String targetFormat,
            @RequestParam(value = "converterId", required = false) String converterId,
            @RequestParam Map<String, String> parameters,
            @RequestHeader(name = "Idempotency-Key", required = false) String idempotencyKey,
            HttpServletRequest request) throws IOException {
        String ownerId = authenticatedOwner(request);
        ConversionJob job = jobs.submit(ownerId, file, sourceFormat, targetFormat, converterId,
                extractOptions(parameters), idempotencyKey);
        return ResponseEntity.accepted().body(ConversionJobView.from(job));
    }

    @GetMapping("/{id}")
    public ConversionJobView status(@PathVariable String id, HttpServletRequest request) {
        return ConversionJobView.from(jobs.findOwned(authenticatedOwner(request), id));
    }

    @PostMapping("/{id}/cancel")
    public ConversionJobView cancel(@PathVariable String id, HttpServletRequest request) {
        String ownerId = authenticatedOwner(request);
        jobs.cancel(ownerId, id);
        return ConversionJobView.from(jobs.findOwned(ownerId, id));
    }

    @GetMapping("/{id}/result")
    public ResponseEntity<Resource> result(@PathVariable String id, HttpServletRequest request) {
        Path result = jobs.resultPath(authenticatedOwner(request), id);
        String fileName = result.getFileName().toString();
        return ResponseEntity.ok()
                .contentType(MediaType.APPLICATION_OCTET_STREAM)
                .header(HttpHeaders.CONTENT_DISPOSITION,
                        ContentDisposition.attachment().filename(fileName).build().toString())
                .body(new FileSystemResource(result));
    }

    private String authenticatedOwner(HttpServletRequest request) {
        Optional<String> owner = authService.resolveUserId(request);
        return owner.orElseThrow(() -> new ResponseStatusException(HttpStatus.UNAUTHORIZED,
                "Authentication is required for conversion jobs."));
    }

    private static Map<String, String> extractOptions(Map<String, String> parameters) {
        Map<String, String> options = new LinkedHashMap<>();
        for (Map.Entry<String, String> entry : parameters.entrySet()) {
            if (entry.getKey().startsWith("option_")) {
                options.put(entry.getKey().substring("option_".length()), entry.getValue());
            }
        }
        return options;
    }
}
