package com.schroeter.conv.jobs;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.util.Optional;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.stereotype.Component;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.schroeter.conv.converters.ConverterRequest;
import com.schroeter.conv.converters.BoundedProcessRunner.ConverterExecutionException;

@Component
public class ConversionJobWorker {

    private static final Logger log = LoggerFactory.getLogger(ConversionJobWorker.class);

    private final ConversionJobService jobs;
    private final long maximumOutputBytes;

    public ConversionJobWorker(
            ConversionJobService jobs,
            @Value("${app.jobs.maximum-output-bytes:1073741824}") long maximumOutputBytes) {
        this.jobs = jobs;
        this.maximumOutputBytes = Math.max(1, maximumOutputBytes);
    }

    @Scheduled(fixedDelayString = "${app.jobs.poll-interval-ms:500}")
    public void poll() {
        try {
            Optional<ConversionJob> claim = jobs.claimNext();
            claim.ifPresent(this::run);
        } catch (Exception e) {
            log.error("Could not claim a conversion job.", e);
        }
    }

    private void run(ConversionJob job) {
        Path attemptDir = null;
        try {
            Path root = jobs.jobsRoot().toRealPath();
            Path source = Path.of(job.getSourcePath()).toAbsolutePath().normalize();
            if (!source.startsWith(root) || Files.isSymbolicLink(source)
                    || !Files.isRegularFile(source, LinkOption.NOFOLLOW_LINKS)) {
                throw new IOException("The stored source is unavailable.");
            }

            attemptDir = jobs.attemptDirectory(job);
            Path outputDir = attemptDir.resolve("output").toRealPath();
            if (!outputDir.startsWith(root)) {
                throw new IOException("Invalid output directory.");
            }
            String sourceName = job.getSourceName();
            int extension = sourceName.lastIndexOf('.');
            String stem = extension > 0 ? sourceName.substring(0, extension) : sourceName;
            Path target = outputDir.resolve(stem + "." + job.getTargetFormat()).normalize();
            if (!target.startsWith(outputDir)) {
                throw new IOException("Invalid output path.");
            }

            var converter = jobs.converterFor(job);
            converter.convert(new ConverterRequest(source, target, job.getSourceFormat(), job.getTargetFormat(),
                    jobs.options(job), job.getId()));

            if (!target.startsWith(root) || Files.isSymbolicLink(target)
                    || !Files.isRegularFile(target, LinkOption.NOFOLLOW_LINKS)) {
                throw new IOException("The converter did not produce a regular output file.");
            }
            long size = Files.size(target);
            if (size > maximumOutputBytes) {
                throw new IOException("The converted output exceeded its configured size limit.");
            }
            if (!jobs.succeed(job.getId(), target, size)) {
                deleteTree(attemptDir);
            }
        } catch (JobProcessCanceledException e) {
            jobs.fail(job.getId(), "CANCELED", "The job was canceled.");
            deleteTree(attemptDir);
        } catch (UnsupportedConversionException | JsonProcessingException e) {
            log.warn("Conversion job {} could not use its configured converter.", job.getId(), e);
            jobs.fail(job.getId(), "CONVERTER_UNAVAILABLE", "The selected converter is no longer available.");
            deleteTree(attemptDir);
        } catch (ConverterExecutionException e) {
            String details = "exitCode=" + e.exitCode() + "; timedOut=" + e.timedOut() + "\n" + e.processResult().output();
            log.warn("Conversion job {} converter={} failed: exitCode={}, timedOut={}, output={}", job.getId(),
                    job.getConverterId(), e.exitCode(), e.timedOut(), e.processResult().output());
            jobs.fail(job.getId(), "CONVERTER_PROCESS_FAILED", "The selected converter could not process this file.",
                    safeDetails(details));
            deleteTree(attemptDir);
        } catch (Exception e) {
            log.warn("Conversion job {} failed.", job.getId(), e);
            jobs.fail(job.getId(), "CONVERSION_FAILED", "Conversion failed. Check the installed converter and try again.",
                    safeDetails(e.getClass().getSimpleName() + ": " + e.getMessage()));
            deleteTree(attemptDir);
        }
    }

    private static String safeDetails(String value) {
        if (value == null || value.isBlank()) return null;
        String clean = value.replaceAll("\\u001B\\[[;\\d]*[ -/]*[@-~]", "")
                .replaceAll("[^\\p{Print}\\n\\t]", " ").trim();
        return clean.length() > 8000 ? clean.substring(0, 8000) + "\n[details truncated]" : clean;
    }

    private static void deleteTree(Path root) {
        if (root == null || !Files.exists(root)) {
            return;
        }
        try (var paths = Files.walk(root)) {
            paths.sorted(java.util.Comparator.reverseOrder()).forEach(path -> {
                try {
                    Files.deleteIfExists(path);
                } catch (IOException ignored) {
                }
            });
        } catch (IOException ignored) {
        }
    }
}
