package com.schroeter.conv.controller;

import java.util.LinkedHashMap;
import java.util.Map;

import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.RestControllerAdvice;
import org.springframework.web.multipart.MaxUploadSizeExceededException;
import org.springframework.web.server.ResponseStatusException;

import com.schroeter.conv.jobs.JobConflictException;
import com.schroeter.conv.jobs.JobNotFoundException;
import com.schroeter.conv.jobs.UnsupportedConversionException;

@RestControllerAdvice
public class ApiExceptionHandler {

    @ExceptionHandler(MaxUploadSizeExceededException.class)
    public ResponseEntity<Map<String, Object>> handleMaxUploadSizeExceeded(MaxUploadSizeExceededException ex) {
        Map<String, Object> response = new LinkedHashMap<>();
        response.put("ok", false);
        response.put("message", "File too large. Maximum upload size is 500MB.");
        return ResponseEntity.status(HttpStatus.PAYLOAD_TOO_LARGE).body(response);
    }

    @ExceptionHandler(JobNotFoundException.class)
    public ResponseEntity<Map<String, Object>> handleJobNotFound(JobNotFoundException ex) {
        return error(HttpStatus.NOT_FOUND, "Job not found.");
    }

    @ExceptionHandler(JobConflictException.class)
    public ResponseEntity<Map<String, Object>> handleJobConflict(JobConflictException ex) {
        return error(HttpStatus.CONFLICT, ex.getMessage());
    }

    @ExceptionHandler(UnsupportedConversionException.class)
    public ResponseEntity<Map<String, Object>> handleUnsupportedConversion(UnsupportedConversionException ex) {
        return error(HttpStatus.UNPROCESSABLE_ENTITY, ex.getMessage());
    }

    @ExceptionHandler(IllegalArgumentException.class)
    public ResponseEntity<Map<String, Object>> handleInvalidRequest(IllegalArgumentException ex) {
        return error(HttpStatus.BAD_REQUEST, ex.getMessage());
    }

    @ExceptionHandler(ResponseStatusException.class)
    public ResponseEntity<Map<String, Object>> handleResponseStatus(ResponseStatusException ex) {
        return error(HttpStatus.valueOf(ex.getStatusCode().value()), ex.getReason());
    }

    private static ResponseEntity<Map<String, Object>> error(HttpStatus status, String message) {
        Map<String, Object> response = new LinkedHashMap<>();
        response.put("ok", false);
        response.put("message", message == null ? status.getReasonPhrase() : message);
        return ResponseEntity.status(status).body(response);
    }
}
