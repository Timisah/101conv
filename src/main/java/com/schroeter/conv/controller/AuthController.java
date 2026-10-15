package com.schroeter.conv.controller;

import java.util.LinkedHashMap;
import java.util.Map;

import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

import com.schroeter.conv.auth.AuthService;

import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;

@RestController
public class AuthController {

    private final AuthService authService;

    public AuthController(AuthService authService) {
        this.authService = authService;
    }

    @PostMapping("/api/auth/register")
    public ResponseEntity<Map<String, Object>> register(
            @RequestParam("username") String username,
            @RequestParam("password") String password,
            HttpServletResponse response) {
        Map<String, Object> payload = new LinkedHashMap<>();
        try {
            authService.register(username, password);
            var loginResult = authService.login(username, password, response);
            if (loginResult.isEmpty()) {
                payload.put("ok", false);
                payload.put("message", "Account created, but login failed.");
                return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR).body(payload);
            }
            payload.put("ok", true);
            payload.put("userId", loginResult.get().getUsername());
            payload.put("role", loginResult.get().getRole());
            payload.put("admin", loginResult.get().isAdmin());
            payload.put("message", "Account created.");
            return ResponseEntity.ok(payload);
        } catch (IllegalArgumentException e) {
            payload.put("ok", false);
            payload.put("message", e.getMessage());
            return ResponseEntity.badRequest().body(payload);
        }
    }

    @PostMapping("/api/auth/login")
    public ResponseEntity<Map<String, Object>> login(
            @RequestParam("username") String username,
            @RequestParam("password") String password,
            HttpServletResponse response) {
        Map<String, Object> payload = new LinkedHashMap<>();
        var userOptional = authService.login(username, password, response);
        if (userOptional.isEmpty()) {
            payload.put("ok", false);
            payload.put("message", "Invalid username or password.");
            return ResponseEntity.status(HttpStatus.UNAUTHORIZED).body(payload);
        }
        payload.put("ok", true);
        payload.put("userId", userOptional.get().getUsername());
        payload.put("role", userOptional.get().getRole());
        payload.put("admin", userOptional.get().isAdmin());
        return ResponseEntity.ok(payload);
    }

    @PostMapping("/api/auth/logout")
    public ResponseEntity<Map<String, Object>> logout(HttpServletRequest request, HttpServletResponse response) {
        authService.logout(request, response);
        Map<String, Object> payload = new LinkedHashMap<>();
        payload.put("ok", true);
        return ResponseEntity.ok(payload);
    }

    @GetMapping("/api/auth/me")
    public ResponseEntity<Map<String, Object>> me(HttpServletRequest request) {
        Map<String, Object> payload = new LinkedHashMap<>();
        var accountOptional = authService.currentUser(request);
        if (accountOptional.isEmpty()) {
            payload.put("ok", false);
            payload.put("authenticated", false);
            return ResponseEntity.status(HttpStatus.UNAUTHORIZED).body(payload);
        }
        payload.put("ok", true);
        payload.put("authenticated", true);
        payload.put("userId", accountOptional.get().getUsername());
        payload.put("role", accountOptional.get().getRole());
        payload.put("admin", accountOptional.get().isAdmin());
        payload.put("createdAt", accountOptional.get().getCreatedAt());
        payload.put("lastLoginAt", accountOptional.get().getLastLoginAt());
        return ResponseEntity.ok(payload);
    }

    @PostMapping("/api/auth/reset-temp")
    public ResponseEntity<Map<String, Object>> resetTempPassword(
            @RequestParam("username") String username) {
        Map<String, Object> payload = new LinkedHashMap<>();
        try {
            var result = authService.resetPasswordToTemporary(username);
            if (result.isEmpty()) {
                payload.put("ok", false);
                payload.put("message", "Unknown user.");
                return ResponseEntity.status(HttpStatus.NOT_FOUND).body(payload);
            }
            payload.put("ok", true);
            payload.put("message", "Temporary password generated and printed to server terminal.");
            return ResponseEntity.ok(payload);
        } catch (IllegalArgumentException e) {
            payload.put("ok", false);
            payload.put("message", e.getMessage());
            return ResponseEntity.badRequest().body(payload);
        }
    }

    @PostMapping("/api/auth/change-password")
    public ResponseEntity<Map<String, Object>> changePassword(
            @RequestParam("currentPassword") String currentPassword,
            @RequestParam("newPassword") String newPassword,
            HttpServletRequest request,
            HttpServletResponse response) {
        Map<String, Object> payload = new LinkedHashMap<>();
        try {
            authService.changePassword(request, currentPassword, newPassword);
            authService.logout(request, response);
            payload.put("ok", true);
            payload.put("message", "Password changed. Please login again.");
            return ResponseEntity.ok(payload);
        } catch (IllegalArgumentException e) {
            payload.put("ok", false);
            payload.put("message", e.getMessage());
            return ResponseEntity.badRequest().body(payload);
        }
    }
}
