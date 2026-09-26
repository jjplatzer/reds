package config;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Small .env reader for local development.
 *
 * Real environment variables always win. That keeps the exact same binary
 * suitable for a future server/service deployment while allowing local runs
 * from either the repository root or server/tais without exporting secrets by
 * hand first.
 */
public final class Env {

    private static final Map<String, String> FILE_VALUES = loadNearestDotEnv();

    private Env() {}

    public static String get(String key) {
        String value = System.getenv(key);
        if (value != null && !value.isBlank()) return value;
        return FILE_VALUES.get(key);
    }

    private static Map<String, String> loadNearestDotEnv() {
        Path dir = Path.of("").toAbsolutePath().normalize();

        for (int depth = 0; depth < 6 && dir != null; depth++, dir = dir.getParent()) {
            Path file = dir.resolve(".env");
            if (!Files.isRegularFile(file)) continue;

            try {
                return Collections.unmodifiableMap(parse(Files.readAllLines(file, StandardCharsets.UTF_8)));
            } catch (IOException e) {
                throw new IllegalStateException("read " + file + ": " + e.getMessage(), e);
            }
        }

        return Map.of();
    }

    private static Map<String, String> parse(List<String> lines) {
        Map<String, String> out = new LinkedHashMap<>();

        for (String raw : lines) {
            String line = raw.strip();
            if (line.isEmpty() || line.startsWith("#")) continue;
            if (line.startsWith("export ")) line = line.substring("export ".length()).strip();

            int eq = line.indexOf('=');
            if (eq <= 0) continue;

            String key = line.substring(0, eq).strip();
            String value = line.substring(eq + 1).strip();
            if (key.isEmpty()) continue;

            if (value.length() >= 2) {
                char first = value.charAt(0);
                char last = value.charAt(value.length() - 1);
                if ((first == '"' && last == '"') || (first == '\'' && last == '\'')) {
                    value = value.substring(1, value.length() - 1);
                }
            }

            out.putIfAbsent(key, value);
        }

        return out;
    }
}
