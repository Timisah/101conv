package com.schroeter.conv.session;

import java.util.Optional;

import org.springframework.data.jpa.repository.JpaRepository;

public interface ConversionSessionRepository extends JpaRepository<ConversionSession, String> {
    Optional<ConversionSession> findBySessionIdAndUserId(String sessionId, String userId);
}
