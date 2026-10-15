package com.schroeter.conv.upload;

import java.time.LocalDateTime;

import com.schroeter.conv.session.ConversionSession;

import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.FetchType;
import jakarta.persistence.Id;
import jakarta.persistence.JoinColumn;
import jakarta.persistence.ManyToOne;
import jakarta.persistence.Table;

@Entity
@Table(name = "upload_records")
public class UploadRecord {

    @Id
    @Column(name = "upload_id", nullable = false, updatable = false, length = 36)
    private String uploadId;

    @ManyToOne(fetch = FetchType.LAZY, optional = false)
    @JoinColumn(name = "session_id", nullable = false)
    private ConversionSession session;

    @Column(name = "original_file_name", nullable = false)
    private String originalFileName;

    @Column(name = "original_file_type", nullable = false, length = 32)
    private String originalFileType;

    @Column(name = "content_type", length = 150)
    private String contentType;

    @Column(name = "original_size_bytes", nullable = false)
    private long originalSizeBytes;

    @Column(name = "uploaded_at", nullable = false)
    private LocalDateTime uploadedAt;

    @Column(name = "stored_file_name", nullable = false)
    private String storedFileName;

    @Column(name = "stored_file_path", nullable = false)
    private String storedFilePath;

    @Column(name = "converted_id", length = 36)
    private String convertedId;

    @Column(name = "converted_file_format", length = 32)
    private String convertedFileFormat;

    @Column(name = "converted_file_name")
    private String convertedFileName;

    @Column(name = "converted_file_path")
    private String convertedFilePath;

    @Column(name = "converted_size_bytes")
    private Long convertedSizeBytes;

    @Column(name = "converted_at")
    private LocalDateTime convertedAt;

    protected UploadRecord() {
    }

    public UploadRecord(
            String uploadId,
            ConversionSession session,
            String originalFileName,
            String originalFileType,
            String contentType,
            long originalSizeBytes,
            LocalDateTime uploadedAt,
            String storedFileName,
            String storedFilePath) {
        this.uploadId = uploadId;
        this.session = session;
        this.originalFileName = originalFileName;
        this.originalFileType = originalFileType;
        this.contentType = contentType;
        this.originalSizeBytes = originalSizeBytes;
        this.uploadedAt = uploadedAt;
        this.storedFileName = storedFileName;
        this.storedFilePath = storedFilePath;
    }

    public String getUploadId() {
        return uploadId;
    }

    public ConversionSession getSession() {
        return session;
    }

    public String getOriginalFileName() {
        return originalFileName;
    }

    public String getOriginalFileType() {
        return originalFileType;
    }

    public String getContentType() {
        return contentType;
    }

    public long getOriginalSizeBytes() {
        return originalSizeBytes;
    }

    public LocalDateTime getUploadedAt() {
        return uploadedAt;
    }

    public String getStoredFileName() {
        return storedFileName;
    }

    public String getStoredFilePath() {
        return storedFilePath;
    }

    public String getConvertedId() {
        return convertedId;
    }

    public String getConvertedFileFormat() {
        return convertedFileFormat;
    }

    public String getConvertedFileName() {
        return convertedFileName;
    }

    public String getConvertedFilePath() {
        return convertedFilePath;
    }

    public LocalDateTime getConvertedAt() {
        return convertedAt;
    }

    public Long getConvertedSizeBytes() {
        return convertedSizeBytes;
    }

    public void markConverted(
            String convertedId,
            String convertedFileFormat,
            String convertedFileName,
            String convertedFilePath,
            long convertedSizeBytes,
            LocalDateTime convertedAt) {
        this.convertedId = convertedId;
        this.convertedFileFormat = convertedFileFormat;
        this.convertedFileName = convertedFileName;
        this.convertedFilePath = convertedFilePath;
        this.convertedSizeBytes = convertedSizeBytes;
        this.convertedAt = convertedAt;
    }
}
