package playback;

import java.nio.file.Path;

public final class PlaybackConfig {
    public final boolean enabled;
    public final Path dir;
    public final int chunkMinutes;
    public final int localRetentionHours;
    public final int flushIntervalSeconds;
    public final boolean zstdEnabled;

    private PlaybackConfig(
            boolean enabled,
            Path dir,
            int chunkMinutes,
            int localRetentionHours,
            int flushIntervalSeconds,
            boolean zstdEnabled
    ) {
        this.enabled = enabled;
        this.dir = dir;
        this.chunkMinutes = chunkMinutes;
        this.localRetentionHours = localRetentionHours;
        this.flushIntervalSeconds = flushIntervalSeconds;
        this.zstdEnabled = zstdEnabled;
    }

    public static PlaybackConfig fromEnv() {
        return new PlaybackConfig(
                boolEnv("REDS_PLAYBACK_ENABLED", false),
                Path.of(stringEnv("REDS_PLAYBACK_DIR", "/var/lib/reds/playback")),
                intEnv("REDS_PLAYBACK_CHUNK_MINUTES", 5),
                intEnv("REDS_PLAYBACK_LOCAL_RETENTION_HOURS", 4),
                intEnv("REDS_PLAYBACK_FLUSH_INTERVAL_SECONDS", 5),
                boolEnv("REDS_PLAYBACK_COMPRESS_ZSTD", true)
        );
    }

    private static int intEnv(String key, int def) {
        try {
            String val = System.getenv(key);
            if (val == null || val.isBlank()) return def;
            return Math.max(1, Integer.parseInt(val.strip()));
        } catch (Exception e) {
            return def;
        }
    }

    private static boolean boolEnv(String key, boolean def) {
        String val = System.getenv(key);
        if (val == null || val.isBlank()) return def;

        return switch (val.strip().toLowerCase()) {
            case "1", "true", "yes", "on" -> true;
            case "0", "false", "no", "off" -> false;
            default -> def;
        };
    }

    private static String stringEnv(String key, String def) {
        String val = System.getenv(key);
        if (val == null || val.isBlank()) return def;
        return val.strip();
    }
}
