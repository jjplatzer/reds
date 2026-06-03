package ingest;

import com.solacesystems.jms.SolConnectionFactory;
import com.solacesystems.jms.SolJmsUtility;
import io.vertx.core.AbstractVerticle;
import io.vertx.core.Promise;
import org.w3c.dom.Document;
import org.w3c.dom.Element;
import org.w3c.dom.NodeList;

import javax.jms.*;
import javax.xml.parsers.DocumentBuilder;
import javax.xml.parsers.DocumentBuilderFactory;
import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

/**
 * FAA SWIM STDDS SMES ingest verticle.
 *
 * Responsibility: parse SMES XML into SurfaceTargets and publish them
 * to the EventBus. Nothing more — no state, no diffing, no delivery.
 *
 * Pipeline:
 *   SMES XML → parse → SurfaceTarget → EventBus({@value SurfaceTarget#ADDRESS})
 *
 * The Solace JMS consume loop is blocking and runs on a Vert.x worker thread.
 *
 * SMES topic structure: SMES/all/false/{type}/{airport}/{tracon}
 *   AT = All-Targets batch  (positionReport, full + partial)
 *   AD = ADS-B delta        (adsbReport)
 *   SE = Surface Event      (positionReport, partial)
 *   SH = SafetyLogicHoldBar (ignored)
 */
public final class SmesConsumer extends AbstractVerticle {

    private Thread consumerThread;

    @Override
    public void start(Promise<Void> startPromise) {
        // The JMS receive loop runs forever, so it doesn't belong on a Vert.x
        // worker thread — the block detector would flag it at 60 s. A dedicated
        // daemon thread is the idiomatic home for unbounded blocking I/O.
        consumerThread = new Thread(() -> run(startPromise), "smes-consumer");
        consumerThread.setDaemon(true);
        consumerThread.start();
    }

    @Override
    public void stop() {
        if (consumerThread != null) consumerThread.interrupt();
    }

    private void run(Promise<Void> startPromise) {
        Config cfg;
        try {
            cfg = Config.fromEnv();
        } catch (Exception e) {
            startPromise.fail(e);
            return;
        }

        DocumentBuilderFactory dbf = DocumentBuilderFactory.newInstance();
        tryFeature(dbf, "http://apache.org/xml/features/disallow-doctype-decl", true);
        tryFeature(dbf, "http://xml.org/sax/features/external-general-entities", false);
        tryFeature(dbf, "http://xml.org/sax/features/external-parameter-entities", false);
        dbf.setExpandEntityReferences(false);
        dbf.setNamespaceAware(true);

        DocumentBuilder db;
        try {
            db = dbf.newDocumentBuilder();
            db.setErrorHandler(null);
        } catch (Exception e) {
            startPromise.fail(e);
            return;
        }

        SolConnectionFactory cf;
        Connection conn = null;
        Session session = null;
        MessageConsumer consumer = null;
        try {
            cf = SolJmsUtility.createConnectionFactory();
            cf.setHost(cfg.jmsUrl);
            cf.setVPN(cfg.vpn);
            cf.setUsername(cfg.username);
            cf.setPassword(cfg.password);
            cf.setConnectRetries(5);
            cf.setConnectRetriesPerHost(3);

            conn = cf.createConnection();
            session = conn.createSession(false, Session.CLIENT_ACKNOWLEDGE);
            consumer = session.createConsumer(session.createQueue(cfg.queueName));
            conn.start();

            startPromise.complete();
            System.out.println("[SMES] Connected. Queue: " + cfg.queueName);

            long msgCount = 0, obsCount = 0;
            long statsLastMs = System.currentTimeMillis();
            long statsLastMsg = 0, statsLastObs = 0;

            while (!Thread.currentThread().isInterrupted()) {
                Message msg = consumer.receive(1000);
                if (msg == null) {
                    if (cfg.printStats && (System.currentTimeMillis() - statsLastMs) >= cfg.statsIntervalMs) {
                        System.out.println("[SMES] msgs=" + msgCount + " (+" + (msgCount - statsLastMsg) +
                                ") obs=" + obsCount + " (+" + (obsCount - statsLastObs) + ")");
                        statsLastMs = System.currentTimeMillis();
                        statsLastMsg = msgCount;
                        statsLastObs = obsCount;
                    }
                    continue;
                }

                if ("SH".equals(extractMsgType(topicOf(msg)))) {
                    msg.acknowledge();
                    msgCount++;
                    continue;
                }

                byte[] xmlBytes = payloadBytes(msg, cfg.maxBytes);
                if (xmlBytes == null || xmlBytes.length == 0) {
                    msg.acknowledge();
                    continue;
                }

                List<SurfaceTarget> targets;
                try {
                    targets = parseMessage(db.parse(new ByteArrayInputStream(xmlBytes)));
                } catch (Exception e) {
                    System.err.println("[SMES] Parse error: " + e.getMessage());
                    msg.acknowledge();
                    continue;
                }

                if (!targets.isEmpty()) {
                    vertx.eventBus().publish(SurfaceTarget.ADDRESS, new TargetBatch(targets));
                    obsCount += targets.size();
                }

                msg.acknowledge();
                msgCount++;
            }
        } catch (Exception e) {
            if (!startPromise.future().isComplete()) startPromise.fail(e);
            else System.err.println("[SMES] Fatal: " + e.getMessage());
        } finally {
            closeQuietly(consumer);
            closeQuietly(session);
            closeQuietly(conn);
        }
    }

