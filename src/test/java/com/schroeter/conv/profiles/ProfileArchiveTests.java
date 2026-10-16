package com.schroeter.conv.profiles;

import static org.junit.jupiter.api.Assertions.*;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;

import org.apache.commons.compress.archivers.tar.TarArchiveEntry;
import org.apache.commons.compress.archivers.tar.TarArchiveOutputStream;
import org.apache.commons.compress.compressors.xz.XZCompressorOutputStream;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class ProfileArchiveTests {
    @TempDir Path temp;

    @Test
    void extractsTarXzPackageAndKeepsItsLicenseFiles() throws Exception {
        byte[] executable = {0x7f, 'E', 'L', 'F'};
        Path archive = createTarXz("engine/bin/ffmpeg", executable);
        Path destination = temp.resolve("unpacked");

        ProfileCatalogService.extractArchive(archive, destination, "tar.xz");

        assertArrayEquals(executable, Files.readAllBytes(destination.resolve("engine/bin/ffmpeg")));
    }

    @Test
    void rejectsTarXzPathTraversal() throws Exception {
        Path archive = createTarXz("../outside", new byte[] {1});

        assertThrows(IllegalArgumentException.class,
                () -> ProfileCatalogService.extractArchive(archive, temp.resolve("unpacked"), "tar.xz"));
        assertFalse(Files.exists(temp.resolve("outside")));
    }

    private Path createTarXz(String name, byte[] bytes) throws IOException {
        Path archive = temp.resolve("fixture.tar.xz");
        try (var xz = new XZCompressorOutputStream(Files.newOutputStream(archive));
             var tar = new TarArchiveOutputStream(xz)) {
            TarArchiveEntry entry = new TarArchiveEntry(name);
            entry.setSize(bytes.length);
            entry.setMode(0755);
            tar.putArchiveEntry(entry);
            tar.write(bytes);
            tar.closeArchiveEntry();
            tar.finish();
        }
        return archive;
    }
}
