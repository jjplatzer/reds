package live;

import config.Env;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;

/** Configuration for the SFDPS HTTP/WebSocket endpoint. */
public record ServerConfig(
        String host,
        int port,
        String wsPath,
        String healthPath,
        int maxClients,
        int pendingMax,
        String bearerToken
) {
    public static ServerConfig fromEnv() {
        return new ServerConfig(
                value("SFDPS_WS_HOST", "127.0.0.1"),
                intValue("SFDPS_WS_PORT", 8082, 1, 65_535),
                pathValue("SFDPS_WS_PATH", "/sfdps/ws"),
                pathValue("SFDPS_HEALTH_PATH", "/sfdps/healthz"),
                intValue("SFDPS_WS_MAX_CLIENTS", 64, 1, 10_000),
                intValue("SFDPS_WS_PENDING_MAX", 8192, 128, 200_000),
                trimToNull(Env.get("REDS_WS_TOKEN"))
        );
    }

    public boolean authEnabled() { return bearerToken != null; }

    public boolean authorized(String authorizationHeader) {
        if (!authEnabled()) return true;
        if (authorizationHeader == null) return false;
        byte[] expected = ("Bearer " + bearerToken).getBytes(StandardCharsets.UTF_8);
        byte[] actual = authorizationHeader.getBytes(StandardCharsets.UTF_8);
        return MessageDigest.isEqual(expected, actual);
    }

    private static String value(String key, String def) {
        String value = trimToNull(Env.get(key));
        return value == null ? def : value;
    }
    private static int intValue(String key, int def, int min, int max) {
        String raw = trimToNull(Env.get(key));
        if (raw == null) return def;
        try { int value = Integer.parseInt(raw); return Math.max(min, Math.min(max, value)); }
        catch (NumberFormatException ignored) { return def; }
    }
    private static String pathValue(String key, String def) {
        String path = value(key, def).strip();
        if (!path.startsWith("/")) path = "/" + path;
        while (path.length() > 1 && path.endsWith("/")) path = path.substring(0, path.length() - 1);
        return path;
    }
    private static String trimToNull(String value) {
        if (value == null) return null;
        value = value.strip();
        return value.isEmpty() ? null : value;
    }
}
