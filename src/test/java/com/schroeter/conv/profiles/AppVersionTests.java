package com.schroeter.conv.profiles;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import org.junit.jupiter.api.Test;

class AppVersionTests {
    @Test
    void treatsMinimumAsInclusiveAndMaximumAsExclusive() {
        assertTrue(AppVersion.supports("0.0.1-SNAPSHOT", "0.0.1", "1.0.0"));
        assertFalse(AppVersion.supports("1.0.0", "0.0.1", "1.0.0"));
        assertFalse(AppVersion.supports("0.0.1", "0.0.2", null));
    }

    @Test
    void rejectsMalformedCompatibilityBounds() {
        assertThrows(IllegalArgumentException.class, () -> AppVersion.parse("latest"));
    }
}
