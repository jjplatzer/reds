package live;

import config.Env;
import ingest.AptcConsumer;
import io.vertx.core.AbstractVerticle;
import io.vertx.core.Promise;
import io.vertx.core.eventbus.MessageConsumer;
import io.vertx.core.http.HttpMethod;
import io.vertx.core.http.HttpServer;
import io.vertx.core.http.HttpServerOptions;
import io.vertx.core.http.HttpServerRequest;
import io.vertx.core.http.ServerWebSocket;
import io.vertx.core.json.DecodeException;
import io.vertx.core.json.JsonObject;
import store.AptcStore;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.Set;

/** Local HTTP/WebSocket transport; TLS is terminated by the existing reverse proxy. */
public final class WebSocketPush extends AbstractVerticle {
    private final AptcConsumer ingest;
    private final Set<Client> clients = new LinkedHashSet<>();
    private HttpServer server;
    private MessageConsumer<JsonObject> events;
    private long inactivityTimer = -1;
    private int inactivitySeconds, maxClients, pendingMax;
    private String wsPath, healthPath, snapshotPath;

    public WebSocketPush(AptcConsumer ingest) { this.ingest = ingest; }

    @Override
    public void start(Promise<Void> promise) {
        try {
            String host = Env.value("APTC_WS_HOST", "127.0.0.1");
            int port = Env.integer("APTC_WS_PORT", 8083, 1, 65_535);
            wsPath = path("APTC_WS_PATH", "/aptc/ws");
            healthPath = path("APTC_HEALTH_PATH", "/aptc/healthz");
            snapshotPath = path("APTC_SNAPSHOT_PATH", "/aptc/configurations");
            if (wsPath.equals(healthPath) || wsPath.equals(snapshotPath) || healthPath.equals(snapshotPath))
                throw new IllegalArgumentException("APTC endpoint paths must be distinct");
            maxClients = Env.integer("APTC_WS_MAX_CLIENTS", 64, 1, 10_000);
            pendingMax = Env.integer("APTC_WS_PENDING_MAX", 256, 1, 100_000);
            inactivitySeconds = Env.integer("REDS_CLIENT_INACTIVITY_SECONDS", 600, 0, Integer.MAX_VALUE);
            events = vertx.eventBus().localConsumer(AptcStore.EVENT_ADDRESS, message -> {
                for (Client client : new ArrayList<>(clients)) client.onUpdate(message.body());
            });
            server = vertx.createHttpServer(new HttpServerOptions()
                    .setMaxWebSocketFrameSize(4096).setMaxWebSocketMessageSize(4096));
            server.requestHandler(this::http).webSocketHandler(this::webSocket);
            server.listen(port, host).onSuccess(ignored -> {
                if (inactivitySeconds > 0) {
                    inactivityTimer = vertx.setPeriodic(Math.min(30_000L, Math.max(1_000L, inactivitySeconds * 500L)), timer -> {
                        long now = System.currentTimeMillis();
                        for (Client client : new ArrayList<>(clients)) {
                            if (now - client.lastActivity >= inactivitySeconds * 1_000L)
                                client.close((short) 1000, "Client inactivity timeout");
                        }
                    });
                }
                System.out.println("[APTC ws] Listening on " + host + ":" + port + wsPath);
                promise.complete();
            }).onFailure(error -> {
                events.unregister();
                promise.fail(error);
            });
        } catch (Exception e) {
            promise.fail(e);
        }
    }

    @Override
    public void stop(Promise<Void> promise) {
        if (inactivityTimer != -1) vertx.cancelTimer(inactivityTimer);
        if (events != null) events.unregister();
        for (Client client : new ArrayList<>(clients)) client.close((short) 1001, "Service stopping");
        if (server == null) promise.complete();
        else server.close().onComplete(result -> {
            if (result.succeeded()) promise.complete();
            else promise.fail(result.cause());
        });
    }

