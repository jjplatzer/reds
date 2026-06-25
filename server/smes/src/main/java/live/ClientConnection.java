package live;

import io.vertx.core.http.ServerWebSocket;

import java.util.Set;

final class ClientConnection {
    final ServerWebSocket ws;
    final String ip;
    final long connectedAtMs;

    volatile Set<String> airports = Set.of();

    private int messagesThisWindow = 0;
    private long messageWindowStartMs = System.currentTimeMillis();

    ClientConnection(ServerWebSocket ws, String ip) {
        this.ws = ws;
        this.ip = ip;
        this.connectedAtMs = System.currentTimeMillis();
    }

    boolean accepts(String airport) {
        return airport != null && !airport.isBlank() && airports.contains(airport);
    }

    boolean allowMessage(ServerConfig config, long nowMs) {
        long windowMs = 60_000L;
        if (nowMs - messageWindowStartMs >= windowMs) {
            messageWindowStartMs = nowMs;
            messagesThisWindow = 0;
        }

        messagesThisWindow++;
        return messagesThisWindow <= config.maxMessagesPerMinute;
    }

    void sendLimitAndClose(String reason) {
        if (ws.isClosed()) return;
        try {
            ws.writeTextMessage("{\"type\":\"limit\",\"reason\":\"" + reason + "\"}");
        } catch (Exception ignored) {
        }
        ws.close();
    }
}
