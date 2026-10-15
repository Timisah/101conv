package com.schroeter.conv.auth;

import java.time.LocalDateTime;

import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Id;
import jakarta.persistence.Table;

@Entity
@Table(name = "user_accounts")
public class UserAccount {

    @Id
    @Column(name = "username", nullable = false, updatable = false, length = 100)
    private String username;

    @Column(name = "password_hash", nullable = false, length = 120)
    private String passwordHash;

    @Column(name = "role", length = 20)
    private String role;

    @Column(name = "created_at", nullable = false)
    private LocalDateTime createdAt;

    @Column(name = "last_login_at")
    private LocalDateTime lastLoginAt;

    protected UserAccount() {
    }

    public UserAccount(String username, String passwordHash, String role, LocalDateTime createdAt) {
        this.username = username;
        this.passwordHash = passwordHash;
        this.role = role;
        this.createdAt = createdAt;
    }

    public String getUsername() {
        return username;
    }

    public String getPasswordHash() {
        return passwordHash;
    }

    public LocalDateTime getCreatedAt() {
        return createdAt;
    }

    public String getRole() {
        return (role == null || role.isBlank()) ? "USER" : role;
    }

    public boolean isAdmin() {
        return "ADMIN".equalsIgnoreCase(getRole());
    }

    public LocalDateTime getLastLoginAt() {
        return lastLoginAt;
    }

    public void updatePasswordHash(String passwordHash) {
        this.passwordHash = passwordHash;
    }

    public void markLogin(LocalDateTime loginAt) {
        this.lastLoginAt = loginAt;
    }
}
