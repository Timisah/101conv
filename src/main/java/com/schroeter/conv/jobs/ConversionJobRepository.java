package com.schroeter.conv.jobs;

import java.time.Instant;
import java.util.List;
import java.util.Optional;

import org.springframework.data.domain.Pageable;
import org.springframework.data.jpa.repository.JpaRepository;
import org.springframework.data.jpa.repository.Lock;
import org.springframework.data.jpa.repository.Query;
import org.springframework.data.repository.query.Param;

import jakarta.persistence.LockModeType;

public interface ConversionJobRepository extends JpaRepository<ConversionJob, String> {

    Optional<ConversionJob> findByIdAndOwnerId(String id, String ownerId);

    Optional<ConversionJob> findByOwnerIdAndIdempotencyKey(String ownerId, String idempotencyKey);

    @Lock(LockModeType.PESSIMISTIC_WRITE)
    @Query("select job from ConversionJob job where job.state = :queued or "
            + "(job.state = :running and job.leaseUntil <= :now) order by job.createdAt asc")
    List<ConversionJob> findClaimable(
            @Param("queued") ConversionJobState queued,
            @Param("running") ConversionJobState running,
            @Param("now") Instant now,
            Pageable pageable);
}
