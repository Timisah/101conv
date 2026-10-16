package com.schroeter.conv.converters;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

import org.springframework.beans.factory.annotation.Value;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Component;

import com.schroeter.conv.jobs.JobProcessCanceledException;

/** Runs an installed converter without shell parsing, with bounded time and captured output. */
@Component
public class BoundedProcessRunner {

    private final Duration timeout;
    private final int outputLimitBytes;
    private final Map<String, Process> activeProcesses = new ConcurrentHashMap<>();
    private final Set<String> cancellationRequests = ConcurrentHashMap.newKeySet();

    @Autowired
    public BoundedProcessRunner(
            @Value("${app.converter.timeout-seconds:300}") long timeoutSeconds,
            @Value("${app.converter.output-limit-bytes:65536}") int outputLimitBytes) {
        this(Duration.ofSeconds(Math.max(1, timeoutSeconds)), Math.max(1024, outputLimitBytes));
    }

    BoundedProcessRunner(Duration timeout, int outputLimitBytes) {
        this.timeout = timeout;
        this.outputLimitBytes = outputLimitBytes;
    }

    public ProcessResult run(List<String> command, String jobId, Path workingDirectory) throws IOException {
        if (command == null || command.isEmpty()) {
            throw new IOException("Converter command is empty.");
        }
        Path workDir = workingDirectory.toAbsolutePath().normalize();
        Files.createDirectories(workDir);
        Path tempDir = workDir.resolve(".tmp");
        Files.createDirectories(tempDir);

        if (jobId != null && cancellationRequests.contains(jobId)) {
            throw new JobProcessCanceledException();
        }

        ProcessBuilder builder = new ProcessBuilder(command);
        builder.directory(workDir.toFile());
        builder.redirectErrorStream(true);
        Map<String, String> environment = builder.environment();
        environment.clear();
        environment.put("PATH", System.getenv().getOrDefault("PATH", "/usr/local/bin:/usr/bin:/bin"));
        environment.put("HOME", workDir.toString());
        environment.put("TMPDIR", tempDir.toString());
        environment.put("LC_ALL", "C.UTF-8");

        Process process = builder.start();
        if (jobId != null) {
            activeProcesses.put(jobId, process);
            if (cancellationRequests.contains(jobId)) {
                terminate(process);
            }
        }

        BoundedOutput output = new BoundedOutput(outputLimitBytes);
        Thread reader = Thread.ofVirtual().name("converter-output-reader").start(() -> drain(process.getInputStream(), output));
        try {
            boolean finished = process.waitFor(timeout.toMillis(), TimeUnit.MILLISECONDS);
            if (!finished) {
                terminate(process);
                joinReader(reader);
                ProcessResult result = output.result();
                throw new ConverterExecutionException(process.exitValue(), result, true);
            }
            joinReader(reader);
            if (jobId != null && cancellationRequests.contains(jobId)) {
                throw new JobProcessCanceledException();
            }
            ProcessResult result = output.result();
            if (process.exitValue() != 0) {
                throw new ConverterExecutionException(process.exitValue(), result, false);
            }
            return result;
        } catch (InterruptedException e) {
            terminate(process);
            Thread.currentThread().interrupt();
            if (jobId != null && cancellationRequests.contains(jobId)) {
                throw new JobProcessCanceledException();
            }
            throw new IOException("Converter execution was interrupted.", e);
        } finally {
            if (jobId != null) {
                activeProcesses.remove(jobId, process);
                cancellationRequests.remove(jobId);
            }
        }
    }

    public void cancel(String jobId) {
        cancellationRequests.add(jobId);
        Process process = activeProcesses.get(jobId);
        if (process != null) {
            terminate(process);
        }
    }

    private static void drain(InputStream stream, BoundedOutput output) {
        try (stream) {
            byte[] buffer = new byte[8192];
            int read;
            while ((read = stream.read(buffer)) >= 0) {
                output.accept(buffer, read);
            }
        } catch (IOException ignored) {
            // Process termination closes the stream; the exit state remains authoritative.
        }
    }

    private static void joinReader(Thread reader) throws InterruptedException {
        reader.join(2_000);
        if (reader.isAlive()) {
            reader.interrupt();
        }
    }

    private static void terminate(Process process) {
        process.descendants().forEach(ProcessHandle::destroy);
        process.destroy();
        try {
            if (!process.waitFor(250, TimeUnit.MILLISECONDS)) {
                process.descendants().forEach(ProcessHandle::destroyForcibly);
                process.destroyForcibly();
                process.waitFor(2, TimeUnit.SECONDS);
            }
        } catch (InterruptedException e) {
            process.descendants().forEach(ProcessHandle::destroyForcibly);
            process.destroyForcibly();
            Thread.currentThread().interrupt();
        }
    }

    public record ProcessResult(String output, boolean truncated) {
    }

    public static final class ConverterExecutionException extends IOException {
        private final int exitCode;
        private final ProcessResult processResult;
        private final boolean timedOut;

        private ConverterExecutionException(int exitCode, ProcessResult processResult, boolean timedOut) {
            super(timedOut ? "Converter exceeded its execution deadline."
                    : "Converter exited with code " + exitCode + ".");
            this.exitCode = exitCode;
            this.processResult = processResult;
            this.timedOut = timedOut;
        }

        public int exitCode() { return exitCode; }
        public ProcessResult processResult() { return processResult; }
        public boolean timedOut() { return timedOut; }
    }

    private static final class BoundedOutput {
        private final int limit;
        private final ByteArrayOutputStream retained;
        private final AtomicBoolean truncated = new AtomicBoolean();

        private BoundedOutput(int limit) {
            this.limit = limit;
            this.retained = new ByteArrayOutputStream(Math.min(limit, 8192));
        }

        private synchronized void accept(byte[] buffer, int length) {
            int remaining = limit - retained.size();
            if (remaining > 0) {
                retained.write(buffer, 0, Math.min(remaining, length));
            }
            if (length > remaining) {
                truncated.set(true);
            }
        }

        private synchronized ProcessResult result() {
            String value = retained.toString(StandardCharsets.UTF_8);
            return new ProcessResult(truncated.get() ? value + "\n[output truncated]" : value, truncated.get());
        }
    }
}
