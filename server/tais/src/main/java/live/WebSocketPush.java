package live;

import io.vertx.core.AbstractVerticle;
import io.vertx.core.Promise;
import io.vertx.core.eventbus.MessageConsumer;
import io.vertx.core.http.HttpMethod;
import io.vertx.core.http.HttpServer;
import io.vertx.core.http.HttpServerRequest;
import io.vertx.core.http.ServerWebSocket;
import io.vertx.core.json.DecodeException;
import io.vertx.core.json.JsonObject;
import store.TrackStore;

import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashSet;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.UUID;

/**
 * Public-facing live TAIS transport.
 *
 * TLS remains outside this JVM (Caddy on Lightsail). The Java service binds to
 * localhost and exposes /tais/ws plus /tais/healthz; Caddy can publish those
 * paths on the same reds-stdds-live host already used by SMES/ASDE-X.
 */
public final class WebSocketPush extends AbstractVerticle {

    private final Set<ClientConnection> clients = new LinkedHashSet<>();
    private final Map<ClientConnection, String> subscriptions = new HashMap<>();

    private ServerConfig config;
    private HttpServer server;
    private MessageConsumer<JsonObject> eventConsumer;
    private long inactivityTimerId = -1;

    @Override
    public void start(Promise<Void> startPromise) {
        try {
            config = ServerConfig.fromEnv();
        } catch (Exception e) {
            startPromise.fail(e);
            return;
        }

        eventConsumer = vertx.eventBus().<JsonObject>localConsumer(
                TrackStore.EVENT_ADDRESS,
                message -> broadcast(message.body())
        );

        server = vertx.createHttpServer();
        server.requestHandler(this::handleHttp);
        server.webSocketHandler(this::handleWebSocket);

        server.listen(config.port(), config.host())
                .onSuccess(ignored -> {
                    if (config.clientInactivitySeconds() > 0) {
                        long checkMs = Math.min(30_000L,
                                Math.max(1_000L, config.clientInactivitySeconds() * 500L));
                        inactivityTimerId = vertx.setPeriodic(checkMs, timer -> closeInactiveClients());
                    }
                    System.out.println("[TAIS ws] Listening on http://" +
                            config.host() + ":" + config.port() + config.wsPath() +
                            " health=" + config.healthPath() +
                            " clientInactivitySeconds=" + config.clientInactivitySeconds());
                    startPromise.complete();
                })
                .onFailure(err -> {
                    if (eventConsumer != null) eventConsumer.unregister();
                    startPromise.fail(err);
                });
    }

    @Override
    public void stop(Promise<Void> stopPromise) {
        if (inactivityTimerId != -1) {
            vertx.cancelTimer(inactivityTimerId);
            inactivityTimerId = -1;
        }
        for (ClientConnection client : new ArrayList<>(clients)) {
            client.close();
            remove(client);
        }
        clients.clear();

        if (eventConsumer != null) {
            eventConsumer.unregister();
            eventConsumer = null;
        }

        if (server == null) {
            stopPromise.complete();
            return;
        }

        server.close().onComplete(ar -> {
            if (ar.succeeded()) stopPromise.complete();
            else stopPromise.fail(ar.cause());
        });
    }

    private void handleHttp(HttpServerRequest request) {
        if (config.healthPath().equals(request.path())) {
            if (request.method() != HttpMethod.GET) {
                request.response().setStatusCode(405).end();
                return;
            }

            JsonObject body = new JsonObject()
                    .put("ok", true)
                    .put("feed", "tais")
                    .put("protocol", 2)
                    .put("clients", clients.size());

            request.response()
                    .putHeader("Content-Type", "application/json")
                    .putHeader("Cache-Control", "no-store")
                    .end(body.encode());
            return;
        }

        request.response().setStatusCode(404).end();
    }

