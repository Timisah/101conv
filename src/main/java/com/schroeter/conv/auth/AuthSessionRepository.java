package com.schroeter.conv.auth;

import java.time.LocalDateTime;
import java.util.Optional;

import org.springframework.data.jpa.repository.JpaRepository;

public interface AuthSessionRepository extends JpaRepository<AuthSession, String> {
    Optional<AuthSession> findBySessionId(String sessionId);

    void deleteByExpiresAtBefore(LocalDateTime cutoff);

    void deleteByUsername(String username);
}
