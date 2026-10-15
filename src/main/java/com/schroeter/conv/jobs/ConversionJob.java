package com.schroeter.conv.jobs;

import java.time.Instant;

import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.EnumType;
import jakarta.persistence.Enumerated;
import jakarta.persistence.Id;
import jakarta.persistence.Index;
import jakarta.persistence.Table;
import jakarta.persistence.UniqueConstraint;
import jakarta.persistence.Version;

@Entity
@Table(name = "conversion_jobs", indexes = {
        @Index(name = "idx_conversion_jobs_state_created", columnList = "state,created_at"),
        @Index(name = "idx_conversion_jobs_owner_created", columnList = "owner_id,created_at")
}, uniqueConstraints = {
        @UniqueConstraint(name = "uk_conversion_jobs_owner_idempotency", columnNames = { "owner_id", "idempotency_key" })
})
public class ConversionJob {

    @Id
    @Column(length = 36, nullable = false, updatable = false)
    private String id;

    @Column(name = "owner_id", nullable = false, length = 100)
    private String ownerId;

    @Column(name = "source_name", nullable = false, length = 255)
    private String sourceName;

    @Column(name = "source_format", nullable = false, length = 32)
    private String sourceFormat;

    @Column(name = "target_format", nullable = false, length = 32)
    private String targetFormat;

    @Column(name = "converter_id", nullable = false, length = 100)
    private String converterId;

    @Column(name = "source_path", nullable = false, length = 2048)
    private String sourcePath;

    @Column(name = "source_sha256", nullable = false, length = 64)
    private String sourceSha256;

    @Column(name = "request_fingerprint", nullable = false, length = 64)
    private String requestFingerprint;

    @Column(name = "idempotency_key", length = 200)
    private String idempotencyKey;

    @Column(name = "options_json", nullable = false, length = 8192)
    private String optionsJson;

    @Enumerated(EnumType.STRING)
    @Column(nullable = false, length = 16)
    private ConversionJobState state;

    @Column(nullable = false)
    private int attempt;

    @Column(nullable = false)
    private int progress;

    @Column(name = "cancel_requested", nullable = false)
    private boolean cancelRequested;

    @Column(name = "created_at", nullable = false)
    private Instant createdAt;

    @Column(name = "started_at")
    private Instant startedAt;

    @Column(name = "finished_at")
    private Instant finishedAt;

    @Column(name = "lease_until")
    private Instant leaseUntil;

    @Column(name = "result_path", length = 2048)
    private String resultPath;

    @Column(name = "result_size")
    private Long resultSize;

    @Column(name = "error_code", length = 80)
    private String errorCode;

    @Column(name = "error_message", length = 500)
    private String errorMessage;

    @Version
    @Column(name = "entity_version", nullable = false)
    private long entityVersion;

    protected ConversionJob() {
    }

    public ConversionJob(String id, String ownerId, String sourceName, String sourceFormat, String targetFormat,
            String converterId, String sourcePath, String sourceSha256, String requestFingerprint,
            String idempotencyKey, String optionsJson, Instant createdAt) {
        this.id = id;
        this.ownerId = ownerId;
        this.sourceName = sourceName;
        this.sourceFormat = sourceFormat;
        this.targetFormat = targetFormat;
        this.converterId = converterId;
        this.sourcePath = sourcePath;
        this.sourceSha256 = sourceSha256;
        this.requestFingerprint = requestFingerprint;
        this.idempotencyKey = idempotencyKey;
        this.optionsJson = optionsJson;
        this.createdAt = createdAt;
        this.state = ConversionJobState.QUEUED;
        this.progress = 0;
        this.attempt = 0;
    }

    public void claim(Instant now, Instant leaseExpiry) {
        if (state.terminal() || cancelRequested) {
            return;
        }
        state = ConversionJobState.RUNNING;
        if (startedAt == null) {
            startedAt = now;
        }
        attempt++;
        progress = 1;
        leaseUntil = leaseExpiry;
    }

    public void succeed(String outputPath, long outputSize, Instant now) {
        if (cancelRequested) {
            cancel(now);
            return;
        }
        state = ConversionJobState.SUCCEEDED;
        resultPath = outputPath;
        resultSize = outputSize;
        progress = 100;
        finishedAt = now;
        leaseUntil = null;
    }

    public void fail(String code, String message, Instant now) {
        state = cancelRequested ? ConversionJobState.CANCELED : ConversionJobState.FAILED;
        errorCode = cancelRequested ? "CANCELED" : code;
        errorMessage = cancelRequested ? "The job was canceled." : message;
        finishedAt = now;
        leaseUntil = null;
    }

    public void requestCancellation(Instant now) {
        if (state.terminal()) {
            return;
        }
        cancelRequested = true;
        if (state == ConversionJobState.QUEUED) {
            cancel(now);
        }
    }

    public void recoverCanceledLease(Instant now) {
        if (cancelRequested && !state.terminal()) {
            cancel(now);
        }
    }

    private void cancel(Instant now) {
        state = ConversionJobState.CANCELED;
        progress = 0;
        errorCode = "CANCELED";
        errorMessage = "The job was canceled.";
        finishedAt = now;
        leaseUntil = null;
    }

    public String getId() { return id; }
    public String getOwnerId() { return ownerId; }
    public String getSourceName() { return sourceName; }
    public String getSourceFormat() { return sourceFormat; }
    public String getTargetFormat() { return targetFormat; }
    public String getConverterId() { return converterId; }
    public String getSourcePath() { return sourcePath; }
    public String getSourceSha256() { return sourceSha256; }
    public String getRequestFingerprint() { return requestFingerprint; }
    public String getIdempotencyKey() { return idempotencyKey; }
    public String getOptionsJson() { return optionsJson; }
    public ConversionJobState getState() { return state; }
    public int getAttempt() { return attempt; }
    public int getProgress() { return progress; }
    public boolean isCancelRequested() { return cancelRequested; }
    public Instant getCreatedAt() { return createdAt; }
    public Instant getStartedAt() { return startedAt; }
    public Instant getFinishedAt() { return finishedAt; }
    public Instant getLeaseUntil() { return leaseUntil; }
    public String getResultPath() { return resultPath; }
    public Long getResultSize() { return resultSize; }
    public String getErrorCode() { return errorCode; }
    public String getErrorMessage() { return errorMessage; }
}
