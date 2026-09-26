package ingest;

import com.solacesystems.jms.SolConnectionFactory;
import com.solacesystems.jms.SolJmsUtility;
import config.Env;
import io.vertx.core.AbstractVerticle;
import io.vertx.core.Promise;
import org.w3c.dom.Document;

import javax.jms.BytesMessage;
import javax.jms.Connection;
import javax.jms.Message;
import javax.jms.MessageConsumer;
import javax.jms.Session;
import javax.jms.TextMessage;
import javax.xml.parsers.DocumentBuilder;
import javax.xml.parsers.DocumentBuilderFactory;
import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Collections;
import java.util.HashSet;
import java.util.Locale;
import java.util.Set;

/**
 * FAA SWIM STDDS TAIS JMS consumer.
 *
 * Responsibility is intentionally narrow: receive SimpleXML, normalize each
 * TATrackAndFlightPlan/record, and publish a TaisBatch. State, history and
 * future ADS-B fusion live downstream.
 *
 * IMPORTANT: TAIS_FACILITIES is only a defensive local allow-list. To retain
 * the low-latency cadence measured with N90, the SWIFT subscription itself must
 * also be filtered server-side to the same facility/facilities.
 */
public final class TaisConsumer extends AbstractVerticle {

    private Thread consumerThread;

    @Override
    public void start(Promise<Void> startPromise) {
        final Config cfg;
        final DocumentBuilder parser;

        try {
            cfg = Config.fromEnv();
            parser = newDocumentBuilder();
        } catch (Exception e) {
            startPromise.fail(e);
            return;
        }

        consumerThread = new Thread(() -> runReconnectLoop(cfg, parser), "tais-consumer");
        consumerThread.setDaemon(true);
        consumerThread.setUncaughtExceptionHandler((thread, err) ->
                System.err.println("[TAIS] Uncaught consumer error: " + err.getMessage()));
        consumerThread.start();

        startPromise.complete();
    }