    // -------------------------------------------------------------------------
    // XML parsing
    // -------------------------------------------------------------------------

    static List<SurfaceTarget> parseMessage(Document doc) {
        Element root = doc.getDocumentElement();
        if (root == null) return List.of();
        if ("SafetyLogicHoldBar".equals(localName(root))) return List.of();

        String airport = blankToNull(text(child(root, "airport")));
        List<SurfaceTarget> out = new ArrayList<>();

        String rootLocal = localName(root);
        if ("asdexMsg".equals(rootLocal) || "SurfaceMovementEventMessage".equalsIgnoreCase(rootLocal)) {
            NodeList children = root.getChildNodes();
            for (int i = 0; i < children.getLength(); i++) {
                if (!(children.item(i) instanceof Element el)) continue;
                switch (localName(el)) {
                    case "positionReport", "mlatReport", "mlatPlotReport", "adsbPlotReport" -> {
                        SurfaceTarget t = parsePositionReport(el, airport);
                        if (t != null) out.add(t);
                    }
                    case "adsbReport" -> {
                        SurfaceTarget t = parseAdsbReport(el, airport);
                        if (t != null) out.add(t);
                    }
                }
            }
        }

        return out;
    }

    private static SurfaceTarget parsePositionReport(Element el, String airport) {
        String track = blankToNull(text(child(el, "track")));
        if (track == null) return null;

        boolean isFull = "true".equalsIgnoreCase(el.getAttribute("full"));
        String stid    = blankToNull(text(child(el, "stid")));
        String positionReportTime = blankToNull(text(child(el, "time")));
        Element manual = child(el, "manual");
        String scratchpad1 = manual != null ? scratchpadToNull(text(child(manual, "scratchpad1"))) : null;
        String scratchpad2 = manual != null ? scratchpadToNull(text(child(manual, "scratchpad2"))) : null;

        // Flight ID
        String callsign = null, squawk = null;
        Element flightId = child(el, "flightId");
        if (flightId != null) {
            String acid = blankToNull(text(child(flightId, "aircraftId")));
            callsign = "UNKN".equalsIgnoreCase(acid) ? null : acid;
            if (callsign == null) callsign = blankToNull(text(child(flightId, "manualCallsign")));
            squawk = blankToNull(text(child(flightId, "mode3ACode")));
            if (squawk == null) squawk = blankToNull(text(child(flightId, "manualMode3ACode")));
        }

        // Flight info
        String tgtType = isFull ? "unknown" : null, acType = null, wake = null, exitFix = null;
        Element flightInfo = child(el, "flightInfo");
        if (flightInfo != null) {
            String raw = blankToNull(text(child(flightInfo, "tgtType")));
            if (raw != null || isFull) tgtType = normalizeTargetType(raw);
            acType = blankToNull(text(child(flightInfo, "acType")));
            if (acType == null) acType = blankToNull(text(child(flightInfo, "manualAircraftType")));
            wake    = blankToNull(text(child(flightInfo, "wake")));
            exitFix = blankToNull(text(child(flightInfo, "fix")));
            if (squawk == null) squawk = blankToNull(text(child(flightInfo, "beaconCode")));
        }

        // Position
        Double lat = null, lon = null, altitude = null;
        Element pos = child(el, "position");
        if (pos != null) {
            lat      = parseDouble(text(child(pos, "latitude")));
            lon      = parseDouble(text(child(pos, "longitude")));
            altitude = parseDouble(text(child(pos, "altitude")));
        }

        // Movement
        Double speed = null, heading = null;
        Element movement = child(el, "movement");
        if (movement != null) {
            speed   = parseDouble(text(child(movement, "speed")));
            heading = parseDouble(text(child(movement, "heading")));
        }

        return new SurfaceTarget(
                airport, track, stid, isFull,
                positionReportTime,
                tgtType, callsign, acType, squawk, exitFix, wake, scratchpad1, scratchpad2,
                lat, lon, altitude, speed, heading
        );
    }

