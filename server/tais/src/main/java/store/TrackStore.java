package store;

import config.Env;
import ingest.TaisBatch;
import ingest.TaisObservation;
import io.vertx.core.AbstractVerticle;
import wire.TaisWire;

import java.time.Instant;

/**
 * Vert.x owner of mutable TAIS target state.
 *
 * It is also the serialization boundary for the live WebSocket feed: consumers
 * receive immutable JsonObject frames, never references to this mutable cache.
 */
public final class TrackStore extends AbstractVerticle {

    public static final String EVENT_ADDRESS = "tais.store.event";
    public static final String SNAPSHOT_ADDRESS = "tais.store.snapshot";

    private TrackCache cache;
    private long revision;
    private long removals;

    @Override
    public void start() {
        int historyCapacity = Math.max(
                TrackCache.STARS_MAX_HISTORY_DOTS,
                intEnv("TAIS_HISTORY_RAW_CAPACITY", TrackCache.DEFAULT_RAW_HISTORY_CAPACITY)
        );
        int statsIntervalMs = Math.max(5_000, intEnv("TAIS_STORE_STATS_INTERVAL_MS", 30_000));
        cache = new TrackCache(historyCapacity);

        // WebSocketPush asks the store for a point-in-time snapshot on each
        // connection. The revision lets the connection discard buffered live
        // frames that are already represented by the snapshot.
        vertx.eventBus().localConsumer(SNAPSHOT_ADDRESS, message ->
                message.reply(TaisWire.snapshot(
                        revision,
                        Instant.now(),
                        cache.snapshot()
                ))
        );

        vertx.eventBus().<TaisBatch>consumer(TaisBatch.ADDRESS, message -> {
            for (TaisObservation obs : message.body().observations()) {
                String key = obs.targetKey();

                // TAIS distinguishes a dropped track from a coasting track.
                // Dropped tracks must disappear from both current state and all
                // future connection snapshots.
                if (isDrop(obs.track().status())) {
                    if (cache.remove(key)) {
                        removals++;
                        vertx.eventBus().publish(
                                EVENT_ADDRESS,
                                TaisWire.remove(++revision, key)
                        );
                    }
                    continue;
                }

                cache.accept(obs);

                // TrackCache intentionally ignores out-of-order reports. For
                // accepted reports (including same-MRT flight-plan changes),
                // current() is the exact observation reference just supplied.
                if (cache.current(key) == obs) {
                    vertx.eventBus().publish(
                            EVENT_ADDRESS,
                            TaisWire.update(++revision, obs)
                    );
                }
            }
        });

        vertx.setPeriodic(statsIntervalMs, ignored -> printStats());
        System.out.println("[TAIS store] Raw history capacity=" + historyCapacity + " positions/track");
    }

    private void printStats() {
        TrackCache.Stats stats = cache.stats();
        System.out.println("[TAIS store] tracks=" + stats.tracks() +
                " history=" + stats.cachedHistorySamples() +
                " positions=" + stats.acceptedPositionSamples() +
                " dup=" + stats.duplicatePositions() +
                " outOfOrder=" + stats.outOfOrderPositions() +
                " resets=" + stats.historyResets() +
                " removed=" + removals +
                " revision=" + revision);
    }

    private static boolean isDrop(String status) {
        return status != null && status.equalsIgnoreCase("drop");
    }

    private static int intEnv(String key, int def) {
        try {
            String value = Env.get(key);
            return value == null ? def : Integer.parseInt(value.strip());
        } catch (Exception ignored) {
            return def;
        }
    }
}
