package ingest;

import com.solacesystems.jms.SolConnectionFactory;
import com.solacesystems.jms.SolJmsUtility;
import config.Env;
import io.vertx.core.AbstractVerticle;
import io.vertx.core.Promise;
import messages.JmsMetadata;
import messages.SfdpsBatch;
import messages.SfdpsChannel;
import messages.SfdpsEvent;
import org.w3c.dom.Document;

import javax.jms.BytesMessage;
import javax.jms.Connection;
import javax.jms.Message;
import javax.jms.MessageConsumer;
import javax.jms.Session;
import javax.jms.TextMessage;
import javax.xml.parsers.DocumentBuilder;
import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Set;

/**
 * Four independent SFDPS Solace consumers feeding one normalized REDS pipeline.
 *
 * Each SWIFT subscription has its own queue and its own reconnect loop. A
 * failure in Airspace, General or Status therefore cannot stall Flight FIXM.
 */
public final class SfdpsConsumer extends AbstractVerticle {

    private final List<Thread> workers = new ArrayList<>();

    @Override
    public void start(Promise<Void> startPromise) {
        final Config config;
        try {
            config = Config.fromEnv();
            for (Subscription subscription : config.subscriptions) {
                DocumentBuilder parser = XmlSupport.newDocumentBuilder();
                Thread thread = new Thread(
                        () -> runReconnectLoop(config, subscription, parser),
                        "sfdps-" + subscription.channel.wireName() + "-consumer"
                );
                thread.setDaemon(true);
                thread.setUncaughtExceptionHandler((ignored, err) ->
                        System.err.println("[SFDPS " + subscription.channel.wireName() + "] Uncaught consumer error: " + err.getMessage()));
                workers.add(thread);
                thread.start();
            }
        } catch (Exception e) {
            startPromise.fail(e);
            return;
        }
        startPromise.complete();
    }

    @Override
    public void stop() {
        for (Thread worker : workers) worker.interrupt();
        for (Thread worker : workers) {
            try { worker.join(3000); }
            catch (InterruptedException e) { Thread.currentThread().interrupt(); break; }
        }
        workers.clear();
    }

    private void runReconnectLoop(Config config, Subscription sub, DocumentBuilder parser) {
        int delayMs = config.reconnectInitialMs;
        long attempt = 0;
        while (!Thread.currentThread().isInterrupted()) {
            attempt++;
            try {
                System.out.println(prefix(sub) + " Connecting. queue=" + sub.queueName + " attempt=" + attempt);
                consumeOnce(config, sub, parser);
                delayMs = config.reconnectInitialMs;
                if (!Thread.currentThread().isInterrupted()) {
                    System.err.println(prefix(sub) + " Consumer stopped unexpectedly; reconnecting");
                }
            } catch (Exception e) {
                if (Thread.currentThread().isInterrupted()) break;
                System.err.println(prefix(sub) + " Disconnected: " + e.getMessage());
            }
            if (Thread.currentThread().isInterrupted()) break;
            sleep(delayMs, sub);
            delayMs = Math.min(config.reconnectMaxMs, Math.max(config.reconnectInitialMs, delayMs * 2));
        }
        System.out.println(prefix(sub) + " Consumer thread stopped");
    }

