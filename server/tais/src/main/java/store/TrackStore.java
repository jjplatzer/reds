package store;

import config.Env;
import ingest.TaisBatch;
import ingest.TaisObservation;
import io.vertx.core.AbstractVerticle;
import io.vertx.core.json.JsonObject;
import registry.FaaRegistry;
import wire.TaisWire;

import java.time.Duration;
import java.time.Instant;
import java.util.HashMap;
import java.util.HashSet;
import java.util.Locale;
import java.util.Map;
import java.util.Set;

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

    private final FaaRegistry registry;

    public TrackStore(FaaRegistry registry) {
        this.registry = java.util.Objects.requireNonNull(registry);
    }

    private TrackCache cache;
    private Duration totalCoastTime;
    private final Map<String, Long> facilityRevisions = new HashMap<>();
    private final Map<String, Set<String>> subscribers = new HashMap<>();
    private long liveFrames;
    private long skippedLiveFrames;
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
        vertx.eventBus().<Object>localConsumer(SNAPSHOT_ADDRESS, message -> {
            long started = System.nanoTime();
            // Retain string requests for legacy in-process callers. The new
            // desktop opts out of bootstrap history and builds it from live data.
            Object request = message.body();
            String facility = normalizeFacility(request instanceof JsonObject options
                    ? options.getString("facility") : (String) request);
            // Register interest atomically with the snapshot boundary. Releases
            // use this same consumer, preserving connect/close ordering even if
            // a socket disappears before its snapshot reply arrives.
            String subscriber = request instanceof JsonObject options
                    ? options.getString("subscriber") : null;
            if (subscriber != null) {
                if (((JsonObject) request).getBoolean("unsubscribe", false)) {
                    Set<String> watching = subscribers.get(facility);
                    if (watching != null) {
                        watching.remove(subscriber);
                        if (watching.isEmpty()) subscribers.remove(facility);
                    }
                    return;
                }
                subscribers.computeIfAbsent(facility, ignored -> new HashSet<>()).add(subscriber);
            }
            boolean includeHistory = !(request instanceof JsonObject options)
                    || options.getBoolean("includeHistory", true);
            var targets = cache.snapshot(facility, includeHistory);
            JsonObject snapshot = TaisWire.snapshot(
                    facilityRevision(facility), Instant.now(), facility, targets, cache.snapshotPlans(facility), registry
            );
            long elapsedMs = (System.nanoTime() - started) / 1_000_000;
            if (elapsedMs >= 250) {
                // Building a large snapshot on the store event loop can delay
                // ordinary ingest; record the cost independently of WS writes.
                System.err.println("[TAIS store] Slow snapshot build; facility=" + facility +
                        " targets=" + targets.size() + " durationMs=" + elapsedMs);
            }
            message.reply(snapshot);
        });

        vertx.eventBus().<TaisBatch>consumer(TaisBatch.ADDRESS, message -> {
            for (TaisObservation obs : message.body().observations()) {
                for (TrackCache.Change change : cache.apply(obs)) {
                    long next = nextFacilityRevision(obs.facility());
                    revision++;
                    if (change.type().equals("remove")) removals++;
                    // Always maintain cache state and source revisions. Only
                    // materialize outbound views for facilities being watched.
                    if (!needsLiveFrame(obs.facility())) continue;
                    JsonObject frame = switch (change.type()) {
                        case "update" -> TaisWire.update(next, cache.snapshotTarget(change.key(), false), registry);
                        case "remove" -> TaisWire.remove(next, obs.facility(), change.key());
                        case "plan_update" -> TaisWire.planUpdate(next, cache.snapshotPlan(change.key()));
                        case "plan_remove" -> TaisWire.planRemove(next, obs.facility(), change.key());
                        default -> throw new IllegalStateException("Unknown TAIS change");
                    };
                    vertx.eventBus().publish(EVENT_ADDRESS, frame);
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
            revision++;
            String facility = target.target().facility();
            long next = nextFacilityRevision(facility);
            if (needsLiveFrame(facility)) {
                vertx.eventBus().publish(EVENT_ADDRESS, TaisWire.update(next, target, registry));
            }
            TrackCache.SnapshotPlan plan = cache.markPlanCoasting(target.target().targetKey());
            if (plan != null) {
                revision++;
                long planRevision = nextFacilityRevision(plan.facility());
                if (needsLiveFrame(plan.facility())) {
                    vertx.eventBus().publish(EVENT_ADDRESS, TaisWire.planUpdate(planRevision, plan));
                }
            }
        }
    }

    private boolean needsLiveFrame(String facility) {
        boolean watched = subscribers.containsKey(normalizeFacility(facility));
        if (watched) liveFrames++;
        else skippedLiveFrames++;
        return watched;
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
                " watchedFacilities=" + subscribers.size() +
                " liveFrames=" + liveFrames +
                " skippedLiveFrames=" + skippedLiveFrames +
                " revision=" + revision);
    }

    private long facilityRevision(String facility) {
        return facilityRevisions.getOrDefault(normalizeFacility(facility), 0L);
    }

    private long nextFacilityRevision(String facility) {
        String normalized = normalizeFacility(facility);
        long next = facilityRevisions.getOrDefault(normalized, 0L) + 1L;
        facilityRevisions.put(normalized, next);
        return next;
    }

    private static String normalizeFacility(String facility) {
        return facility == null ? "" : facility.strip().toUpperCase(Locale.ROOT);
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
