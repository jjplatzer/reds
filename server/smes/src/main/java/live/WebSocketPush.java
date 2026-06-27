package live;

import io.vertx.core.AbstractVerticle;
import io.vertx.core.Promise;
import io.vertx.core.http.HttpServer;
import io.vertx.core.http.HttpServerOptions;
import io.vertx.core.http.HttpServerRequest;
import io.vertx.core.http.ServerWebSocket;
import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;
import playback.PlaybackBootstrapReader;
import playback.PlaybackCatalog;
import playback.PlaybackConfig;
import playback.PlaybackRangeReader;
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
    private ServerConfig config;
    private IpLimiter ipLimiter;
    private PlaybackConfig playbackConfig;

    @Override
    public void start(Promise<Void> startPromise) {
        config = ServerConfig.fromEnv();
        playbackConfig = PlaybackConfig.fromEnv();
        ipLimiter = new IpLimiter(config);

        HttpServer server = vertx.createHttpServer(new HttpServerOptions()
                .setMaxWebSocketFrameSize(config.maxInboundBytes));

        server.requestHandler(this::handleHttpRequest);

        server.webSocketHandler(ws -> {
            if (!"/ws".equals(ws.path())) {
                ws.close();
                return;
            }
            if (!authorized(ws, config)) {
                ws.close();
                return;
            }

            long nowMs = System.currentTimeMillis();
            String ip = ipLimiter.ipOf(ws);
            boolean replacingExistingIp = ipLimiter.hasClientForIp(ip);

            if (!replacingExistingIp && !ipLimiter.canAcceptGlobal(clients.size())) {
                ws.close();
                return;
            }

            if (!ipLimiter.canReconnect(ip, nowMs)) {
                ws.close();
                return;
            }

            ClientConnection client = new ClientConnection(ws, ip);
            ClientConnection old = ipLimiter.replaceForIp(ip, client);
            if (old != null) {
                removeClient(old.ws);
            }

            clients.put(ws, client);

            ws.closeHandler(v -> removeClient(ws));
            ws.exceptionHandler(e -> {
                removeClient(ws);
                ws.close();
            });

            vertx.setTimer(Math.max(1, config.subscribeTimeoutSeconds) * 1000L, timerId -> {
                ClientConnection current = clients.get(ws);
                if (current != null && current.airports.isEmpty() && !ws.isClosed()) {
                    current.sendLimitAndClose("subscribe_timeout");
                }
            });

            // Inbound: update only this client's subscription. TargetStore receives
            // the union of all client subscriptions, not one global client value.
            ws.textMessageHandler(text -> {
                long msgNowMs = System.currentTimeMillis();

                if (text == null || text.length() > config.maxInboundBytes) {
                    client.sendLimitAndClose("message_too_large");
                    return;
                }

                if (!client.allowMessage(config, msgNowMs)) {
                    client.sendLimitAndClose("message_rate_exceeded");
                    return;
                }

                JsonObject msg;
                try { msg = new JsonObject(text); } catch (Exception ignored) { return; }
                String type = msg.getString("type", "");

                if ("activity".equals(type)) {
                    client.markActivity(msgNowMs);
                    return;
                }

                if ("setAirports".equals(type)) {
                    client.markActivity(msgNowMs);
                    client.airports = parseAirports(
                            msg.getJsonArray("airports"),
                            config.maxAirportsPerConnection
                    );
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

        if (config.clientInactivitySeconds > 0) {
            long checkMs = Math.min(30_000L, Math.max(1_000L, config.clientInactivitySeconds * 500L));
            vertx.setPeriodic(checkMs, ignored -> closeInactiveClients());
        }

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
        ClientConnection client = clients.remove(ws);
        if (client != null) ipLimiter.remove(client);
        publishUnionFilter();
    }

    private void closeInactiveClients() {
        long nowMs = System.currentTimeMillis();

        for (ClientConnection client : clients.values()) {
            ServerWebSocket ws = client.ws;
            if (ws.isClosed()) continue;

            if (client.inactiveFor(config, nowMs)) {
                client.sendLimitAndClose("inactivity_timeout");
            }
        }
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

    private static Set<String> parseAirports(JsonArray arr, int maxAirports) {
        if (arr == null || arr.isEmpty() || maxAirports <= 0) return Set.of();

        Set<String> next = new HashSet<>(Math.min(arr.size(), maxAirports) * 2);
        for (int i = 0; i < arr.size(); i++) {
            if (next.size() >= maxAirports) break;

            String icao = arr.getString(i);
            if (icao == null) continue;
            icao = icao.strip().toUpperCase();
            if (icao.matches("[A-Z]{4}")) next.add(icao);
        }
        return Collections.unmodifiableSet(next);
    }

    private void handleHttpRequest(HttpServerRequest req) {
        if ("GET".equals(req.method().name()) && "/playback/availability".equals(req.path())) {
            String airport = req.getParam("airport");

            JsonObject body = PlaybackCatalog.availability(playbackConfig, airport);

            req.response()
                    .putHeader("content-type", "application/json")
                    .end(body.encode());
            return;
        }

        if ("GET".equals(req.method().name()) && "/playback/range".equals(req.path())) {
            String airport = req.getParam("airport");
            String from = req.getParam("from");
            String to = req.getParam("to");

            vertx.<PlaybackRangeReader.RangeResult>executeBlocking(promise -> {
                promise.complete(PlaybackRangeReader.readRange(playbackConfig, airport, from, to));
            }).onSuccess(result -> {
                req.response()
                        .setStatusCode(result.statusCode())
                        .putHeader("content-type", result.contentType())
                        .end(result.body());
            }).onFailure(err -> {
                req.response()
                        .setStatusCode(500)
                        .putHeader("content-type", "application/json")
                        .end(new JsonObject().put("error", err.getMessage()).encode());
            });

            return;
        }

        if ("GET".equals(req.method().name()) && "/playback/bootstrap".equals(req.path())) {
            String airport = req.getParam("airport");
            String at = req.getParam("at");

            vertx.<PlaybackBootstrapReader.BootstrapResult>executeBlocking(promise -> {
                promise.complete(PlaybackBootstrapReader.bootstrap(playbackConfig, airport, at));
            }).onSuccess(result -> {
                req.response()
                        .setStatusCode(result.statusCode())
                        .putHeader("content-type", result.contentType())
                        .end(result.body());
            }).onFailure(err -> {
                req.response()
                        .setStatusCode(500)
                        .putHeader("content-type", "application/json")
                        .end(new JsonObject().put("error", err.getMessage()).encode());
            });

            return;
        }

        req.response().setStatusCode(404).end("not found\n");
    }

}
