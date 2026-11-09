package store;

import ingest.AptcParser;
import io.vertx.core.AbstractVerticle;
import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;

import java.time.Instant;
import java.util.Map;
import java.util.TreeMap;

/** Event-loop-owned latest airport reports. Sparse APTC reports have no track-style expiry. */
public final class AptcStore extends AbstractVerticle {
    public static final String EVENT_ADDRESS = "aptc.store.event";
    public static final String SNAPSHOT_ADDRESS = "aptc.store.snapshot";
    private final Map<String, JsonObject> airports = new TreeMap<>();
    private long revision, outOfOrder;

    @Override
    public void start() {
        vertx.eventBus().<JsonArray>localConsumer(AptcParser.ADDRESS, message -> {
            int accepted = 0;
            for (Object value : message.body()) {
                JsonObject frame = apply((JsonObject) value);
                if (frame != null) {
                    accepted++;
                    vertx.eventBus().publish(EVENT_ADDRESS, frame);
                }
            }
            message.reply(new JsonObject().put("accepted", accepted));
        });
        vertx.eventBus().localConsumer(SNAPSHOT_ADDRESS, message -> message.reply(snapshot()));
    }

    // Exposed for local replay/verification; live calls run only on this verticle's event loop.
    public JsonObject apply(JsonObject configuration) {
        String airport = configuration.getString("airport");
        JsonObject previous = airports.get(airport);
        if (previous != null && older(configuration, previous)) {
            outOfOrder++;
            return null;
        }
        airports.put(airport, configuration.copy());
        return new JsonObject().put("type", "update").put("feed", "aptc").put("protocol", 1)
                .put("revision", ++revision).put("configuration", configuration.copy());
    }

    public JsonObject snapshot() {
        JsonArray configurations = new JsonArray();
        airports.values().forEach(configuration -> configurations.add(configuration.copy()));
        return new JsonObject().put("type", "snapshot").put("feed", "aptc").put("protocol", 1)
                .put("revision", revision).put("time", Instant.now().toString())
                .put("outOfOrder", outOfOrder).put("configurations", configurations);
    }

    private static boolean older(JsonObject incoming, JsonObject current) {
        int comparison = reportTime(incoming).compareTo(reportTime(current));
        if (comparison != 0) return comparison < 0;
        // sourceTimeStamp changes on refresh; it cannot override an older body updateTime.
        for (String field : new String[]{"entryTime", "sourceTimeStamp"}) {
            String next = incoming.getString(field), previous = current.getString(field);
            if (next != null && previous != null) {
                comparison = Instant.parse(next).compareTo(Instant.parse(previous));
                if (comparison != 0) return comparison < 0;
            }
        }
        return false;
    }

    private static Instant reportTime(JsonObject configuration) {
        for (String field : new String[]{"updateTime", "entryTime", "eventTime", "sourceTimeStamp", "receivedAt"}) {
            String value = configuration.getString(field);
            if (value != null) return Instant.parse(value);
        }
        return Instant.EPOCH;
    }
}