    private void consumeOnce(Config config, Subscription sub, DocumentBuilder parser) throws Exception {
        Connection connection = null;
        Session session = null;
        MessageConsumer consumer = null;
        try {
            SolConnectionFactory factory = SolJmsUtility.createConnectionFactory();
            factory.setHost(sub.jmsUrl);
            factory.setVPN(sub.vpn);
            factory.setUsername(config.username);
            factory.setPassword(config.password);
            factory.setConnectRetries(5);
            factory.setConnectRetriesPerHost(3);
            factory.setReconnectRetries(10);

            connection = factory.createConnection();
            session = connection.createSession(false, Session.CLIENT_ACKNOWLEDGE);
            consumer = session.createConsumer(session.createQueue(sub.queueName));
            connection.start();

            System.out.println(prefix(sub) + " Connected. facilities=" +
                    (config.facilities.isEmpty() ? "ALL (local allow-list disabled)" : config.facilities));

            long deliveries = 0, logical = 0, accepted = 0, filtered = 0, parseErrors = 0;
            long statsLastMs = System.currentTimeMillis();
            long lastDeliveries = 0, lastLogical = 0, lastAccepted = 0;

            while (!Thread.currentThread().isInterrupted()) {
                Message message = consumer.receive(1000);
                long nowMs = System.currentTimeMillis();
                if (message != null) {
                    deliveries++;
                    try {
                        byte[] xml = payloadBytes(message, config.maxBytes);
                        if (xml != null && xml.length > 0) {
                            JmsMetadata metadata = metadata(message);
                            if (!sub.channel.matchesDataType(metadata.dataType())) {
                                System.err.println(prefix(sub) + " Unexpected FDPS_DataType=" + metadata.dataType() +
                                        " (expected " + sub.channel.dataType() + ")");
                            }

                            Instant receivedAt = Instant.now();
                            Document doc = parser.parse(new ByteArrayInputStream(xml));
                            List<SfdpsEvent> parsed = SfdpsParser.parse(sub.channel, doc, metadata, receivedAt);
                            logical += parsed.size();
                            if (parsed.isEmpty()) {
                                parseErrors++;
                                System.err.println(prefix(sub) + " Parsed XML but produced no logical events; root=" +
                                        XmlSupport.localName(doc.getDocumentElement()) +
                                        " messageType=" + metadata.messageType());
                            }

                            List<SfdpsEvent> kept = new ArrayList<>(parsed.size());
                            for (SfdpsEvent event : parsed) {
                                if (config.accepts(event.facility())) {
                                    kept.add(event);
                                    accepted++;
                                } else {
                                    filtered++;
                                }
                            }
                            if (!kept.isEmpty()) {
                                vertx.eventBus().publish(SfdpsBatch.ADDRESS, new SfdpsBatch(sub.channel, kept));
                            }
                        }
                    } catch (Exception e) {
                        parseErrors++;
                        System.err.println(prefix(sub) + " Parse error: " + e.getMessage());
                    }
                    // Poison XML must not create an infinite Solace redelivery loop.
                    message.acknowledge();
                }

                if (config.printStats && nowMs - statsLastMs >= config.statsIntervalMs) {
                    System.out.println(prefix(sub) + " deliveries=" + deliveries + " (+" + (deliveries - lastDeliveries) + ")" +
                            " logical=" + logical + " (+" + (logical - lastLogical) + ")" +
                            " accepted=" + accepted + " (+" + (accepted - lastAccepted) + ")" +
                            " filtered=" + filtered + " parseErrors=" + parseErrors);
                    statsLastMs = nowMs;
                    lastDeliveries = deliveries;
                    lastLogical = logical;
                    lastAccepted = accepted;
                }
            }
        } finally {
            closeQuietly(consumer);
            closeQuietly(session);
            closeQuietly(connection);
        }
    }

    private static JmsMetadata metadata(Message message) {
        return new JmsMetadata(
                stringProperty(message, "FDPS_SourceSystem"),
                upper(stringProperty(message, "FDPS_SourceFacility")),
                stringProperty(message, "FDPS_MessageType"),
                stringProperty(message, "FDPS_FlightOperator"),
                stringProperty(message, "FDPS_FlightId"),
                stringProperty(message, "FDPS_Origin"),
                stringProperty(message, "FDPS_DestID"),
                booleanProperty(message, "FDPS_OneMinFreq"),
                stringProperty(message, "FDPS_Sensitive"),
                stringProperty(message, "FDPS_Restricted"),
                booleanProperty(message, "FDPS_Authoritative"),
                instantProperty(message, "FDPS_RcvdTime", "FDPS_RcvdTimeEpoch"),
                instantProperty(message, "FDPS_SentTime", "FDPS_SentTimeEpoch"),
                longProperty(message, "FDPS_SequenceNo"),
                stringProperty(message, "FDPS_DataType")
        );
    }

    private static String stringProperty(Message message, String key) {
        try {
            Object value = message.getObjectProperty(key);
            return value == null ? null : XmlSupport.blankToNull(String.valueOf(value));
        } catch (Exception ignored) { return null; }
    }

    private static Boolean booleanProperty(Message message, String key) {
        String value = stringProperty(message, key);
        return XmlSupport.parseBoolean(value);
    }

    private static Long longProperty(Message message, String key) {
        return XmlSupport.parseLong(stringProperty(message, key));
    }

    private static Instant instantProperty(Message message, String textKey, String epochKey) {
        Instant parsed = XmlSupport.parseInstant(stringProperty(message, textKey));
        if (parsed != null) return parsed;
        Long epoch = longProperty(message, epochKey);
        return epoch == null ? null : Instant.ofEpochSecond(epoch);
    }

    private static byte[] payloadBytes(Message message, int maxBytes) throws Exception {
        if (message instanceof BytesMessage bytes) {
            long len = bytes.getBodyLength();
            if (len <= 0) return new byte[0];
            if (len > maxBytes) throw new IllegalArgumentException("oversized SFDPS message: " + len + " bytes");
            byte[] out = new byte[(int) len];
            byte[] chunk = new byte[Math.min(8192, out.length)];
            int offset = 0;
            while (offset < out.length) {
                int read = bytes.readBytes(chunk, Math.min(chunk.length, out.length - offset));
                if (read < 0) break;
                System.arraycopy(chunk, 0, out, offset, read);
                offset += read;
                if (read == 0) break;
            }
            if (offset == out.length) return out;
            return java.util.Arrays.copyOf(out, offset);
        }
        if (message instanceof TextMessage text) {
            String value = text.getText();
            if (value == null) return new byte[0];
            byte[] out = value.getBytes(StandardCharsets.UTF_8);
            if (out.length > maxBytes) throw new IllegalArgumentException("oversized SFDPS message: " + out.length + " bytes");
            return out;
        }
        return null;
    }

