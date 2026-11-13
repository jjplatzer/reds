package live;

import config.Env;
import ingest.ItwsConsumer;
import io.vertx.core.AbstractVerticle;
import io.vertx.core.Promise;
import io.vertx.core.eventbus.MessageConsumer;
import io.vertx.core.http.HttpMethod;
import io.vertx.core.http.HttpServer;
import io.vertx.core.http.HttpServerOptions;
import io.vertx.core.http.HttpServerRequest;
import io.vertx.core.http.ServerWebSocket;
import io.vertx.core.json.DecodeException;
import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;
import store.ItwsStore;

import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashSet;
import java.util.Set;
import java.util.Locale;
import java.util.TreeSet;

/** Local HTTP/WebSocket transport; TLS is terminated by the existing reverse proxy. */
public final class WebSocketPush extends AbstractVerticle {
    private final ItwsConsumer ingest;
    private final Set<Client> clients = new LinkedHashSet<>();
    private HttpServer server;
    private MessageConsumer<JsonObject> events;
    private long inactivityTimer = -1;
    private int inactivitySeconds, maxClients, pendingMax, queueBytes;
    private String wsPath, healthPath, snapshotPath;

    public WebSocketPush(ItwsConsumer ingest) { this.ingest = ingest; }

    @Override
    public void start(Promise<Void> promise) {
        try {
            String host = Env.value("ITWS_WS_HOST", "127.0.0.1");
            int port = Env.integer("ITWS_WS_PORT", 8084, 1, 65_535);
            wsPath = path("ITWS_WS_PATH", "/itws/ws");
            healthPath = path("ITWS_HEALTH_PATH", "/itws/healthz");
            snapshotPath = path("ITWS_SNAPSHOT_PATH", "/itws/products");
            if (wsPath.equals(healthPath) || wsPath.equals(snapshotPath) || healthPath.equals(snapshotPath))
                throw new IllegalArgumentException("ITWS endpoint paths must be distinct");
            maxClients = Env.integer("ITWS_WS_MAX_CLIENTS", 64, 1, 10_000);
            pendingMax = Env.integer("ITWS_WS_PENDING_MAX", 128, 1, 10_000);
            queueBytes = Env.integer("ITWS_WS_QUEUE_BYTES", 8 << 20, 64 << 10, 128 << 20);
            inactivitySeconds = Env.integer("REDS_CLIENT_INACTIVITY_SECONDS", 600, 0, Integer.MAX_VALUE);
            events = vertx.eventBus().localConsumer(ItwsStore.EVENT_ADDRESS, message -> {
                for (Client client : new ArrayList<>(clients)) client.onUpdate(message.body());
            });
            server = vertx.createHttpServer(new HttpServerOptions()
                    .setMaxWebSocketFrameSize(4096).setMaxWebSocketMessageSize(4096));
            server.requestHandler(this::http).webSocketHandler(this::webSocket);
            server.listen(port, host).onSuccess(ignored -> {
                if (inactivitySeconds > 0) {
                    inactivityTimer = vertx.setPeriodic(Math.min(30_000L, Math.max(1_000L, inactivitySeconds * 500L)), timer -> {
                        long now = System.nanoTime();
                        for (Client client : new ArrayList<>(clients)) {
                            if (now - client.lastActivity >= inactivitySeconds * 1_000_000_000L)
                                client.close((short) 1000, "Client inactivity timeout");
                        }
                    });
                }
                System.out.println("[ITWS ws] Listening on " + host + ":" + port + wsPath);
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
        JsonObject query;
        try { query = query(sites(request.getParam("site"))).put("statsOnly", healthPath.equals(request.path()))
                .put("includeHistory", "1".equals(request.getParam("history"))); }
        catch (IllegalArgumentException e) { request.response().setStatusCode(400).end(new JsonObject().put("error", "Invalid site filter").encode()); return; }
        vertx.eventBus().<JsonObject>request(ItwsStore.SNAPSHOT_ADDRESS, query).onSuccess(reply -> {
            JsonObject snapshot = reply.body();
            if (healthPath.equals(request.path())) {
                request.response().end(new JsonObject().put("ok", true).put("feed", "itws").put("protocol", 1)
                        .put("clients", clients.size()).mergeIn(snapshot).mergeIn(ingest.status()).encode());
            } else request.response().end(snapshot.encode());
        }).onFailure(error -> request.response().setStatusCode(503)
                .end(new JsonObject().put("ok", false).put("error", "ITWS store unavailable").encode()));
    }

    private void webSocket(ServerWebSocket socket) {
        if (!wsPath.equals(socket.path())) { socket.reject(404); return; }
        if (clients.size() >= maxClients) { socket.reject(503); return; }
        Set<String> sites;
        boolean includeHistory;
        try {
            sites = sites(queryParameter(socket.query(), "site"));
            includeHistory = "1".equals(queryParameter(socket.query(), "history"));
        }
        catch (IllegalArgumentException e) { socket.reject(400); return; }
        socket.setWriteQueueMaxSize(queueBytes);
        Client client = new Client(socket, sites);
        clients.add(client);
        socket.closeHandler(ignored -> client.remove());
        socket.exceptionHandler(error -> client.close((short) 1011, "Transport error"));
        socket.textMessageHandler(text -> {
            try {
                if ("activity".equals(new JsonObject(text).getString("type"))) client.lastActivity = System.nanoTime();
            } catch (DecodeException | ClassCastException ignored) {}
        });
        vertx.eventBus().<JsonObject>request(ItwsStore.SNAPSHOT_ADDRESS, query(sites).put("includeHistory", includeHistory))
                .onSuccess(reply -> client.onSnapshot(reply.body()))
                .onFailure(error -> client.close((short) 1011, "Snapshot unavailable"));
    }

    private final class Client {
        final ServerWebSocket socket;
        final Set<String> sites;
        final ArrayDeque<JsonObject> pending = new ArrayDeque<>();
        long lastActivity = System.nanoTime();
        long revision;
        boolean waitingForSnapshot = true, closed;

        Client(ServerWebSocket socket, Set<String> sites) { this.socket = socket; this.sites = sites; }

        void onUpdate(JsonObject frame) {
            if (closed) return;
            String site = "update".equals(frame.getString("type"))
                    ? frame.getJsonObject("product").getString("site") : frame.getString("site");
            if (!sites.isEmpty() && !sites.contains(site)) return;
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
            int limit = Math.min(queueBytes, 16 << 20); // Also respect net/itws.go's reader cap.
            String encoded = encodeWithinHistoryLimit(frame, limit);
            if (socket.writeQueueFull() || encoded.getBytes(StandardCharsets.UTF_8).length > limit) {
                close((short) 1013, "Client too slow or snapshot too large; select a site");
                return;
            }
            revision = frame.getLong("revision");
            socket.writeTextMessage(encoded).onFailure(error -> close((short) 1011, "Write failed"));
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

    // Bootstrap history is optional. Fit it to the transport budget by dropping
    // oldest saved pictures, preserving their null slots and all live weather.
    // This operates on the private snapshot reply, never on store-owned grids.
    private static String encodeWithinHistoryLimit(JsonObject frame, int limit) {
        String encoded = frame.encode();
        int bytes = encoded.getBytes(StandardCharsets.UTF_8).length;
        JsonArray history = frame.getJsonArray("history");
        if (bytes <= limit || history == null) return encoded;
        record Saved(JsonArray pictures, int index, Instant generated, int bytes) {}
        var saved = new ArrayList<Saved>();
        for (Object value : history) {
            JsonArray pictures = ((JsonObject) value).getJsonArray("products");
            for (int i = 0; i < pictures.size(); i++) {
                JsonObject picture = pictures.getJsonObject(i);
                if (picture != null) saved.add(new Saved(pictures, i,
                        Instant.parse(picture.getString("generationTime")),
                        picture.encode().getBytes(StandardCharsets.UTF_8).length));
            }
        }
        saved.sort(Comparator.comparing(Saved::generated));
        for (Saved picture : saved) {
            if (bytes <= limit) break;
            picture.pictures.set(picture.index, null);
            bytes -= picture.bytes - 4; // Replacing one JSON object with null.
        }
        return frame.encode();
    }

    private static JsonObject query(Set<String> sites) {
        return new JsonObject().put("sites", new JsonArray(new ArrayList<>(sites)));
    }

    private static Set<String> sites(String value) {
        Set<String> result = new TreeSet<>();
        if (value == null || value.isBlank()) return result;
        for (String item : value.toUpperCase(Locale.ROOT).split(",")) {
            String code = item.strip();
            if (!code.matches("[A-Z0-9_-]{1,32}")) throw new IllegalArgumentException("Invalid site");
            result.add(code);
        }
        return result.contains("ALL") ? Set.of() : result;
    }

    private static String queryParameter(String query, String key) {
        if (query == null) return null;
        for (String part : query.split("&")) {
            String[] pair = part.split("=", 2);
            if (URLDecoder.decode(pair[0], StandardCharsets.UTF_8).equals(key))
                return URLDecoder.decode(pair.length == 2 ? pair[1] : "", StandardCharsets.UTF_8);
        }
        return null;
    }

    private static String path(String key, String fallback) {
        String value = Env.value(key, fallback);
        if (!value.startsWith("/")) value = "/" + value;
        while (value.length() > 1 && value.endsWith("/")) value = value.substring(0, value.length() - 1);
        return value;
    }
}
