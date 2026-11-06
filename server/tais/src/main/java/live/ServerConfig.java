package live;

import config.Env;

/** Configuration for the local TAIS HTTP/WebSocket endpoint. */
public record ServerConfig(
        String host,
        int port,
        String wsPath,
        String healthPath,
        int maxClients,
        int pendingMax,
        int outboundMaxFrames,
        long outboundMaxBytes,
        int clientInactivitySeconds
) {
    public static ServerConfig fromEnv() {
        return new ServerConfig(
                value("TAIS_WS_HOST", "127.0.0.1"),
                intValue("TAIS_WS_PORT", 8081, 1, 65_535),
                pathValue("TAIS_WS_PATH", "/tais/ws"),
                pathValue("TAIS_HEALTH_PATH", "/tais/healthz"),
                intValue("TAIS_WS_MAX_CLIENTS", 64, 1, 10_000),
                intValue("TAIS_WS_PENDING_MAX", 4096, 128, 100_000),
                intValue("TAIS_WS_OUTBOUND_MAX_FRAMES", 4096, 128, 100_000),
                intValue("TAIS_WS_OUTBOUND_MAX_BYTES", 16 << 20, 16 << 20, 1 << 30),
                intValue("REDS_CLIENT_INACTIVITY_SECONDS", 600, Integer.MIN_VALUE, Integer.MAX_VALUE)
        );
    }

    private static String value(String key, String def) {
        String value = trimToNull(Env.get(key));
        return value == null ? def : value;
    }

    private static int intValue(String key, int def, int min, int max) {
        String raw = trimToNull(Env.get(key));
        if (raw == null) return def;
        try {
            int value = Integer.parseInt(raw);
            return Math.max(min, Math.min(max, value));
        } catch (NumberFormatException ignored) {
            return def;
        }
    }

    private static String pathValue(String key, String def) {
        String path = value(key, def).strip();
        if (!path.startsWith("/")) path = "/" + path;
        while (path.length() > 1 && path.endsWith("/")) {
            path = path.substring(0, path.length() - 1);
        }
        return path;
    }

    private static String trimToNull(String value) {
        if (value == null) return null;
        value = value.strip();
        return value.isEmpty() ? null : value;
    }
}
