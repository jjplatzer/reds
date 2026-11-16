package ingest;

import com.solacesystems.jms.SolConnectionFactory;
import com.solacesystems.jms.SolJmsUtility;
import config.Env;
import io.vertx.core.AbstractVerticle;
import io.vertx.core.Promise;
import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;

import javax.jms.BytesMessage;
import javax.jms.Connection;
import javax.jms.JMSException;
import javax.jms.Message;
import javax.jms.MessageConsumer;
import javax.jms.Session;
import javax.jms.TextMessage;
import javax.xml.parsers.DocumentBuilder;
import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Arrays;
import java.util.List;
import java.util.Locale;
import java.util.Set;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import java.util.stream.Collectors;

/** One independent ITWS queue; parsing and broker I/O run outside the Vert.x event loop. */
public final class ItwsConsumer extends AbstractVerticle {
    private Thread worker;
    private volatile boolean running, connected;
    private volatile Instant lastMessageAt;
    private final AtomicLong deliveries = new AtomicLong();
    private final AtomicLong parsed = new AtomicLong();
    private final AtomicLong accepted = new AtomicLong();
    private final AtomicLong filtered = new AtomicLong();
    private final AtomicLong parseErrors = new AtomicLong();
    private final AtomicLong invalidMessages = new AtomicLong();
    private final AtomicLong ignoredMessages = new AtomicLong();
    private final AtomicLong reconnects = new AtomicLong();

    @Override
    public void start(Promise<Void> promise) {
        try {
            Config config = Config.fromEnv();
            DocumentBuilder parser = ItwsParser.newDocumentBuilder();
            running = true;
            worker = new Thread(() -> run(config, parser), "itws-consumer");
            worker.setDaemon(true);
            worker.start();
            promise.complete();
        } catch (Exception e) {
            promise.fail(e);
        }
    }

    @Override
    public void stop(Promise<Void> promise) {
        running = false;
        if (worker == null) { promise.complete(); return; }
        worker.interrupt();
        vertx.executeBlocking(() -> { worker.join(3_000); return null; })
                .onComplete(result -> {
                    if (result.succeeded()) promise.complete();
                    else promise.fail(result.cause());
                });
    }

    public JsonObject status() {
        Instant last = lastMessageAt;
        return new JsonObject().put("jmsConnected", connected)
                .put("lastMessageAt", last == null ? null : last.toString())
                .put("deliveries", deliveries.get()).put("parsedProducts", parsed.get())
                .put("acceptedProducts", accepted.get()).put("filteredProducts", filtered.get())
                .put("parseErrors", parseErrors.get()).put("invalidMessages", invalidMessages.get())
                .put("ignoredMessages", ignoredMessages.get()).put("reconnects", reconnects.get());
    }

    private void run(Config config, DocumentBuilder parser) {
        int delay = config.reconnectInitialMs;
        while (running && !Thread.currentThread().isInterrupted()) {
            try {
                consume(config, parser);
            } catch (Exception e) {
                if (!running || Thread.currentThread().isInterrupted()) break;
                System.err.println("[ITWS] Connection/ingest failed (" + e.getClass().getSimpleName() + "); reconnecting");
            } finally {
                connected = false;
            }
            if (!running || Thread.currentThread().isInterrupted()) break;
            reconnects.incrementAndGet();
            try { Thread.sleep(delay); }
            catch (InterruptedException e) { Thread.currentThread().interrupt(); break; }
            delay = (int) Math.min(config.reconnectMaxMs, (long) delay * 2);
        }
    }

