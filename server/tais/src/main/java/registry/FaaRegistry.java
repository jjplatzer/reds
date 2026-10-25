package registry;

import config.Env;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStreamReader;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.zip.ZipEntry;
import java.util.zip.ZipFile;

/**
 * Read-only ICAO 24-bit address -> US N-number index from the FAA's
 * ReleasableAircraft.zip / MASTER.txt. Only the two identifiers are retained;
 * the registrant's name, address and other personal fields are never stored.
 *
 * A registration is NOT an ADS-B-transmitted Flight ID. The wire protocol
 * keeps these separate so a future ADS-B source can take precedence.
 */
public final class FaaRegistry {

    private static final FaaRegistry EMPTY = new FaaRegistry(Map.of());
    private final Map<Integer, String> registrations;

    private FaaRegistry(Map<Integer, String> registrations) {
        this.registrations = Collections.unmodifiableMap(registrations);
    }

    /** An unset path disables enrichment; a configured but bad file fails startup. */
    public static FaaRegistry loadConfigured() throws IOException {
        String configured = Env.get("TAIS_FAA_REGISTRY_PATH");
        if (configured == null || configured.isBlank()) {
            System.out.println("[TAIS registry] Disabled (TAIS_FAA_REGISTRY_PATH unset)");
            return EMPTY;
        }
        Path path = Path.of(configured.strip());
        FaaRegistry index = load(path);
        System.out.println("[TAIS registry] Loaded " + index.size() +
                " ICAO-to-registration entries from " + path.getFileName());
        return index;
    }

    /** Accept either the FAA's original ZIP or its extracted MASTER.txt. */
    public static FaaRegistry load(Path path) throws IOException {
        if (!Files.isRegularFile(path)) {
            throw new IOException("FAA registry not found: " + path);
        }
        if (path.getFileName().toString().toLowerCase(Locale.ROOT).endsWith(".zip")) {
            try (ZipFile zip = new ZipFile(path.toFile())) {
                ZipEntry master = zip.stream()
                        .filter(e -> !e.isDirectory())
                        .filter(e -> e.getName().toUpperCase(Locale.ROOT).matches("(?:.*/)?MASTER\\.TXT"))
                        .findFirst().orElseThrow(() -> new IOException("MASTER.txt missing in " + path));
                try (BufferedReader reader = new BufferedReader(
                        new InputStreamReader(zip.getInputStream(master), StandardCharsets.UTF_8))) {
                    return read(reader);
                }
            }
        }
        try (BufferedReader reader = new BufferedReader(
                new InputStreamReader(Files.newInputStream(path), StandardCharsets.UTF_8))) {
            return read(reader);
        }
    }

    private static FaaRegistry read(BufferedReader reader) throws IOException {
        String header = reader.readLine();
        if (header == null) throw new IOException("FAA MASTER.txt is empty");
        List<String> columns = csvHeader(header.replace("\uFEFF", ""));
        int registrationColumn = findColumn(columns, "N-NUMBER");
        int hexColumn = findColumn(columns, "MODE S CODE HEX");
        if (registrationColumn < 0 || hexColumn < 0) {
            throw new IOException("FAA MASTER.txt requires N-NUMBER and MODE S CODE HEX headers");
        }

        Map<Integer, String> index = new HashMap<>();
        Set<Integer> conflicting = new HashSet<>();
        String line;
        while ((line = reader.readLine()) != null) {
            String[] values = csvColumns(line, registrationColumn, hexColumn);
            String nNumber = normalizeNNumber(values[0]);
            Integer address = decodeAddress(values[1]);
            if (nNumber == null || address == null || conflicting.contains(address)) continue;
            String old = index.putIfAbsent(address, nNumber);
            if (old != null && !old.equals(nNumber)) {
                // Never arbitrarily attribute one ICAO address to two registrations.
                index.remove(address);
                conflicting.add(address);
            }
        }
        if (index.isEmpty()) throw new IOException("FAA MASTER.txt has no usable registration entries");
        return new FaaRegistry(index);
    }

    public int size() {
        return registrations.size();
    }

    /** Null means unknown, invalid, or the all-zero non-address. */
    public String lookup(String acAddress) {
        Integer key = decodeAddress(acAddress);
        return key == null ? null : registrations.get(key);
    }

    private static String normalizeNNumber(String raw) {
        if (raw == null) return null;
        String value = raw.strip().toUpperCase(Locale.ROOT);
        if (value.startsWith("N")) value = value.substring(1);
        if (value.isEmpty() || value.length() > 5) return null;
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            if (!((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z'))) return null;
        }
        return "N" + value;
    }

    private static Integer decodeAddress(String raw) {
        if (raw == null) return null;
        String value = raw.strip();
        if (value.length() != 6) return null;
        int code = 0;
        for (int i = 0; i < 6; i++) {
            int hex = Character.digit(value.charAt(i), 16);
            if (hex < 0 || value.charAt(i) > 127) return null;
            code = (code << 4) | hex;
        }
        return code == 0 ? null : code;
    }

    private static int findColumn(List<String> header, String required) {
        for (int i = 0; i < header.size(); i++) {
            if (required.equals(header.get(i).strip().toUpperCase(Locale.ROOT))) return i;
        }
        return -1;
    }

    private static List<String> csvHeader(String line) {
        List<String> fields = new ArrayList<>();
        StringBuilder value = new StringBuilder();
        boolean quoted = false;
        for (int i = 0; i < line.length(); i++) {
            char ch = line.charAt(i);
            if (ch == '"') {
                if (quoted && i + 1 < line.length() && line.charAt(i + 1) == '"') {
                    value.append('"');
                    i++;
                } else quoted = !quoted;
            } else if (ch == ',' && !quoted) {
                fields.add(value.toString());
                value.setLength(0);
            } else {
                value.append(ch);
            }
        }
        fields.add(value.toString());
        return fields;
    }

    /** Extract only the two requested fields, avoiding allocations for PII. */
    private static String[] csvColumns(String line, int registrationIndex, int hexIndex) {
        String[] result = {null, null};
        int index = 0;
        boolean quoted = false;
        StringBuilder value = new StringBuilder();
        for (int i = 0; i <= line.length(); i++) {
            char ch = i < line.length() ? line.charAt(i) : ',';
            if (ch == '"') {
                if (quoted && i + 1 < line.length() && line.charAt(i + 1) == '"') {
                    if (index == registrationIndex || index == hexIndex) value.append('"');
                    i++;
                } else quoted = !quoted;
            } else if (ch == ',' && !quoted) {
                if (index == registrationIndex) result[0] = value.toString();
                if (index == hexIndex) result[1] = value.toString();
                if (result[0] != null && result[1] != null) break;
                value.setLength(0);
                index++;
            } else if (index == registrationIndex || index == hexIndex) {
                value.append(ch);
            }
        }
        return result;
    }
}
