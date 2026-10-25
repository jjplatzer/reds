package registry;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.zip.ZipEntry;
import java.util.zip.ZipOutputStream;

import static org.junit.jupiter.api.Assertions.*;

final class FaaRegistryTest {

    @TempDir Path temp;

    private static final String MASTER = "N-NUMBER,NAME,MODE S CODE HEX,CERTIFICATE EXPIRATION DATE,\n" +
            "172SP,\"SMITH, JOHN\",A9D334,20290101,\n" +
            " 6019P ,Other Name,A4FA0F,20300101,\n" +
            " 777AA ,Another Name,000000,20300101,\n" +
            " 21BC ,A Name,NOTHEX,20300101,\n" +
            " 999A ,A Name,A9D334,20300101,\n" + // collision: reject both
            "  123 ,A Name,A3A9B6,20300101,\n";

    @Test
    void mapsOnlyUnambiguousModeSHexToNNumber() throws Exception {
        Path file = temp.resolve("MASTER.txt");
        Files.writeString(file, MASTER);
        FaaRegistry registry = FaaRegistry.load(file);
        assertEquals(2, registry.size());
        assertEquals("N6019P", registry.lookup("a4fa0f"));
        assertEquals("N123", registry.lookup(" A3A9B6 "));
        assertNull(registry.lookup("A9D334")); // conflicting registrations
        assertNull(registry.lookup("000000"));
        assertNull(registry.lookup("zzzzzz"));
        assertNull(registry.lookup(""));
    }

    @Test
    void readsOriginalZipDirectly() throws Exception {
        Path zipPath = temp.resolve("ReleasableAircraft.zip");
        try (ZipOutputStream zip = new ZipOutputStream(Files.newOutputStream(zipPath))) {
            zip.putNextEntry(new ZipEntry("ReleasableAircraft/MASTER.txt"));
            zip.write(MASTER.getBytes(java.nio.charset.StandardCharsets.UTF_8));
            zip.closeEntry();
        }
        assertEquals("N123", FaaRegistry.load(zipPath).lookup("a3a9b6"));
    }

    @Test
    void failsOnMissingRequiredFieldsOrMissingConfiguredFile() throws Exception {
        Path file = temp.resolve("MASTER.txt");
        Files.writeString(file, "N-NUMBER,NAME\n172SP,Test\n");
        assertThrows(IOException.class, () -> FaaRegistry.load(file));
        assertThrows(IOException.class, () -> FaaRegistry.load(temp.resolve("missing.zip")));
    }
}
