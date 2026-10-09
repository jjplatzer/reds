package live;

import io.vertx.core.AbstractVerticle;
import io.vertx.core.Promise;
import io.vertx.core.eventbus.MessageConsumer;
import io.vertx.core.http.HttpMethod;
import io.vertx.core.http.HttpServer;
import io.vertx.core.http.HttpServerRequest;
import io.vertx.core.http.ServerWebSocket;
import io.vertx.core.json.JsonObject;
import store.SfdpsStore;
import wire.SfdpsWire;

import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.Locale;
import java.util.Set;

/** Public-facing normalized SFDPS transport. TLS remains at Caddy. */
public final class WebSocketPush extends AbstractVerticle {
    private final Set<ClientConnection> clients = new LinkedHashSet<>();
    private ServerConfig config;
    private HttpServer server;
    private MessageConsumer<JsonObject> eventConsumer;

    @Override
    public void start(Promise<Void> startPromise) {
        try { config = ServerConfig.fromEnv(); }
        catch (Exception e) { startPromise.fail(e); return; }

        eventConsumer = vertx.eventBus().<JsonObject>localConsumer(SfdpsStore.EVENT_ADDRESS,
                message -> broadcast(message.body()));

        server = vertx.createHttpServer();
        server.requestHandler(this::handleHttp);
        server.webSocketHandler(this::handleWebSocket);
        server.listen(config.port(), config.host())
                .onSuccess(ignored -> {
                    System.out.println("[SFDPS ws] Listening on http://" + config.host() + ":" + config.port() +
                            config.wsPath() + " health=" + config.healthPath() +
                            " auth=" + (config.authEnabled() ? "enabled" : "disabled"));
                    startPromise.complete();
                })
                .onFailure(err -> {
                    if (eventConsumer != null) eventConsumer.unregister();
                    startPromise.fail(err);
                });
    }

    @Override
    public void stop(Promise<Void> stopPromise) {
        for (ClientConnection client : new ArrayList<>(clients)) client.close();
        clients.clear();
        if (eventConsumer != null) { eventConsumer.unregister(); eventConsumer = null; }
        if (server == null) { stopPromise.complete(); return; }
        server.close().onComplete(ar -> { if (ar.succeeded()) stopPromise.complete(); else stopPromise.fail(ar.cause()); });
    }

    private void handleHttp(HttpServerRequest request) {
        if (config.healthPath().equals(request.path())) {
            if (request.method() != HttpMethod.GET) { request.response().setStatusCode(405).end(); return; }
            request.response()
                    .putHeader("Content-Type", "application/json")
                    .putHeader("Cache-Control", "no-store")
                    .end(new JsonObject().put("ok", true).put("feed", SfdpsWire.FEED)
                            .put("protocol", SfdpsWire.PROTOCOL).put("clients", clients.size()).encode());
            return;
        }
        request.response().setStatusCode(404).end();
    }

    private void handleWebSocket(ServerWebSocket socket) {
        if (!config.wsPath().equals(socket.path())) { socket.reject(404); return; }
        if (!config.authorized(socket.headers().get("Authorization"))) { socket.reject(401); return; }
        if (clients.size() >= config.maxClients()) { socket.reject(503); return; }
        String facility = requestedFacility(socket.query());
        if (facility == null) { socket.reject(400); return; }

        ClientConnection client = new ClientConnection(socket, facility, config.pendingMax());
        clients.add(client);
        socket.closeHandler(ignored -> remove(client));
        socket.exceptionHandler(err -> { client.close(); remove(client); });

        vertx.eventBus().<JsonObject>request(SfdpsStore.SNAPSHOT_ADDRESS, facility)
                .onSuccess(reply -> { if (clients.contains(client)) client.onSnapshot(reply.body()); })
                .onFailure(err -> { client.close(); remove(client); });

        System.out.println("[SFDPS ws] Client connected; facility=" + facility + " clients=" + clients.size());
    }

    private void broadcast(JsonObject frame) {
        for (ClientConnection client : new ArrayList<>(clients)) client.onLiveFrame(frame);
    }

    private void remove(ClientConnection client) {
        if (!clients.remove(client)) return;
        client.markClosed();
        System.out.println("[SFDPS ws] Client disconnected; facility=" + client.facility() + " clients=" + clients.size());
    }

    private static String requestedFacility(String query) {
        if (query == null || query.isBlank()) return null;
        for (String pair : query.split("&")) {
            int equals = pair.indexOf('=');
            String rawKey = equals >= 0 ? pair.substring(0, equals) : pair;
            if (!"facility".equals(URLDecoder.decode(rawKey, StandardCharsets.UTF_8))) continue;
            String rawValue = equals >= 0 ? pair.substring(equals + 1) : "";
            String facility = URLDecoder.decode(rawValue, StandardCharsets.UTF_8).strip().toUpperCase(Locale.ROOT);
            return facility.matches("Z[A-Z0-9]{2,3}") ? facility : null;
        }
        return null;
    }
}