    private static void closeQuietly(MessageConsumer consumer) { if (consumer != null) try { consumer.close(); } catch (Exception ignored) {} }
    private static void closeQuietly(Session session) { if (session != null) try { session.close(); } catch (Exception ignored) {} }
    private static void closeQuietly(Connection connection) { if (connection != null) try { connection.close(); } catch (Exception ignored) {} }

    private static void sleep(int delayMs, Subscription sub) {
        try {
            System.err.println(prefix(sub) + " Reconnecting in " + delayMs + "ms");
            Thread.sleep(delayMs);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    private static String prefix(Subscription sub) { return "[SFDPS " + sub.channel.wireName() + "]"; }
    private static String upper(String value) { return value == null ? null : value.toUpperCase(Locale.ROOT); }

    private record Subscription(SfdpsChannel channel, String jmsUrl, String vpn, String queueName) {}

    private static final class Config {
        final String username, password;
        final List<Subscription> subscriptions;
        final Set<String> facilities;
        final int maxBytes, statsIntervalMs, reconnectInitialMs, reconnectMaxMs;
        final boolean printStats;

        private Config(String username, String password, List<Subscription> subscriptions,
                       Set<String> facilities, int maxBytes, boolean printStats,
                       int statsIntervalMs, int reconnectInitialMs, int reconnectMaxMs) {
            this.username = username;
            this.password = password;
            this.subscriptions = List.copyOf(subscriptions);
            this.facilities = facilities;
            this.maxBytes = maxBytes;
            this.printStats = printStats;
            this.statsIntervalMs = statsIntervalMs;
            this.reconnectInitialMs = reconnectInitialMs;
            this.reconnectMaxMs = reconnectMaxMs;
        }

        static Config fromEnv() {
            String defaultUrl = trim(Env.get("SFDPS_JMS_URL"));
            String defaultVpn = trim(Env.get("SFDPS_VPN"));
            List<Subscription> subscriptions = List.of(
                    subscription(SfdpsChannel.FLIGHT_FIXM, "FLIGHT", defaultUrl, defaultVpn),
                    subscription(SfdpsChannel.AIRSPACE_AIXM, "AIRSPACE", defaultUrl, defaultVpn),
                    subscription(SfdpsChannel.GENERAL, "GENERAL", defaultUrl, defaultVpn),
                    subscription(SfdpsChannel.STATUS, "STATUS", defaultUrl, defaultVpn)
            );
            return new Config(
                    must("SCDS_USERNAME"),
                    must("SCDS_PASSWORD"),
                    subscriptions,
                    parseFacilities(Env.get("SFDPS_FACILITIES")),
                    Math.max(1024, intEnv("SFDPS_MAX_BYTES", 32 * 1024 * 1024)),
                    boolEnv("SFDPS_PRINT_STATS", true),
                    Math.max(1000, intEnv("SFDPS_STATS_INTERVAL_MS", 30_000)),
                    Math.max(1000, intEnv("SFDPS_RECONNECT_INITIAL_MS", 5_000)),
                    Math.max(1000, intEnv("SFDPS_RECONNECT_MAX_MS", 60_000))
            );
        }

        boolean accepts(String facility) {
            if (facilities.isEmpty()) return true;
            if (facility == null || facility.isBlank()) return false;
            String normalized = facility.toUpperCase(Locale.ROOT).strip();
            if ("ALL".equals(normalized) || "NA".equals(normalized) || facilities.contains(normalized)) return true;
            for (String part : normalized.split("[\\s,;/]+")) {
                if (facilities.contains(part)) return true;
            }
            return false;
        }

        private static Subscription subscription(SfdpsChannel channel, String suffix, String defaultUrl, String defaultVpn) {
            String url = firstNonBlank(Env.get("SFDPS_" + suffix + "_JMS_URL"), defaultUrl);
            String vpn = firstNonBlank(Env.get("SFDPS_" + suffix + "_VPN"), defaultVpn);
            String queue = must("SFDPS_" + suffix + "_QUEUE");
            if (url == null) throw new IllegalArgumentException("Missing env: SFDPS_" + suffix + "_JMS_URL or SFDPS_JMS_URL");
            if (vpn == null) throw new IllegalArgumentException("Missing env: SFDPS_" + suffix + "_VPN or SFDPS_VPN");
            return new Subscription(channel, url, vpn, queue);
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
            String value = trim(Env.get(key));
            if (value == null) throw new IllegalArgumentException("Missing env: " + key);
            return value;
        }
        private static String firstNonBlank(String a, String b) { return trim(a) != null ? trim(a) : trim(b); }
        private static String trim(String value) { return value == null || value.isBlank() ? null : value.strip(); }
        private static int intEnv(String key, int def) {
            try { String value = Env.get(key); return value == null ? def : Integer.parseInt(value.strip()); }
            catch (Exception ignored) { return def; }
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