    private void consume(Config config, DocumentBuilder parser) throws Exception {
        SolConnectionFactory factory = SolJmsUtility.createConnectionFactory();
        factory.setHost(config.url);
        factory.setVPN(config.vpn);
        factory.setUsername(config.username);
        factory.setPassword(config.password);
        factory.setConnectRetries(0);
        factory.setConnectRetriesPerHost(0);
        factory.setConnectTimeoutInMillis(10_000);
        factory.setReadTimeoutInMillis(10_000);
        factory.setReconnectRetries(0); // This worker owns bounded, interruptible backoff.
        AtomicReference<JMSException> failure = new AtomicReference<>();
        try (Connection connection = factory.createConnection()) {
            connection.setExceptionListener(failure::set);
            try (Session session = connection.createSession(false, Session.CLIENT_ACKNOWLEDGE);
                 MessageConsumer consumer = session.createConsumer(session.createQueue(config.queue))) {
                connection.start();
                connected = true;
                System.out.println("[ITWS] Connected; sites=" + config.sites + " sources=" + config.sources);
                long statsAt = System.nanoTime();
                while (running && !Thread.currentThread().isInterrupted()) {
                    if (failure.get() != null) throw failure.get();
                    Message message = consumer.receive(1_000);
                    if (message != null) {
                        deliveries.incrementAndGet();
                        lastMessageAt = Instant.now();
                        ItwsParser.Batch batch;
                        try {
                            byte[] xml = payload(message, config.maxBytes);
                            batch = ItwsParser.parse(xml, parser, lastMessageAt);
                        } catch (Exception e) {
                            parseErrors.incrementAndGet();
                            System.err.println("[ITWS] Rejected XML/body (" + e.getClass().getSimpleName() + ")");
                            message.acknowledge(); // Poison messages must not redeliver forever.
                            continue;
                        }
                        parsed.addAndGet(batch.products().size());
                        invalidMessages.addAndGet(batch.invalidMessages());
                        ignoredMessages.addAndGet(batch.ignoredMessages());
                        List<JsonObject> kept = batch.products().stream().filter(product ->
                                (config.sites.isEmpty() || config.sites.contains(product.getString("site")))
                                && (config.sources.isEmpty() || config.sources.contains(product.getString("source"))))
                                .toList();
                        filtered.addAndGet(batch.products().size() - kept.size());
                        if (!kept.isEmpty()) {
                            // The worker can block; the Vert.x event loop never waits on JMS.
                            // A store failure leaves this delivery unacknowledged for redelivery.
                            var reply = vertx.eventBus().<JsonObject>request(ItwsParser.ADDRESS, new JsonArray(kept))
                                    .toCompletionStage().toCompletableFuture().get(10, TimeUnit.SECONDS);
                            accepted.addAndGet(reply.body().getInteger("accepted", 0));
                        }
                        message.acknowledge();
                    }
                    if (System.nanoTime() - statsAt >= config.statsIntervalMs * 1_000_000L) {
                        System.out.println("[ITWS] " + status().encode());
                        statsAt = System.nanoTime();
                    }
                }
            }
        }
    }

    private static byte[] payload(Message message, int maxBytes) throws Exception {
        if (message instanceof TextMessage text) {
            String value = text.getText();
            byte[] body = value == null ? new byte[0] : value.getBytes(StandardCharsets.UTF_8);
            if (body.length > maxBytes) throw new IllegalArgumentException("Oversized message");
            return body;
        }
        if (message instanceof BytesMessage bytes) {
            long size = bytes.getBodyLength();
            if (size < 0 || size > maxBytes) throw new IllegalArgumentException("Oversized message");
            ByteArrayOutputStream out = new ByteArrayOutputStream((int) size);
            byte[] buffer = new byte[8192];
            int count;
            while ((count = bytes.readBytes(buffer)) != -1) {
                if (count == 0 || out.size() + count > maxBytes) throw new IllegalArgumentException("Invalid body length");
                out.write(buffer, 0, count);
            }
            if (out.size() != size) throw new IllegalArgumentException("Incomplete message");
            return out.toByteArray();
        }
        throw new IllegalArgumentException("Unsupported JMS body");
    }

    private record Config(String url, String vpn, String username, String password, String queue,
                          Set<String> sites, Set<String> sources, int maxBytes,
                          int statsIntervalMs, int reconnectInitialMs, int reconnectMaxMs) {
        static Config fromEnv() {
            int initial = Env.integer("ITWS_RECONNECT_INITIAL_MS", 5_000, 1_000, 60_000);
            return new Config(Env.required("ITWS_JMS_URL"), Env.required("ITWS_VPN"),
                    Env.required("SCDS_USERNAME"), Env.required("SCDS_PASSWORD"), Env.required("ITWS_QUEUE"),
                    codes("ITWS_SITES"), codes("ITWS_SOURCES"),
                    Env.integer("ITWS_MAX_BYTES", ItwsParser.MAX_BYTES, 1024, ItwsParser.MAX_BYTES),
                    Env.integer("ITWS_STATS_INTERVAL_MS", 30_000, 1_000, 3_600_000), initial,
                    Env.integer("ITWS_RECONNECT_MAX_MS", 60_000, initial, 600_000));
        }

        private static Set<String> codes(String key) {
            Set<String> codes = Arrays.stream(Env.value(key, "").split(","))
                    .map(String::strip).filter(value -> !value.isEmpty())
                    .map(value -> value.toUpperCase(Locale.ROOT)).collect(Collectors.toUnmodifiableSet());
            return codes.contains("ALL") ? Set.of() : codes;
        }
    }
}
