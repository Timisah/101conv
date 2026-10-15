package com.schroeter.conv.upload;

import java.time.LocalDateTime;
import java.util.List;
import java.util.Optional;

import org.springframework.data.domain.Pageable;
import org.springframework.data.jpa.repository.JpaRepository;
import org.springframework.data.jpa.repository.Query;
import org.springframework.data.repository.query.Param;

public interface UploadRecordRepository extends JpaRepository<UploadRecord, String> {
    Optional<UploadRecord> findByUploadIdAndSessionSessionId(String uploadId, String sessionId);
    Optional<UploadRecord> findByUploadIdAndSessionSessionIdAndSessionUserId(String uploadId, String sessionId, String userId);

    long countBySessionSessionId(String sessionId);

    @Query("""
            select ur
            from UploadRecord ur
            join fetch ur.session s
            where ur.convertedAt is not null
            order by ur.convertedAt desc
            """)
    List<UploadRecord> findConvertedWithSession(Pageable pageable);

    @Query("""
            select ur
            from UploadRecord ur
            join fetch ur.session s
            where ur.convertedAt is not null
              and s.userId = :userId
            order by ur.convertedAt desc
            """)
    List<UploadRecord> findConvertedWithSessionByUserId(@Param("userId") String userId, Pageable pageable);

    @Query("""
            select ur
            from UploadRecord ur
            join fetch ur.session s
            where ur.convertedAt is not null
              and ur.convertedAt < :cutoff
            """)
    List<UploadRecord> findConvertedBefore(@Param("cutoff") LocalDateTime cutoff);

    @Query("""
            select ur
            from UploadRecord ur
            join fetch ur.session s
            where ur.convertedAt is not null
            """)
    List<UploadRecord> findAllConvertedWithSession();

    @Query("""
            select ur
            from UploadRecord ur
            join fetch ur.session s
            where s.sessionId = :sessionId
              and ur.convertedAt is not null
            order by ur.convertedAt desc
            """)
    List<UploadRecord> findConvertedBySessionId(@Param("sessionId") String sessionId);

    @Query("""
            select ur
            from UploadRecord ur
            join fetch ur.session s
            where s.sessionId = :sessionId
              and s.userId = :userId
              and ur.convertedAt is not null
            order by ur.convertedAt desc
            """)
    List<UploadRecord> findConvertedBySessionIdAndUserId(@Param("sessionId") String sessionId, @Param("userId") String userId);

    @Query("""
            select s.sessionId as sessionId,
                   s.userId as userId,
                   count(ur) as convertedCount,
                   max(ur.convertedAt) as latestConvertedAt
            from UploadRecord ur
            join ur.session s
            where ur.convertedAt is not null
            group by s.sessionId, s.userId
            order by max(ur.convertedAt) desc
            """)
    List<SessionSummaryProjection> findSessionSummaries(Pageable pageable);

    @Query("""
            select s.sessionId as sessionId,
                   s.userId as userId,
                   count(ur) as convertedCount,
                   max(ur.convertedAt) as latestConvertedAt
            from UploadRecord ur
            join ur.session s
            where ur.convertedAt is not null
              and s.userId = :userId
            group by s.sessionId, s.userId
            order by max(ur.convertedAt) desc
            """)
    List<SessionSummaryProjection> findSessionSummariesByUserId(@Param("userId") String userId, Pageable pageable);
}