    private void http(HttpServerRequest request) {
        if (!healthPath.equals(request.path()) && !snapshotPath.equals(request.path())) {
            request.response().setStatusCode(404).end();
            return;
        }
        if (request.method() != HttpMethod.GET) {
            request.response().setStatusCode(405).putHeader("Allow", "GET").end();
            return;
        }
        request.response().putHeader("Content-Type", "application/json").putHeader("Cache-Control", "no-store");
        vertx.eventBus().<JsonObject>request(AptcStore.SNAPSHOT_ADDRESS, "").onSuccess(reply -> {
            JsonObject snapshot = reply.body();
            if (healthPath.equals(request.path())) {
                request.response().end(new JsonObject().put("ok", true).put("feed", "aptc").put("protocol", 1)
                        .put("clients", clients.size()).put("revision", snapshot.getLong("revision"))
                        .put("airports", snapshot.getJsonArray("configurations").size())
                        .put("outOfOrder", snapshot.getLong("outOfOrder")).mergeIn(ingest.status()).encode());
            } else request.response().end(snapshot.encode());
        }).onFailure(error -> request.response().setStatusCode(503)
                .end(new JsonObject().put("ok", false).put("error", "APTC store unavailable").encode()));
    }

    private void webSocket(ServerWebSocket socket) {
        if (!wsPath.equals(socket.path())) { socket.reject(404); return; }
        if (clients.size() >= maxClients) { socket.reject(503); return; }
        socket.setWriteQueueMaxSize(2 << 20);
        Client client = new Client(socket);
        clients.add(client);
        socket.closeHandler(ignored -> client.remove());
        socket.exceptionHandler(error -> client.close((short) 1011, "Transport error"));
        socket.textMessageHandler(text -> {
            try {
                if ("activity".equals(new JsonObject(text).getString("type"))) client.lastActivity = System.currentTimeMillis();
            } catch (DecodeException | ClassCastException ignored) {}
        });
        vertx.eventBus().<JsonObject>request(AptcStore.SNAPSHOT_ADDRESS, "")
                .onSuccess(reply -> client.onSnapshot(reply.body()))
                .onFailure(error -> client.close((short) 1011, "Snapshot unavailable"));
    }

    private final class Client {
        final ServerWebSocket socket;
        final ArrayDeque<JsonObject> pending = new ArrayDeque<>();
        long lastActivity = System.currentTimeMillis();
        long revision;
        boolean waitingForSnapshot = true, closed;

        Client(ServerWebSocket socket) { this.socket = socket; }

        void onUpdate(JsonObject frame) {
            if (closed) return;
            if (waitingForSnapshot) {
                if (pending.size() >= pendingMax) close((short) 1013, "Snapshot backlog exceeded");
                else pending.addLast(frame);
            } else if (frame.getLong("revision") > revision) send(frame);
        }

        void onSnapshot(JsonObject snapshot) {
            if (closed) return;
            send(snapshot);
            waitingForSnapshot = false;
            while (!pending.isEmpty() && !closed) {
                JsonObject frame = pending.removeFirst();
                if (frame.getLong("revision") > revision) send(frame);
            }
        }

        void send(JsonObject frame) {
            if (closed) return;
            if (socket.writeQueueFull()) { close((short) 1013, "Client too slow"); return; }
            revision = frame.getLong("revision");
            socket.writeTextMessage(frame.encode()).onFailure(error -> close((short) 1011, "Write failed"));
        }

        void close(short code, String reason) {
            if (closed) return;
            remove();
            socket.close(code, reason);
        }

        void remove() {
            closed = true;
            pending.clear();
            clients.remove(this);
        }
    }

    private static String path(String key, String fallback) {
        String value = Env.value(key, fallback);
        if (!value.startsWith("/")) value = "/" + value;
        while (value.length() > 1 && value.endsWith("/")) value = value.substring(0, value.length() - 1);
        return value;
    }
}
