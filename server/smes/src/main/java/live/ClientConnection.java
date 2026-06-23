package live;

import io.vertx.core.http.ServerWebSocket;

import java.util.Set;

final class ClientConnection {
    final ServerWebSocket ws;
    volatile Set<String> airports = Set.of();

    ClientConnection(ServerWebSocket ws) {
        this.ws = ws;
    }

    boolean accepts(String airport) {
        return airport != null && !airport.isBlank() && airports.contains(airport);
    }
}
