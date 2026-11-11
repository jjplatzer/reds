package config;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.LinkedHashMap;
import java.util.Map;

/** Same nearest-.env and environment-precedence rules as the TAIS service. */
public final class Env {
    private static final Map<String, String> FILE_VALUES = loadNearestDotEnv();

    private Env() {}

    public static String get(String key) {
        String value = System.getenv(key);
        return value != null && !value.isBlank() ? value : FILE_VALUES.get(key);
    }

    public static String value(String key, String fallback) {
        String value = get(key);
        return value == null || value.isBlank() ? fallback : value.strip();
    }

    public static String required(String key) {
        String value = value(key, null);
        if (value == null) throw new IllegalArgumentException("Missing " + key);
        return value;
    }

    public static int integer(String key, int fallback, int min, int max) {
        String value = value(key, null);
        if (value == null) return fallback;
        try { return Math.clamp(Integer.parseInt(value), min, max); }
        catch (NumberFormatException e) { throw new IllegalArgumentException("Invalid integer for " + key); }
    }

    private static Map<String, String> loadNearestDotEnv() {
        Path dir = Path.of("").toAbsolutePath().normalize();
        for (int depth = 0; depth < 6 && dir != null; depth++, dir = dir.getParent()) {
            Path file = dir.resolve(".env");
            if (!Files.isRegularFile(file)) continue;
            try {
                Map<String, String> values = new LinkedHashMap<>();
                for (String raw : Files.readAllLines(file, StandardCharsets.UTF_8)) {
                    String line = raw.strip();
                    if (line.isEmpty() || line.startsWith("#")) continue;
                    if (line.startsWith("export ")) line = line.substring(7).strip();
                    int eq = line.indexOf('=');
                    if (eq <= 0) continue;
                    String key = line.substring(0, eq).strip();
                    String value = line.substring(eq + 1).strip();
                    if (value.length() >= 2 && ((value.startsWith("\"") && value.endsWith("\""))
                            || (value.startsWith("'") && value.endsWith("'")))) {
                        value = value.substring(1, value.length() - 1);
                    }
                    values.putIfAbsent(key, value);
                }
                return Map.copyOf(values);
            } catch (IOException e) {
                throw new IllegalStateException("Cannot read .env", e);
            }
        }
        return Map.of();
    }
}