    /** adsbReport: adsbReport/report/basicReport; uses "lat"/"lon" not "latitude"/"longitude". */
    private static SurfaceTarget parseAdsbReport(Element el, String airport) {
        Element basicReport = child(child(el, "report"), "basicReport");
        if (basicReport == null) return null;

        String track = blankToNull(text(child(basicReport, "track")));
        if (track == null) return null;

        boolean isFull = "true".equalsIgnoreCase(el.getAttribute("full"));
        String positionReportTime = blankToNull(text(child(basicReport, "time")));

        // Position
        Double lat = null, lon = null, altitude = null;
        Element pos = child(basicReport, "position");
        if (pos != null) {
            lat      = parseDouble(text(child(pos, "lat")));
            lon      = parseDouble(text(child(pos, "lon")));
            altitude = parseDouble(text(child(pos, "altitude")));
        }

        // Velocity
        Double speed = null, heading = null;
        Element velocity = child(basicReport, "velocity");
        if (velocity != null) {
            speed   = parseDouble(text(child(velocity, "speed")));
            heading = parseDouble(text(child(velocity, "heading")));
        }

        // adsbReports typically lack stid, flight info, and identity fields
        return new SurfaceTarget(
                airport, track, null, isFull,
                positionReportTime,
                isFull ? "unknown" : null, null, null, null, null, null, null, null,
                lat, lon, altitude, speed, heading
        );
    }

    // -------------------------------------------------------------------------
    // XML helpers
    // -------------------------------------------------------------------------

    private static Element child(Element parent, String localName) {
        if (parent == null) return null;
        NodeList nl = parent.getChildNodes();
        for (int i = 0; i < nl.getLength(); i++)
            if (nl.item(i) instanceof Element el && localName.equals(localName(el))) return el;
        return null;
    }

    private static String text(Element el) {
        if (el == null) return null;
        String t = el.getTextContent();
        return t == null ? null : t.strip();
    }

    private static String localName(Element el) {
        String ln = el.getLocalName();
        if (ln != null && !ln.isBlank()) return ln;
        String name = el.getNodeName();
        if (name == null) return "";
        int i = name.indexOf(':');
        return (i >= 0 && i + 1 < name.length()) ? name.substring(i + 1) : name;
    }

    // -------------------------------------------------------------------------
    // Type helpers
    // -------------------------------------------------------------------------

    private static String blankToNull(String s) { return (s == null || s.isBlank()) ? null : s; }

    private static String normalizeTargetType(String raw) {
        if (raw == null) return "unknown";
        if ("aircraft".equalsIgnoreCase(raw)) return "aircraft";
        if ("vehicle".equalsIgnoreCase(raw) || "VEH".equalsIgnoreCase(raw)) return "vehicle";
        return "unknown";
    }

