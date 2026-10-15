package com.schroeter.conv.upload;

import java.time.LocalDateTime;

public interface SessionSummaryProjection {
    String getSessionId();

    String getUserId();

    long getConvertedCount();

    LocalDateTime getLatestConvertedAt();
}
