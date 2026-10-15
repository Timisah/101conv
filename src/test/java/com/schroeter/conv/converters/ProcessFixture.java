package com.schroeter.conv.converters;

public final class ProcessFixture {
    private ProcessFixture() {
    }

    public static void main(String[] args) throws Exception {
        if ("sleep".equals(args[0])) {
            Thread.sleep(Long.parseLong(args[1]) * 1000);
        } else if ("flood".equals(args[0])) {
            for (int i = 0; i < 200_000; i++) {
                System.out.print('x');
            }
        } else if ("copy".equals(args[0])) {
            java.nio.file.Files.copy(java.nio.file.Path.of(args[1]), java.nio.file.Path.of(args[2]));
        }
    }
}
