package live;

final class ServerConfig {
    static final int DEFAULT_PORT = 8080;
    static final String DEFAULT_HOST = "127.0.0.1";

    final String host;
    final int port;

    final String requiredToken;
    final boolean requireAuth;
    final boolean publicMode;

    final int maxClients;
    final int maxClientsPerIp;
    final int maxAirportsPerConnection;
    final int maxInboundBytes;
    final int maxMessagesPerMinute;
    final int subscribeTimeoutSeconds;
    final int reconnectCooldownSeconds;

    private ServerConfig(
            String host,
            int port,
            String requiredToken,
            boolean requireAuth,
            boolean publicMode,
            int maxClients,
            int maxClientsPerIp,
            int maxAirportsPerConnection,
            int maxInboundBytes,
            int maxMessagesPerMinute,
            int subscribeTimeoutSeconds,
            int reconnectCooldownSeconds
    ) {
        this.host = host;
        this.port = port;
        this.requiredToken = requiredToken;
        this.requireAuth = requireAuth;
        this.publicMode = publicMode;
        this.maxClients = maxClients;
        this.maxClientsPerIp = maxClientsPerIp;
        this.maxAirportsPerConnection = maxAirportsPerConnection;
        this.maxInboundBytes = maxInboundBytes;
        this.maxMessagesPerMinute = maxMessagesPerMinute;
        this.subscribeTimeoutSeconds = subscribeTimeoutSeconds;
        this.reconnectCooldownSeconds = reconnectCooldownSeconds;
    }

    static ServerConfig fromEnv() {
        String host = stringEnv("WS_HOST", DEFAULT_HOST);
        int port = intEnv("WS_PORT", DEFAULT_PORT);

        String token = stringEnv("REDS_WS_TOKEN", "");

        // Backward-compatible default: require auth iff REDS_WS_TOKEN is set.
        boolean requireAuth = boolEnv("REDS_REQUIRE_AUTH", !token.isBlank());
        boolean publicMode = boolEnv("REDS_PUBLIC_MODE", false);

        return new ServerConfig(
                host,
                port,
                token,
                requireAuth,
                publicMode,
                intEnv("REDS_MAX_CLIENTS", 50),
                intEnv("REDS_MAX_CLIENTS_PER_IP", 1),
                intEnv("REDS_MAX_AIRPORTS_PER_CONNECTION", 1),
                intEnv("REDS_MAX_INBOUND_BYTES", 2048),
                intEnv("REDS_MAX_MESSAGES_PER_MINUTE", 10),
                intEnv("REDS_SUBSCRIBE_TIMEOUT_SECONDS", 10),
                intEnv("REDS_RECONNECT_COOLDOWN_SECONDS", 5)
        );
    }

    String authModeLabel() {
        if (!requireAuth) return "anonymous";
        if (!requiredToken.isBlank()) return "bearer-token";
        return "required-but-unconfigured";
    }

    private static int intEnv(String key, int def) {
        try {
            String val = System.getenv(key);
            if (val == null || val.isBlank()) return def;
            return Integer.parseInt(val.strip());
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