    private static String scratchpadToNull(String s) {
        s = blankToNull(s);
        return s == null || "none".equalsIgnoreCase(s.strip()) ? null : s;
    }

    private static Double parseDouble(String s) {
        if (s == null || s.isBlank()) return null;
        try { return Double.parseDouble(s); } catch (NumberFormatException e) { return null; }
    }

    // -------------------------------------------------------------------------
    // JMS helpers
    // -------------------------------------------------------------------------

    private static String topicOf(Message msg) {
        try { Destination d = msg.getJMSDestination(); return d != null ? d.toString() : ""; }
        catch (Exception e) { return ""; }
    }

    private static String extractMsgType(String topic) {
        if (topic == null || topic.isBlank()) return "";
        String[] parts = topic.split("/");
        if (parts.length >= 4)
            return switch (parts[3].toUpperCase(Locale.ROOT)) {
                case "AT", "AD", "SE", "SH" -> parts[3].toUpperCase(Locale.ROOT);
                default -> "";
            };
        return "";
    }

    private static byte[] payloadBytes(Message msg, int maxBytes) throws JMSException {
        if (msg instanceof BytesMessage bm) {
            long len = bm.getBodyLength();
            if (len <= 0) return new byte[0];
            if (len > maxBytes) { System.err.println("[SMES] Oversized message: " + len); return new byte[0]; }
            byte[] out = new byte[(int) len];
            bm.readBytes(out);
            return out;
        }
        if (msg instanceof TextMessage tm) {
            String s = tm.getText();
            if (s == null) return new byte[0];
            byte[] out = s.getBytes(StandardCharsets.UTF_8);
            if (out.length > maxBytes) { System.err.println("[SMES] Oversized message: " + out.length); return new byte[0]; }
            return out;
        }
        return null;
    }

    private static void tryFeature(DocumentBuilderFactory dbf, String f, boolean v) {
        try { dbf.setFeature(f, v); } catch (Exception ignored) {}
    }

    private static void closeQuietly(MessageConsumer c) { if (c != null) try { c.close(); } catch (Exception ignored) {} }
    private static void closeQuietly(Session s)         { if (s != null) try { s.close(); } catch (Exception ignored) {} }
    private static void closeQuietly(Connection c)      { if (c != null) try { c.close(); } catch (Exception ignored) {} }

    // =========================================================================
    // Configuration
    // =========================================================================

    static final class Config {
        final String  jmsUrl, vpn, username, password, queueName;
        final int     maxBytes, statsIntervalMs;
        final boolean printStats;

        private Config(String jmsUrl, String vpn, String username, String password,
                       String queueName, int maxBytes, boolean printStats, int statsIntervalMs) {
            this.jmsUrl = jmsUrl; this.vpn = vpn; this.username = username; this.password = password;
            this.queueName = queueName; this.maxBytes = maxBytes;
            this.printStats = printStats; this.statsIntervalMs = statsIntervalMs;
        }

        static Config fromEnv() {
            String jmsUrl = must("SMES_JMS_URL");
            String vpn    = must("SMES_VPN");
            String user   = must("SCDS_USERNAME");
            String pass   = must("SCDS_PASSWORD");
            String queue  = must("SMES_QUEUE");
            return new Config(jmsUrl, vpn, user, pass, queue,
                    intEnv("SMES_MAX_BYTES", 10 * 1024 * 1024),
                    boolEnv("SMES_PRINT_STATS", false),
                    Math.max(250, intEnv("SMES_STATS_INTERVAL_MS", 2000)));
        }

        private static String env(String k) { return System.getenv(k); }
        private static String must(String k) {
            String v = env(k);
            if (v == null || v.isBlank()) throw new IllegalArgumentException("Missing env: " + k);
            return v;
        }
        private static int intEnv(String k, int def) {
            try { return Integer.parseInt(env(k).strip()); } catch (Exception e) { return def; }
        }
        private static boolean boolEnv(String k, boolean def) {
            String v = env(k); if (v == null || v.isBlank()) return def;
            v = v.strip().toLowerCase(Locale.ROOT);
            return v.equals("1") || v.equals("true") || v.equals("yes");
        }
    }
}