    private void handleWebSocket(ServerWebSocket socket) {
        if (!config.wsPath().equals(socket.path())) {
            socket.reject(404);
            return;
        }

        if (clients.size() >= config.maxClients()) {
            System.err.println("[TAIS ws] Rejected client: max clients reached");
            socket.reject(503);
            return;
        }

        String facility = requestedFacility(socket.query());
        if (facility == null) {
            System.err.println("[TAIS ws] Rejected client: missing/invalid facility scope");
            socket.reject(400);
            return;
        }

        ClientConnection client = new ClientConnection(socket, facility, config.pendingMax(),
                config.outboundMaxFrames(), config.outboundMaxBytes(), requestsPlans(socket.query()));
        clients.add(client);
        String subscriber = UUID.randomUUID().toString();
        subscriptions.put(client, subscriber);

        // Only explicit user activity refreshes the timer. Surveillance
        // updates and WebSocket ping/pong traffic must not keep an idle user on.
        socket.textMessageHandler(text -> {
            try {
                if ("activity".equals(new JsonObject(text).getValue("type"))) {
                    client.markActivity(System.currentTimeMillis());
                }
            } catch (DecodeException ignored) {
                // Malformed/unknown messages do not count as activity.
            }
        });

        socket.closeHandler(ignored -> remove(client));
        socket.exceptionHandler(err -> {
            System.err.println("[TAIS ws] Client error: " + err.getMessage());
            client.close();
            remove(client);
        });

        // The TrackStore owns mutable target state. Request its immutable
        // snapshot instead of reaching across Vert.x event-loop ownership.
        // Older desktops retain their preloaded trails. New desktops request
        // history=0 and warm up their local alerts while collecting live reports.
        JsonObject snapshotRequest = new JsonObject().put("facility", facility)
                .put("subscriber", subscriber)
                .put("includeHistory", !requestsNoHistory(socket.query()));
        vertx.eventBus().<JsonObject>request(TrackStore.SNAPSHOT_ADDRESS, snapshotRequest)
                .onSuccess(reply -> {
                    if (clients.contains(client)) {
                        client.onSnapshot(reply.body());
                    }
                })
                .onFailure(err -> {
                    System.err.println("[TAIS ws] Snapshot failed: " + err.getMessage());
                    client.close();
                    remove(client);
                });

        System.out.println("[TAIS ws] Client connected; facility=" + facility +
                " clients=" + clients.size());
    }

    private void broadcast(JsonObject frame) {
        for (ClientConnection client : new ArrayList<>(clients)) {
            client.onLiveFrame(frame);
        }
    }

    private void closeInactiveClients() {
        long nowMs = System.currentTimeMillis();
        for (ClientConnection client : new ArrayList<>(clients)) {
            if (client.inactiveFor(config, nowMs)) {
                System.out.println("[TAIS ws] Closing inactive client; facility=" + client.facility());
                client.closeForInactivity();
                remove(client);
            }
        }
    }

    private void remove(ClientConnection client) {
        if (!clients.remove(client)) return;
        client.markClosed();
        // Send from the same event-loop context and to the same consumer as
        // registration, so early closes cannot leave a stale subscription.
        vertx.eventBus().send(TrackStore.SNAPSHOT_ADDRESS, new JsonObject()
                .put("facility", client.facility())
                .put("subscriber", subscriptions.remove(client))
                .put("unsubscribe", true));
        System.out.println("[TAIS ws] Client disconnected; facility=" + client.facility() +
                " clients=" + clients.size());
    }

    private static String requestedFacility(String query) {
        if (query == null || query.isBlank()) return null;

        for (String pair : query.split("&")) {
            int equals = pair.indexOf('=');
            String rawKey = equals >= 0 ? pair.substring(0, equals) : pair;
            if (!"facility".equals(URLDecoder.decode(rawKey, StandardCharsets.UTF_8))) continue;

            String rawValue = equals >= 0 ? pair.substring(equals + 1) : "";
            String facility = URLDecoder.decode(rawValue, StandardCharsets.UTF_8)
                    .strip()
                    .toUpperCase(Locale.ROOT);
            return facility.matches("[A-Z0-9]{1,16}") ? facility : null;
        }

        return null;
    }

    private static boolean requestsPlans(String query) {
        if (query == null) return false;
        for (String pair : query.split("&")) {
            if (pair.equals("plans=1")) return true;
        }
        return false;
    }

    private static boolean requestsNoHistory(String query) {
        if (query == null) return false;
        for (String pair : query.split("&")) {
            if (pair.equals("history=0")) return true;
        }
        return false;
    }
}
