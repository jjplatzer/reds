package store;

import config.Env;
import ingest.TaisBatch;
import ingest.TaisObservation;
import io.vertx.core.AbstractVerticle;

/** Vert.x owner of the mutable TAIS track cache. */
public final class TrackStore extends AbstractVerticle {

    private TrackCache cache;

    @Override
    public void start() {
        int historyCapacity = Math.max(
                TrackCache.STARS_MAX_HISTORY_DOTS,
                intEnv("TAIS_HISTORY_RAW_CAPACITY", TrackCache.DEFAULT_RAW_HISTORY_CAPACITY)
        );
        int statsIntervalMs = Math.max(5_000, intEnv("TAIS_STORE_STATS_INTERVAL_MS", 30_000));
        cache = new TrackCache(historyCapacity);

        vertx.eventBus().<TaisBatch>consumer(TaisBatch.ADDRESS, message -> {
            for (TaisObservation obs : message.body().observations()) {
                cache.accept(obs);
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
                " resets=" + stats.historyResets());
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
