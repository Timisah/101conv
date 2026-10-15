package com.schroeter.conv.jobs;

import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.ResponseStatus;

@ResponseStatus(HttpStatus.UNPROCESSABLE_ENTITY)
public class UnsupportedConversionException extends RuntimeException {
    public UnsupportedConversionException(String source, String target) {
        super("No installed converter supports " + source + " to " + target + ".");
    }
}
