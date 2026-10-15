package com.schroeter.conv.jobs;

import java.time.Instant;

public record ConversionJobView(
        String id,
        ConversionJobState state,
        String sourceName,
        String sourceFormat,
        String targetFormat,
        String converterId,
        int attempt,
        Integer progress,
        Instant createdAt,
        Instant startedAt,
        Instant finishedAt,
        String errorCode,
        String errorMessage,
        boolean cancellationRequested,
        String resultUrl) {

    public static ConversionJobView from(ConversionJob job) {
        return new ConversionJobView(
                job.getId(), job.getState(), job.getSourceName(), job.getSourceFormat(), job.getTargetFormat(),
                job.getConverterId(), job.getAttempt(),
                job.getState() == ConversionJobState.SUCCEEDED ? 100 : null,
                job.getCreatedAt(), job.getStartedAt(), job.getFinishedAt(), job.getErrorCode(), job.getErrorMessage(),
                job.isCancelRequested(), job.getState() == ConversionJobState.SUCCEEDED
                        ? "/api/v1/jobs/" + job.getId() + "/result" : null);
    }
}
