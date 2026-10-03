package store;

import config.Env;
import ingest.TaisBatch;
import ingest.TaisObservation;
import io.vertx.core.AbstractVerticle;
import wire.TaisWire;

import java.time.Duration;
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

    private static final int DEFAULT_TOTAL_COAST_TIME_MS = 30_000;
    private static final int DEFAULT_COAST_SWEEP_INTERVAL_MS = 1_000;

    private TrackCache cache;
    private Duration totalCoastTime;
    private long revision;
    private long removals;
    private long coastPhase2Transitions;

    @Override
    public void start() {
        int historyCapacity = Math.max(
                TrackCache.STARS_MAX_HISTORY_DOTS,
                intEnv("TAIS_HISTORY_RAW_CAPACITY", TrackCache.DEFAULT_RAW_HISTORY_CAPACITY)
        );
        int statsIntervalMs = Math.max(5_000, intEnv("TAIS_STORE_STATS_INTERVAL_MS", 30_000));
        int totalCoastTimeMs = Math.max(1_000, intEnv(
                "TAIS_COAST_TIMEOUT_MS",
                DEFAULT_TOTAL_COAST_TIME_MS
        ));
        int coastSweepIntervalMs = Math.max(250, intEnv(
                "TAIS_COAST_SWEEP_INTERVAL_MS",
                DEFAULT_COAST_SWEEP_INTERVAL_MS
        ));

        cache = new TrackCache(historyCapacity);
        totalCoastTime = Duration.ofMillis(totalCoastTimeMs);

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
                // A source-declared drop is the terminal lifecycle event. Coast
                // Phase 2 is retained so the client can move the flight from the
                // radar window into the Coast/Suspend List.
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
                // Serialize the cache snapshot so coast-phase state accompanies
                // ordinary TAIS updates without inventing a second feed.
                if (cache.current(key) == obs) {
                    TrackCache.SnapshotTarget target = cache.snapshotTarget(key);
                    if (target != null) {
                        vertx.eventBus().publish(
                                EVENT_ADDRESS,
                                TaisWire.update(++revision, target)
                        );
                    }
                }
            }
        });

        vertx.setPeriodic(coastSweepIntervalMs, ignored -> advanceCoastPhases());
        vertx.setPeriodic(statsIntervalMs, ignored -> printStats());
        System.out.println("[TAIS store] Raw history capacity=" + historyCapacity + " positions/track" +
                " totalCoastTime=" + totalCoastTimeMs + "ms" +
                " coastSweep=" + coastSweepIntervalMs + "ms");
    }

    private void advanceCoastPhases() {
        for (TrackCache.SnapshotTarget target : cache.advanceCoastPhases(Instant.now(), totalCoastTime)) {
            coastPhase2Transitions++;
            vertx.eventBus().publish(
                    EVENT_ADDRESS,
                    TaisWire.update(++revision, target)
            );
        }
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
                " coastPhase2=" + coastPhase2Transitions +
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
