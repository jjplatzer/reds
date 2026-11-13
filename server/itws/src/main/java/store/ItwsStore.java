package store;

import config.Env;
import ingest.ItwsParser;
import io.vertx.core.AbstractVerticle;
import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;

import java.time.Instant;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.TreeMap;
import java.util.stream.Collectors;

/** Event-loop-owned live grids and two saved weather pictures per radar source. */
public final class ItwsStore extends AbstractVerticle {
    public static final String EVENT_ADDRESS = "itws.store.event";
    public static final String SNAPSHOT_ADDRESS = "itws.store.snapshot";
    private final Map<String, Entry> entries = new TreeMap<>();
    private final int maxProducts;
    private final long maxHistoryBytes;
    private long revision, outOfOrder, duplicates, expiredMessages, expirations, evictions;
    private long expiryTimer = -1;

    public ItwsStore() {
        this(Env.integer("ITWS_MAX_PRODUCTS", 256, 1, 4096),
                Env.integer("ITWS_HISTORY_MAX_BYTES", 64 << 20, 0, 512 << 20));
    }

    public ItwsStore(int maxProducts) { this(maxProducts, 64L << 20); }

    public ItwsStore(int maxProducts, long maxHistoryBytes) {
        if (maxProducts < 1) throw new IllegalArgumentException("Invalid ITWS product limit");
        if (maxHistoryBytes < 0) throw new IllegalArgumentException("Invalid ITWS history limit");
        this.maxProducts = maxProducts;
        this.maxHistoryBytes = maxHistoryBytes;
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
                message.reply(snapshot(sites, now, query.getBoolean("includeHistory", false)));
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
            // History clients may still hold this source after live expiry.
            frames.add(remove(oldest, "capacity"));
            evictions++;
        }
        Entry current = new Entry(product, now, previous);
        entries.put(key, current);
        trimHistory();
        frames.add(envelope("update").put("revision", ++revision).put("product", current.product.copy()));
        return List.copyOf(frames);
    }

    public List<JsonObject> expire(Instant now) {
        List<JsonObject> frames = new ArrayList<>();
        for (Entry entry : entries.values()) {
            if (entry.product != null && !entry.expires.isAfter(now)) {
                frames.add(remove(entry, "expired"));
                // Live weather expires independently of the bounded archive.
                // The ordering watermark still prevents stale resurrection.
                entry.product = null;
                expirations++;
            }
        }
        trimHistory();
        return List.copyOf(frames);
    }

    public JsonObject snapshot(Set<String> sites, Instant now) {
        return snapshot(sites, now, false);
    }

    public JsonObject snapshot(Set<String> sites, Instant now, boolean includeHistory) {
        JsonArray products = new JsonArray();
        entries.values().stream().filter(entry -> entry.product != null && entry.expires.isAfter(now))
                .filter(entry -> sites.isEmpty() || sites.contains(entry.site))
                .forEach(entry -> products.add(entry.product.copy()));
        JsonObject frame = envelope("snapshot").put("revision", revision).put("time", now.toString()).put("products", products);
        if (includeHistory) {
            JsonArray history = new JsonArray();
            for (Entry entry : entries.values()) {
                if (!sites.isEmpty() && !sites.contains(entry.site)) continue;
                JsonArray pictures = new JsonArray();
                boolean retained = false;
                for (int i = 0; i < entry.pictures.length; i++) {
                    Picture picture = entry.pictures[i];
                    // Active current weather already appears in products. Keep
                    // slot numbers when a memory limit has evicted a picture.
                    boolean active = i == 0 && entry.product != null && entry.expires.isAfter(now);
                    JsonObject saved = picture == null || active ? null : picture.product.copy();
                    pictures.add(saved);
                    retained |= saved != null;
                }
                if (retained) history.add(new JsonObject().put("key", entry.key).put("products", pictures));
            }
            frame.put("history", history);
        }
        return frame;
    }

    public JsonObject stats() {
        long active = entries.values().stream().filter(entry -> entry.product != null).count();
        return new JsonObject().put("revision", revision).put("products", active).put("knownSources", entries.size())
                .put("outOfOrder", outOfOrder).put("duplicates", duplicates).put("expiredMessages", expiredMessages)
                .put("expirations", expirations).put("evictions", evictions)
                .put("historyBytes", historyBytes()).put("historyMaxBytes", maxHistoryBytes);
    }

    private long historyBytes() {
        long bytes = 0;
        for (Entry entry : entries.values()) {
            for (int i = 0; i < entry.pictures.length; i++) {
                if ((i != 0 || entry.product == null) && entry.pictures[i] != null)
                    bytes += entry.pictures[i].bytes;
            }
        }
        return bytes;
    }

    private void trimHistory() {
        long bytes = historyBytes();
        if (bytes <= maxHistoryBytes) return;
        record Saved(Entry entry, int index) {}
        List<Saved> saved = new ArrayList<>();
        for (Entry entry : entries.values()) {
            for (int i = 0; i < entry.pictures.length; i++) {
                if ((i != 0 || entry.product == null) && entry.pictures[i] != null) saved.add(new Saved(entry, i));
            }
        }
        saved.sort(Comparator.comparing(item -> item.entry.pictures[item.index].generated));
        for (Saved item : saved) {
            if (bytes <= maxHistoryBytes) break;
            bytes -= item.entry.pictures[item.index].bytes;
            item.entry.pictures[item.index] = null;
        }
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
        // Current, previous, oldest (TI 6191.409 section 6.2). Keep the latest
        // expired picture available to roll into history on the next update.
        final Picture[] pictures = new Picture[3];

        Entry(JsonObject product, Instant now, Entry previous) {
            this.product = product.copy();
            key = product.getString("key");
            site = product.getString("site");
            source = product.getString("source");
            productId = product.getInteger("productId");
            hash = product.getString("contentHash");
            generated = Instant.parse(product.getString("generationTime"));
            expires = Instant.parse(product.getString("expirationTime"));
            updated = now;
            pictures[0] = new Picture(this.product, generated);
            if (previous != null) {
                // A corrected picture with the same generation time replaces
                // that scan; it does not consume another history slot.
                int offset = generated.equals(previous.generated) ? 0 : 1;
                for (int i = 1; i < pictures.length; i++) pictures[i] = previous.pictures[i - offset];
            }
        }
    }

    private static final class Picture {
        final JsonObject product;
        final Instant generated;
        final int bytes;

        Picture(JsonObject product, Instant generated) {
            this.product = product;
            this.generated = generated;
            bytes = product.encode().getBytes(StandardCharsets.UTF_8).length;
        }
    }
}
