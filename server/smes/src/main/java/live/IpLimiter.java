package live;

import io.vertx.core.http.ServerWebSocket;

import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

final class IpLimiter {
    private final ServerConfig config;
    private final Map<String, ClientConnection> byIp = new ConcurrentHashMap<>();
    private final Map<String, Long> lastDisconnectMs = new ConcurrentHashMap<>();

    IpLimiter(ServerConfig config) {
        this.config = config;
    }

    String ipOf(ServerWebSocket ws) {
        String forwardedFor = ws.headers().get("X-Forwarded-For");
        if (forwardedFor != null && !forwardedFor.isBlank()) {
            String first = forwardedFor.split(",", 2)[0].strip();
            if (!first.isBlank()) return first;
        }

        String realIp = ws.headers().get("X-Real-IP");
        if (realIp != null && !realIp.isBlank()) {
            return realIp.strip();
        }

        if (ws.remoteAddress() == null) return "unknown";
        String host = ws.remoteAddress().host();
        if (host == null || host.isBlank()) return "unknown";
        return host;
    }

    boolean canAcceptGlobal(int currentClients) {
        return currentClients < config.maxClients;
    }

    boolean hasClientForIp(String ip) {
        return byIp.containsKey(ip);
    }

    boolean canReconnect(String ip, long nowMs) {
        long last = lastDisconnectMs.getOrDefault(ip, 0L);
        long cooldownMs = Math.max(0, config.reconnectCooldownSeconds) * 1000L;
        return cooldownMs == 0 || nowMs - last >= cooldownMs;
    }

    ClientConnection replaceForIp(String ip, ClientConnection next) {
        if (config.maxClientsPerIp <= 0) return null;

        ClientConnection old = byIp.put(ip, next);
        if (old != null && old != next && !old.ws.isClosed()) {
            old.sendLimitAndClose("replaced_by_new_connection");
        }
        return old;
    }

    void remove(ClientConnection client) {
        if (client == null) return;
        byIp.remove(client.ip, client);
        lastDisconnectMs.put(client.ip, System.currentTimeMillis());
    }
}
