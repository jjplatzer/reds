package store;

import config.Env;
import ingest.ItwsParser;
import io.vertx.core.AbstractVerticle;
import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;

import java.time.Instant;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.TreeMap;
import java.util.stream.Collectors;

/** Event-loop-owned latest grids, including bounded ordering watermarks after expiry. */
public final class ItwsStore extends AbstractVerticle {
    public static final String EVENT_ADDRESS = "itws.store.event";
    public static final String SNAPSHOT_ADDRESS = "itws.store.snapshot";
    private final Map<String, Entry> entries = new TreeMap<>();
    private final int maxProducts;
    private long revision, outOfOrder, duplicates, expiredMessages, expirations, evictions;
    private long expiryTimer = -1;

    public ItwsStore() { this(Env.integer("ITWS_MAX_PRODUCTS", 256, 1, 4096)); }

    public ItwsStore(int maxProducts) {
        if (maxProducts < 1) throw new IllegalArgumentException("Invalid ITWS product limit");
        this.maxProducts = maxProducts;
    }

    @Override
    public void start() {
        vertx.eventBus().<JsonArray>localConsumer(ItwsParser.ADDRESS, message -> {
            Instant now = Instant.now();
            publish(expire(now));
            int accepted = 0;
            for (Object value : message.body()) {
                List<JsonObject> frames = apply((JsonObject) value, now);
                if (frames.stream().anyMatch(frame -> "update".equals(frame.getString("type")))) accepted++;
                publish(frames);
            }
            message.reply(new JsonObject().put("accepted", accepted));
        });
        vertx.eventBus().<JsonObject>localConsumer(SNAPSHOT_ADDRESS, message -> {
            Instant now = Instant.now();
            publish(expire(now));
            JsonObject query = message.body();
            if (query.getBoolean("statsOnly", false)) message.reply(stats());
            else {
                Set<String> sites = query.getJsonArray("sites", new JsonArray()).stream()
                        .map(Object::toString).collect(Collectors.toSet());
                message.reply(snapshot(sites, now));
            }
        });
        expiryTimer = vertx.setPeriodic(1_000, ignored -> publish(expire(Instant.now())));
    }

    @Override
    public void stop() { if (expiryTimer != -1) vertx.cancelTimer(expiryTimer); }

    private void publish(List<JsonObject> frames) {
        frames.forEach(frame -> vertx.eventBus().publish(EVENT_ADDRESS, frame));
    }

    // Public for offline verification; live callers are confined to this verticle's event loop.
    public List<JsonObject> apply(JsonObject product, Instant now) {
        String key = product.getString("key");
        Instant generated = Instant.parse(product.getString("generationTime"));
        Instant expires = Instant.parse(product.getString("expirationTime"));
        Entry previous = entries.get(key);
        if (previous != null && generated.isBefore(previous.generated)) {
            outOfOrder++;
            return List.of();
        }
        if (!expires.isAfter(now)) {
            expiredMessages++;
            return List.of();
        }
        if (previous != null && generated.equals(previous.generated) && product.getString("contentHash").equals(previous.hash)) {
            duplicates++;
            return List.of();
        }
        List<JsonObject> frames = new ArrayList<>();
        if (previous == null && entries.size() >= maxProducts) {
            // Prefer freeing an expired watermark over evicting a visible source.
            Entry oldest = entries.values().stream().min(Comparator
                    .comparing((Entry entry) -> entry.product != null).thenComparing(entry -> entry.updated)).orElseThrow();
            entries.remove(oldest.key);
            if (oldest.product != null) frames.add(remove(oldest, "capacity"));
            evictions++;
        }
        Entry current = new Entry(product, now);
        entries.put(key, current);
        frames.add(envelope("update").put("revision", ++revision).put("product", current.product.copy()));
        return List.copyOf(frames);
    }

    public List<JsonObject> expire(Instant now) {
        List<JsonObject> frames = new ArrayList<>();
        for (Entry entry : entries.values()) {
            if (entry.product != null && !entry.expires.isAfter(now)) {
                frames.add(remove(entry, "expired"));
                // Keep only the small source/time/hash watermark so an older refresh cannot revive it.
                entry.product = null;
                expirations++;
            }
        }
        return List.copyOf(frames);
    }

    public JsonObject snapshot(Set<String> sites, Instant now) {
        JsonArray products = new JsonArray();
        entries.values().stream().filter(entry -> entry.product != null && entry.expires.isAfter(now))
                .filter(entry -> sites.isEmpty() || sites.contains(entry.site))
                .forEach(entry -> products.add(entry.product.copy()));
        return envelope("snapshot").put("revision", revision).put("time", now.toString()).put("products", products);
    }

    public JsonObject stats() {
        long active = entries.values().stream().filter(entry -> entry.product != null).count();
        return new JsonObject().put("revision", revision).put("products", active).put("knownSources", entries.size())
                .put("outOfOrder", outOfOrder).put("duplicates", duplicates).put("expiredMessages", expiredMessages)
                .put("expirations", expirations).put("evictions", evictions);
    }

    private JsonObject remove(Entry entry, String reason) {
        return envelope("remove").put("revision", ++revision).put("key", entry.key).put("site", entry.site)
                .put("source", entry.source).put("productId", entry.productId).put("reason", reason);
    }

    private static JsonObject envelope(String type) {
        return new JsonObject().put("type", type).put("feed", "itws").put("protocol", 1);
    }

    private static final class Entry {
        final String key, site, source, hash;
        final int productId;
        final Instant generated, expires, updated;
        JsonObject product;

        Entry(JsonObject product, Instant now) {
            this.product = product.copy();
            key = product.getString("key");
            site = product.getString("site");
            source = product.getString("source");
            productId = product.getInteger("productId");
            hash = product.getString("contentHash");
            generated = Instant.parse(product.getString("generationTime"));
            expires = Instant.parse(product.getString("expirationTime"));
            updated = now;
        }
    }
}
