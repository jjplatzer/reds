package live;

import io.vertx.core.AbstractVerticle;
import io.vertx.core.Promise;
import io.vertx.core.http.HttpServer;
import io.vertx.core.http.HttpServerOptions;
import io.vertx.core.http.ServerWebSocket;
import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;
import store.AirportFilter;
import store.TargetStore;

import java.util.Collections;
import java.util.HashSet;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;

/**
 * WebSocket push verticle.
 *
 * Pipeline position:
 *   EventBus({@value TargetStore#DIFF_ADDRESS}) → per-client filter → WebSocket clients
 *
 * Responsibilities:
 *   1. Serve a WebSocket endpoint at ws://{host}:{port}/ws.
 *   2. Optionally require REDS_WS_TOKEN as a Bearer token.
 *   3. Track each client's own airport subscription.
 *   4. Publish the union of all client subscriptions to TargetStore.
 *   5. Fan-out each diff frame only to matching clients.
 */
public final class WebSocketPush extends AbstractVerticle {

    private final Map<ServerWebSocket, ClientConnection> clients = new ConcurrentHashMap<>();

    @Override
    public void start(Promise<Void> startPromise) {
        ServerConfig config = ServerConfig.fromEnv();

        HttpServer server = vertx.createHttpServer(new HttpServerOptions()
                .setMaxWebSocketFrameSize(config.maxInboundBytes));

        server.webSocketHandler(ws -> {
            if (!"/ws".equals(ws.path())) {
                ws.close();
                return;
            }
            if (!authorized(ws, config)) {
                ws.close();
                return;
            }

            ClientConnection client = new ClientConnection(ws);
            clients.put(ws, client);

            ws.closeHandler(v -> removeClient(ws));
            ws.exceptionHandler(e -> {
                removeClient(ws);
                ws.close();
            });

            // Inbound: update only this client's subscription. TargetStore receives
            // the union of all client subscriptions, not one global client value.
            ws.textMessageHandler(text -> {
                JsonObject msg;
                try { msg = new JsonObject(text); } catch (Exception ignored) { return; }
                if ("setAirports".equals(msg.getString("type", ""))) {
                    client.airports = parseAirports(msg.getJsonArray("airports"));
                    publishUnionFilter();
                }
            });

            ws.writeTextMessage(new JsonObject()
                    .put("type", "connected")
                    .put("clients", clients.size())
                    .encode());
        });

        // Subscribe to diffs and fan-out. encode() once, write String to matching clients.
        // Clients whose write queue is full are skipped — stale frames are worthless.
        vertx.eventBus().<JsonObject>consumer(TargetStore.DIFF_ADDRESS, msg -> {
            JsonObject body = msg.body();
            String frame = body.encode();
            String airport = body.getString("airport", "");
            for (ClientConnection client : clients.values()) {
                ServerWebSocket ws = client.ws;
                if (!client.accepts(airport)) continue;
                if (!ws.isClosed() && !ws.writeQueueFull()) ws.writeTextMessage(frame);
            }
        });

        server.listen(config.port, config.host)
                .onSuccess(s -> {
                    System.out.println("[WS] Listening on ws://" + config.host + ":" + config.port
                            + "/ws (auth=" + config.authModeLabel()
                            + ", publicMode=" + config.publicMode
                            + ", maxClients=" + config.maxClients + ")");
                    startPromise.complete();
                })
                .onFailure(startPromise::fail);
    }

    private void removeClient(ServerWebSocket ws) {
        clients.remove(ws);
        publishUnionFilter();
    }

    private void publishUnionFilter() {
        Set<String> union = new HashSet<>();
        for (ClientConnection client : clients.values()) union.addAll(client.airports);

        JsonArray airports = new JsonArray();
        union.stream().sorted().forEach(airports::add);
        vertx.eventBus().publish(AirportFilter.ADDRESS, new JsonObject().put("airports", airports));
    }

    private static boolean authorized(ServerWebSocket ws, ServerConfig config) {
        if (!config.requireAuth) return true;
        if (config.requiredToken == null || config.requiredToken.isBlank()) return false;

        String header = ws.headers().get("Authorization");
        if (header == null) return false;

        String prefix = "Bearer ";
        if (!header.startsWith(prefix)) return false;
        return constantTimeEquals(config.requiredToken, header.substring(prefix.length()).strip());
    }

    private static boolean constantTimeEquals(String a, String b) {
        if (a == null || b == null) return false;
        int diff = a.length() ^ b.length();
        int n = Math.min(a.length(), b.length());
        for (int i = 0; i < n; i++) diff |= a.charAt(i) ^ b.charAt(i);
        return diff == 0;
    }

    private static Set<String> parseAirports(JsonArray arr) {
        if (arr == null || arr.isEmpty()) return Set.of();

        Set<String> next = new HashSet<>(arr.size() * 2);
        for (int i = 0; i < arr.size(); i++) {
            String icao = arr.getString(i);
            if (icao == null) continue;
            icao = icao.strip().toUpperCase();
            if (icao.matches("[A-Z]{4}")) next.add(icao);
        }
        return Collections.unmodifiableSet(next);
    }

}
