package com.schroeter.conv.jobs;

public enum ConversionJobState {
    QUEUED,
    RUNNING,
    SUCCEEDED,
    FAILED,
    CANCELED;

    public boolean terminal() {
        return this == SUCCEEDED || this == FAILED || this == CANCELED;
    }
}
