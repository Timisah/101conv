package com.schroeter.conv.jobs;

import java.io.IOException;

public class JobProcessCanceledException extends IOException {
    public JobProcessCanceledException() {
        super("Conversion canceled.");
    }
}