    @Override
    public void stop() {
        if (consumerThread == null) return;

        consumerThread.interrupt();
        try {
            consumerThread.join(3000);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    private void runReconnectLoop(Config cfg, DocumentBuilder parser) {
        int delayMs = cfg.reconnectInitialMs;
        long attempt = 0;

        while (!Thread.currentThread().isInterrupted()) {
            attempt++;
            try {
                System.out.println("[TAIS] Connecting. queue=" + cfg.queueName + " attempt=" + attempt);
                consumeOnce(cfg, parser);
                delayMs = cfg.reconnectInitialMs;

                if (!Thread.currentThread().isInterrupted()) {
                    System.err.println("[TAIS] Consumer stopped unexpectedly; reconnecting");
                }
            } catch (Exception e) {
                if (Thread.currentThread().isInterrupted()) break;
                System.err.println("[TAIS] Disconnected: " + e.getMessage());
            }

            if (Thread.currentThread().isInterrupted()) break;
            sleepBeforeReconnect(delayMs);
            delayMs = Math.min(cfg.reconnectMaxMs, Math.max(cfg.reconnectInitialMs, delayMs * 2));
        }

        System.out.println("[TAIS] Consumer thread stopped");
    }

    private void consumeOnce(Config cfg, DocumentBuilder parser) throws Exception {
        Connection connection = null;
        Session session = null;
        MessageConsumer consumer = null;

        try {
            SolConnectionFactory factory = SolJmsUtility.createConnectionFactory();
            factory.setHost(cfg.jmsUrl);
            factory.setVPN(cfg.vpn);
            factory.setUsername(cfg.username);
            factory.setPassword(cfg.password);
            factory.setConnectRetries(5);
            factory.setConnectRetriesPerHost(3);
            factory.setReconnectRetries(10);

            connection = factory.createConnection();
            session = connection.createSession(false, Session.CLIENT_ACKNOWLEDGE);
            consumer = session.createConsumer(session.createQueue(cfg.queueName));
            connection.start();

            System.out.println("[TAIS] Connected. facilities=" +
                    (cfg.facilities.isEmpty() ? "ALL (local allow-list disabled)" : cfg.facilities));

            long messages = 0;
            long records = 0;
            long accepted = 0;
            long filtered = 0;
            long parseErrors = 0;
            long statsLastMs = System.currentTimeMillis();
            long lastMessages = 0, lastRecords = 0, lastAccepted = 0;

            while (!Thread.currentThread().isInterrupted()) {
                Message message = consumer.receive(1000);
                long nowMs = System.currentTimeMillis();

                if (message != null) {
                    messages++;
                    byte[] xml = payloadBytes(message, cfg.maxBytes);
                    if (xml == null || xml.length == 0) {
                        message.acknowledge();
                        continue;
                    }

                    try {
                        Instant receivedAt = Instant.now();
                        Document doc = parser.parse(new ByteArrayInputStream(xml));
                        TaisBatch batch = TaisParser.parse(doc, receivedAt);
                        records += batch.observations().size();

                        if (!batch.observations().isEmpty()) {
                            if (cfg.accepts(batch.facility())) {
                                vertx.eventBus().publish(TaisBatch.ADDRESS, batch);
                                accepted += batch.observations().size();
                            } else {
                                filtered += batch.observations().size();
                            }
                        }
                    } catch (Exception e) {
                        parseErrors++;
                        System.err.println("[TAIS] Parse error: " + e.getMessage());
                    }

                    // Poison XML should not cause an infinite redelivery loop.
                    message.acknowledge();
                }

                if (cfg.printStats && nowMs - statsLastMs >= cfg.statsIntervalMs) {
                    System.out.println("[TAIS] msgs=" + messages + " (+" + (messages - lastMessages) + ")" +
                            " records=" + records + " (+" + (records - lastRecords) + ")" +
                            " accepted=" + accepted + " (+" + (accepted - lastAccepted) + ")" +
                            " filtered=" + filtered + " parseErrors=" + parseErrors);
                    statsLastMs = nowMs;
                    lastMessages = messages;
                    lastRecords = records;
                    lastAccepted = accepted;
                }
            }
        } finally {
            closeQuietly(consumer);
            closeQuietly(session);
            closeQuietly(connection);
        }
    }

    private static DocumentBuilder newDocumentBuilder() throws Exception {
        DocumentBuilderFactory dbf = DocumentBuilderFactory.newInstance();
        dbf.setNamespaceAware(true);
        dbf.setExpandEntityReferences(false);
        tryFeature(dbf, "http://apache.org/xml/features/disallow-doctype-decl", true);
        tryFeature(dbf, "http://xml.org/sax/features/external-general-entities", false);
        tryFeature(dbf, "http://xml.org/sax/features/external-parameter-entities", false);
        return dbf.newDocumentBuilder();
    }

    private static byte[] payloadBytes(Message message, int maxBytes) throws Exception {
        if (message instanceof BytesMessage bytes) {
            long len = bytes.getBodyLength();
            if (len <= 0) return new byte[0];
            if (len > maxBytes) throw new IllegalArgumentException("oversized TAIS message: " + len + " bytes");
            byte[] out = new byte[(int) len];
            int read = bytes.readBytes(out);
            if (read < 0) return new byte[0];
            return out;
        }

        if (message instanceof TextMessage text) {
            String value = text.getText();
            if (value == null) return new byte[0];
            byte[] out = value.getBytes(StandardCharsets.UTF_8);
            if (out.length > maxBytes) throw new IllegalArgumentException("oversized TAIS message: " + out.length + " bytes");
            return out;
        }

        return null;
    }

    private static void tryFeature(DocumentBuilderFactory dbf, String feature, boolean value) {
        try {
            dbf.setFeature(feature, value);
        } catch (Exception ignored) {
            // Parser implementation may not expose every hardening feature.
        }
    }

    private static void sleepBeforeReconnect(int delayMs) {
        try {
            System.err.println("[TAIS] Reconnecting in " + delayMs + "ms");
            Thread.sleep(delayMs);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    private static void closeQuietly(MessageConsumer consumer) {
        if (consumer != null) try { consumer.close(); } catch (Exception ignored) {}
    }

    private static void closeQuietly(Session session) {
        if (session != null) try { session.close(); } catch (Exception ignored) {}
    }

    private static void closeQuietly(Connection connection) {
        if (connection != null) try { connection.close(); } catch (Exception ignored) {}
    }

    private static final class Config {
        final String jmsUrl, vpn, username, password, queueName;
        final Set<String> facilities;
        final int maxBytes, statsIntervalMs, reconnectInitialMs, reconnectMaxMs;
        final boolean printStats;

        private Config(String jmsUrl, String vpn, String username, String password, String queueName,
                       Set<String> facilities, int maxBytes, boolean printStats, int statsIntervalMs,
                       int reconnectInitialMs, int reconnectMaxMs) {
            this.jmsUrl = jmsUrl;
            this.vpn = vpn;
            this.username = username;
            this.password = password;
            this.queueName = queueName;
            this.facilities = facilities;
            this.maxBytes = maxBytes;
            this.printStats = printStats;
            this.statsIntervalMs = statsIntervalMs;
            this.reconnectInitialMs = reconnectInitialMs;
            this.reconnectMaxMs = reconnectMaxMs;
        }

        static Config fromEnv() {
            return new Config(
                    must("TAIS_JMS_URL"),
                    must("TAIS_VPN"),
                    must("SCDS_USERNAME"),
                    must("SCDS_PASSWORD"),
                    must("TAIS_QUEUE"),
                    parseFacilities(Env.get("TAIS_FACILITIES")),
                    Math.max(1024, intEnv("TAIS_MAX_BYTES", 10 * 1024 * 1024)),
                    boolEnv("TAIS_PRINT_STATS", true),
                    Math.max(1000, intEnv("TAIS_STATS_INTERVAL_MS", 30_000)),
                    Math.max(1000, intEnv("TAIS_RECONNECT_INITIAL_MS", 5_000)),
                    Math.max(1000, intEnv("TAIS_RECONNECT_MAX_MS", 60_000))
            );
        }

        boolean accepts(String facility) {
            return facilities.isEmpty() || facilities.contains(facility);
        }

        private static Set<String> parseFacilities(String value) {
            if (value == null || value.isBlank()) return Set.of();
            Set<String> out = new HashSet<>();
            for (String part : value.split(",")) {
                String facility = part.strip().toUpperCase(Locale.ROOT);
                if (!facility.isEmpty()) out.add(facility);
            }
            return Collections.unmodifiableSet(out);
        }

        private static String must(String key) {
            String value = Env.get(key);
            if (value == null || value.isBlank()) throw new IllegalArgumentException("Missing env: " + key);
            return value.strip();
        }

        private static int intEnv(String key, int def) {
            try {
                String value = Env.get(key);
                return value == null ? def : Integer.parseInt(value.strip());
            } catch (Exception ignored) {
                return def;
            }
        }

        private static boolean boolEnv(String key, boolean def) {
            String value = Env.get(key);
            if (value == null || value.isBlank()) return def;
            return switch (value.strip().toLowerCase(Locale.ROOT)) {
                case "1", "true", "yes", "on" -> true;
                case "0", "false", "no", "off" -> false;
                default -> def;
            };
        }
    }
}
