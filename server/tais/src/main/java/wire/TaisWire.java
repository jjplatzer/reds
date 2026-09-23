package wire;

import ingest.TaisObservation;
import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;
import store.HistoryPosition;
import store.TrackCache;

import java.time.Instant;
import java.util.List;

/**
 * Stable protocol-v1 JSON for the REDS TAIS WebSocket.
 *
 * The wire representation stays close to the normalized SimpleXML model and
 * deliberately performs no surveillance fusion or display-specific inference.
 */
public final class TaisWire {

    public static final int PROTOCOL = 1;
    public static final String FEED = "tais";

    private TaisWire() {}

    public static JsonObject snapshot(
            long revision,
            Instant generatedAt,
            List<TrackCache.SnapshotTarget> targets
    ) {
        JsonArray array = new JsonArray();
        for (TrackCache.SnapshotTarget target : targets) {
            array.add(target(target.target(), target.history(), true));
        }

        return envelope("snapshot", revision)
                .put("generatedAt", generatedAt.toString())
                .put("targets", array);
    }

    public static JsonObject update(long revision, TaisObservation observation) {
        return envelope("update", revision)
                .put("target", target(observation, List.of(), false));
    }

    public static JsonObject remove(long revision, String key) {
        return envelope("remove", revision)
                .put("key", key);
    }

    private static JsonObject envelope(String type, long revision) {
        return new JsonObject()
                .put("type", type)
                .put("protocol", PROTOCOL)
                .put("feed", FEED)
                .put("revision", revision);
    }

    private static JsonObject target(
            TaisObservation observation,
            List<HistoryPosition> history,
            boolean includeHistory
    ) {
        JsonObject out = new JsonObject()
                .put("key", observation.targetKey())
                .put("facility", observation.facility());

        putInstant(out, "receivedAt", observation.receivedAt());

        TaisObservation.RecordMeta meta = observation.recordMeta();
        if (meta != null) {
            JsonObject record = new JsonObject();
            put(record, "sequenceNumber", meta.sequenceNumber());
            put(record, "source", meta.source());
            put(record, "type", meta.type());
            put(record, "starsTimestamp", meta.starsTimestamp());
            put(record, "starsSourceId", meta.starsSourceId());
            put(record, "starsTimeSync", meta.starsTimeSync());
            putInstant(record, "safaReceiptTime", meta.safaReceiptTime());
            put(record, "safaTimeSync", meta.safaTimeSync());
            out.put("recordMeta", record);
        }

        TaisObservation.Track track = observation.track();
        JsonObject trackJson = new JsonObject();
        put(trackJson, "trackNum", track.trackNum());
        putInstant(trackJson, "mrtTime", track.mrtTime());
        put(trackJson, "status", track.status());
        put(trackJson, "acAddress", track.acAddress());
        put(trackJson, "xPos", track.xPos());
        put(trackJson, "yPos", track.yPos());
        put(trackJson, "lat", track.lat());
        put(trackJson, "lon", track.lon());
        put(trackJson, "verticalRate", track.verticalRate());
        put(trackJson, "vx", track.vx());
        put(trackJson, "vy", track.vy());
        put(trackJson, "verticalRateRaw", track.verticalRateRaw());
        put(trackJson, "vxRaw", track.vxRaw());
        put(trackJson, "vyRaw", track.vyRaw());
        put(trackJson, "frozen", track.frozen());
        put(trackJson, "newTrack", track.newTrack());
        put(trackJson, "pseudo", track.pseudo());
        put(trackJson, "adsb", track.adsb());
        put(trackJson, "reportedBeaconCode", track.reportedBeaconCode());
        put(trackJson, "reportedAltitude", track.reportedAltitude());
        out.put("track", trackJson);

        TaisObservation.FlightPlan fp = observation.flightPlan();
        if (fp != null) {
            JsonObject flightPlan = new JsonObject();
            put(flightPlan, "sfpn", fp.sfpn());
            put(flightPlan, "ocr", fp.ocr());
            put(flightPlan, "rnav", fp.rnav());
            put(flightPlan, "scratchPad1", fp.scratchPad1());
            put(flightPlan, "scratchPad2", fp.scratchPad2());
            put(flightPlan, "cps", fp.cps());
            put(flightPlan, "runway", fp.runway());
            put(flightPlan, "assignedBeaconCode", fp.assignedBeaconCode());
            put(flightPlan, "requestedAltitude", fp.requestedAltitude());
            put(flightPlan, "assignedAltitude", fp.assignedAltitude());
            put(flightPlan, "category", fp.category());
            put(flightPlan, "dbi", fp.dbi());
            put(flightPlan, "acid", fp.acid());
            put(flightPlan, "acType", fp.acType());
            put(flightPlan, "entryFix", fp.entryFix());
            put(flightPlan, "exitFix", fp.exitFix());
            put(flightPlan, "airport", fp.airport());
            put(flightPlan, "flightRules", fp.flightRules());
            put(flightPlan, "rawFlightRules", fp.rawFlightRules());
            put(flightPlan, "type", fp.type());
            put(flightPlan, "ptdTime", fp.ptdTime());
            put(flightPlan, "status", fp.status());
            put(flightPlan, "deleted", fp.deleted());
            put(flightPlan, "suspended", fp.suspended());
            put(flightPlan, "lld", fp.lld());
            put(flightPlan, "ecid", fp.ecid());
            put(flightPlan, "eqptSuffix", fp.eqptSuffix());
            out.put("flightPlan", flightPlan);
        }

        TaisObservation.EnhancedData enhanced = observation.enhancedData();
        if (enhanced != null) {
            JsonObject enhancedData = new JsonObject();
            put(enhancedData, "eramGufi", enhanced.eramGufi());
            put(enhancedData, "sfdpsGufi", enhanced.sfdpsGufi());
            put(enhancedData, "departureAirport", enhanced.departureAirport());
            put(enhancedData, "destinationAirport", enhanced.destinationAirport());
            out.put("enhancedData", enhancedData);
        }

        if (includeHistory) {
            JsonArray positions = new JsonArray();
            for (HistoryPosition position : history) {
                JsonObject point = new JsonObject();
                putInstant(point, "time", position.time());
                put(point, "lat", position.lat());
                put(point, "lon", position.lon());
                put(point, "reportedAltitude", position.reportedAltitude());
                put(point, "vx", position.vx());
                put(point, "vy", position.vy());
                put(point, "verticalRate", position.verticalRate());
                put(point, "adsb", position.adsb());
                positions.add(point);
            }
            out.put("history", positions);
        }

        return out;
    }

    private static void put(JsonObject out, String key, Object value) {
        if (value != null) out.put(key, value);
    }

    private static void putInstant(JsonObject out, String key, Instant value) {
        if (value != null) out.put(key, value.toString());
    }
}
