package com.schroeter.conv.converters;

import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class BoundedProcessRunnerTests {

    @TempDir
    Path temp;

    @Test
    void terminatesAConverterThatExceedsItsDeadline() {
        BoundedProcessRunner runner = new BoundedProcessRunner(Duration.ofMillis(100), 1024);
        assertThrows(IOException.class, () -> runner.run(java(List.of("sleep", "30")), null, temp));
    }

    @Test
    void boundsRetainedProcessOutput() throws Exception {
        BoundedProcessRunner runner = new BoundedProcessRunner(Duration.ofSeconds(10), 1024);
        var result = runner.run(java(List.of("flood")), null, temp);
        assertTrue(result.truncated());
        assertTrue(result.output().length() < 1100);
    }

    private static List<String> java(List<String> args) {
        String executable = Path.of(System.getProperty("java.home"), "bin", "java").toString();
        String classpath = System.getProperty("surefire.test.class.path", System.getProperty("java.class.path"));
        var command = new java.util.ArrayList<String>(List.of(executable, "-cp", classpath,
                ProcessFixture.class.getName()));
        command.addAll(args);
        return command;
    }
}
