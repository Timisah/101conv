package com.schroeter.conv.auth;

import java.time.LocalDateTime;

import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Id;
import jakarta.persistence.Table;

@Entity
@Table(name = "auth_sessions")
public class AuthSession {

    @Id
    @Column(name = "session_id", nullable = false, updatable = false, length = 64)
    private String sessionId;

    @Column(name = "username", nullable = false, length = 100)
    private String username;

    @Column(name = "created_at", nullable = false)
    private LocalDateTime createdAt;

    @Column(name = "expires_at", nullable = false)
    private LocalDateTime expiresAt;

    protected AuthSession() {
    }

    public AuthSession(String sessionId, String username, LocalDateTime createdAt, LocalDateTime expiresAt) {
        this.sessionId = sessionId;
        this.username = username;
        this.createdAt = createdAt;
        this.expiresAt = expiresAt;
    }

    public String getSessionId() {
        return sessionId;
    }

    public String getUsername() {
        return username;
    }

    public LocalDateTime getCreatedAt() {
        return createdAt;
    }

    public LocalDateTime getExpiresAt() {
        return expiresAt;
    }

    public boolean isExpired(LocalDateTime now) {
        return expiresAt.isBefore(now);
    }
}