package com.schroeter.conv.session;

import java.time.LocalDateTime;

import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Id;
import jakarta.persistence.Table;

@Entity
@Table(name = "conversion_sessions")
public class ConversionSession {

    @Id
    @Column(name = "session_id", nullable = false, updatable = false, length = 36)
    private String sessionId;

    @Column(name = "session_start_time", nullable = false)
    private LocalDateTime sessionStartTime;

    @Column(name = "user_id", nullable = false, length = 100)
    private String userId;

    @Column(name = "folder_path", nullable = false)
    private String folderPath;

    protected ConversionSession() {
    }

    public ConversionSession(String sessionId, LocalDateTime sessionStartTime, String userId, String folderPath) {
        this.sessionId = sessionId;
        this.sessionStartTime = sessionStartTime;
        this.userId = userId;
        this.folderPath = folderPath;
    }

    public String getSessionId() {
        return sessionId;
    }

    public LocalDateTime getSessionStartTime() {
        return sessionStartTime;
    }

    public String getUserId() {
        return userId;
    }

    public String getFolderPath() {
        return folderPath;
    }
}
