package com.schroeter.conv.converters;

import java.io.IOException;
import java.util.Map;
import java.util.Set;

public interface ConverterModule {

    String id();

    String friendlyName();

    int priority();

    Map<String, Set<String>> supportedConversions();

    boolean isAvailable();

    void convert(ConverterRequest request) throws IOException;
}
