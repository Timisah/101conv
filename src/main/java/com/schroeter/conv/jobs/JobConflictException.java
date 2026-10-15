package com.schroeter.conv.jobs;

import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.ResponseStatus;

@ResponseStatus(HttpStatus.CONFLICT)
public class JobConflictException extends RuntimeException {
    public JobConflictException(String message) {
        super(message);
    }
}
