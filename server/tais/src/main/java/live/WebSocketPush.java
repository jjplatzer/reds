package live;

import io.vertx.core.AbstractVerticle;
import io.vertx.core.Promise;
import io.vertx.core.eventbus.MessageConsumer;
import io.vertx.core.http.HttpMethod;
import io.vertx.core.http.HttpServer;
import io.vertx.core.http.HttpServerRequest;
import io.vertx.core.http.ServerWebSocket;
import io.vertx.core.json.JsonObject;
import store.TrackStore;

import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.Set;

/**
 * Public-facing live TAIS transport.
 *
 * TLS remains outside this JVM (Caddy on Lightsail). The Java service binds to
 * localhost and exposes /tais/ws plus /tais/healthz; Caddy can publish those
 * paths on the same reds-stdds-live host already used by SMES/ASDE-X.
 */
public final class WebSocketPush extends AbstractVerticle {

    private final Set<ClientConnection> clients = new LinkedHashSet<>();

    private ServerConfig config;
    private HttpServer server;
    private MessageConsumer<JsonObject> eventConsumer;

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
                    System.out.println("[TAIS ws] Listening on http://" +
                            config.host() + ":" + config.port() + config.wsPath() +
                            " health=" + config.healthPath() +
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
        for (ClientConnection client : new ArrayList<>(clients)) {
            client.close();
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
                    .put("protocol", 1)
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

        if (!config.authorized(socket.headers().get("Authorization"))) {
            System.err.println("[TAIS ws] Rejected unauthorized client");
            socket.reject(401);
            return;
        }

        if (clients.size() >= config.maxClients()) {
            System.err.println("[TAIS ws] Rejected client: max clients reached");
            socket.reject(503);
            return;
        }

        ClientConnection client = new ClientConnection(socket, config.pendingMax());
        clients.add(client);

        socket.closeHandler(ignored -> remove(client));
        socket.exceptionHandler(err -> {
            System.err.println("[TAIS ws] Client error: " + err.getMessage());
            client.close();
            remove(client);
        });

        // The TrackStore owns mutable target state. Request its immutable
        // snapshot instead of reaching across Vert.x event-loop ownership.
        vertx.eventBus().<JsonObject>request(TrackStore.SNAPSHOT_ADDRESS, "snapshot")
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

        System.out.println("[TAIS ws] Client connected; clients=" + clients.size());
    }

    private void broadcast(JsonObject frame) {
        for (ClientConnection client : new ArrayList<>(clients)) {
            client.onLiveFrame(frame);
        }
    }

    private void remove(ClientConnection client) {
        if (!clients.remove(client)) return;
        client.markClosed();
        System.out.println("[TAIS ws] Client disconnected; clients=" + clients.size());
    }
}
