package store;

import ingest.SurfaceTarget;
import ingest.TargetBatch;
import io.vertx.core.AbstractVerticle;
import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;

import java.time.Instant;
import java.util.HashMap;
import java.util.Map;
import java.util.Objects;
import java.util.Set;

/**
 * Target state store verticle.
 *
 * Pipeline position:
 *   EventBus({@value SurfaceTarget#ADDRESS}) → merge → diff → EventBus({@value #DIFF_ADDRESS})
 *
 * Responsibilities:
 *   1. Receive SurfaceTargets (batched) from all ingest sources.
 *   2. Merge each observation into current target state (handles full/partial).
 *   3. Compute a field-level diff — only what actually changed.
 *   4. Publish the diff for downstream WebSocket push.
 *   5. Evict stale targets after 3 minutes.
 */
public final class TargetStore extends AbstractVerticle {

    /** EventBus address for outbound diffs — consumed by push/WebSocketPush. */
    public static final String DIFF_ADDRESS = "faa.persist.diff";

    private static final long EVICT_INTERVAL_MS  = 60_000L;
    private static final long STALE_THRESHOLD_MS = 3 * 60 * 1000L;

    private final Map<String, TargetState> store = new HashMap<>();
    private final AirportFilter filter = new AirportFilter();

    @Override
    public void start() {
        // Seed filter from env so the consumer starts already scoped.
        String initial = System.getenv("INITIAL_AIRPORT");
        if (initial != null && !initial.isBlank()) {
            filter.update(new JsonObject().put("airports",
                    new JsonArray().add(initial.strip().toUpperCase())));
            System.out.println("[Store] Initial airport filter: " + filter.active());
        }

        // Airport filter — updated by the UI via WebSocket → EventBus.
        vertx.eventBus().<JsonObject>consumer(AirportFilter.ADDRESS, msg -> {
            Set<String> newActive = filter.update(msg.body());
            pruneToFilter(newActive);
            snapshotTo(newActive);
            System.out.println("[Store] Airport filter → " + (newActive.isEmpty() ? "none" : newActive)
                    + " (" + store.size() + " targets retained)");
        });

        // Subscribe to batched observations.
        vertx.eventBus().<TargetBatch>consumer(SurfaceTarget.ADDRESS, msg -> {
            for (SurfaceTarget obs : msg.body().targets()) {
                if (!filter.accepts(obs.airport())) continue;
                handleObservation(obs);
            }
        });

        // Periodic stale-target eviction
        vertx.setPeriodic(EVICT_INTERVAL_MS, ignored -> evictStale());
    }

    /**
     * Pushes a full-state diff for every target the new filter accepts, so a
     * freshly-(re)connected client doesn't have to wait for AT-full reports
     * to populate its local cache. The frame shape matches handleObservation's
     * normal diff, so the client reuses its existing parser path.
     */
    private void snapshotTo(Set<String> active) {
        Instant now = Instant.now();
        for (Map.Entry<String, TargetState> e : store.entrySet()) {
            TargetState s = e.getValue();
            if (s.airport == null || !active.contains(s.airport)) continue;

            JsonObject changed = new JsonObject();
            if (s.positionReportTime != null) changed.put("positionReportTime", s.positionReportTime);
            if (s.tgtType  != null) changed.put("tgtType",  s.tgtType);
            if (s.callsign != null) changed.put("callsign", s.callsign);
            if (s.acType   != null) changed.put("acType",   s.acType);
            if (s.squawk   != null) changed.put("squawk",   s.squawk);
            if (s.exitFix  != null) changed.put("exitFix",  s.exitFix);
            if (s.wake     != null) changed.put("wake",     s.wake);
            if (s.scratchpad1 != null) changed.put("scratchpad1", s.scratchpad1);
            if (s.scratchpad2 != null) changed.put("scratchpad2", s.scratchpad2);
            if (s.lat      != null) changed.put("lat",      s.lat);
            if (s.lon      != null) changed.put("lon",      s.lon);
            if (s.altitude != null) changed.put("altitude", s.altitude);
            if (s.speed    != null) changed.put("speed",    s.speed);
            if (s.heading  != null) changed.put("heading",  s.heading);
            if (changed.isEmpty()) continue;

            vertx.eventBus().publish(DIFF_ADDRESS, new JsonObject()
                    .put("key",       e.getKey())
                    .put("airport",   s.airport)
                    .put("updatedAt", now.toString())
                    .put("isFull",    true)
                    .put("changed",   changed));
        }
    }

    /**
     * Immediately removes all targets not covered by the new filter and broadcasts
     * removal diffs so the UI can clean up without waiting for TTL eviction.
     */
    private void pruneToFilter(Set<String> newActive) {
        store.entrySet().removeIf(entry -> {
            String airport = entry.getValue().airport;
            if (airport != null && newActive.contains(airport)) return false;
            vertx.eventBus().publish(DIFF_ADDRESS, new JsonObject()
                    .put("key",       entry.getKey())
                    .put("removed",   true)
                    .put("updatedAt", Instant.now().toString()));
            return true;
        });
    }

    private void handleObservation(SurfaceTarget obs) {
        String key = obs.targetKey();
        TargetState state = store.computeIfAbsent(key, k -> new TargetState(obs));

        JsonObject changed = state.merge(obs);
        if (changed.isEmpty()) return;

        vertx.eventBus().publish(DIFF_ADDRESS, new JsonObject()
                .put("key",       key)
                .put("airport",   obs.airport())
                .put("updatedAt", Instant.now().toString())
                .put("isFull",    obs.isFull())
                .put("changed",   changed));
    }

    private void evictStale() {
        Instant cutoff = Instant.now().minusMillis(STALE_THRESHOLD_MS);
        store.entrySet().removeIf(entry -> {
            TargetState s = entry.getValue();
            if (s.updatedAt.isBefore(cutoff)) {
                vertx.eventBus().publish(DIFF_ADDRESS, new JsonObject()
                        .put("key",       entry.getKey())
                        .put("removed",   true)
                        .put("updatedAt", Instant.now().toString()));
                return true;
            }
            return false;
        });
    }

    // =========================================================================
    // Per-target state — holds current truth and computes field-level diffs
    // =========================================================================

    private static final class TargetState {
        Instant updatedAt;
        final String airport;

        String positionReportTime;

        // Identity
        String tgtType, callsign, acType, squawk, exitFix, wake, scratchpad1, scratchpad2;

        // Position + kinematics
        Double lat, lon, altitude, speed, heading;

        TargetState(SurfaceTarget first) {
            this.updatedAt = Instant.now();
            this.airport   = first.airport();
        }

        /**
         * Merges observation into current state and returns a JsonObject containing
         * only the fields that changed. Empty object = nothing changed.
         *
         * Full reports: null incoming clears current (recorded as null in diff).
         * Partial reports: null incoming = no update (current value preserved).
         */
        JsonObject merge(SurfaceTarget obs) {
            updatedAt = Instant.now();
            JsonObject changed = new JsonObject();

            if (obs.isFull()) {
                positionReportTime = trackFull(changed, "positionReportTime", positionReportTime, obs.positionReportTime());
                tgtType  = trackFull(changed, "tgtType",  tgtType,  obs.tgtType());
                callsign = trackFull(changed, "callsign", callsign, obs.callsign());
                acType   = trackFull(changed, "acType",   acType,   obs.acType());
                squawk   = trackFull(changed, "squawk",   squawk,   obs.squawk());
                exitFix  = trackFull(changed, "exitFix",  exitFix,  obs.exitFix());
                wake     = trackFull(changed, "wake",     wake,     obs.wake());
                scratchpad1 = trackFull(changed, "scratchpad1", scratchpad1, obs.scratchpad1());
                scratchpad2 = trackFull(changed, "scratchpad2", scratchpad2, obs.scratchpad2());
                lat      = trackFull(changed, "lat",      lat,      obs.lat());
                lon      = trackFull(changed, "lon",      lon,      obs.lon());
                altitude = trackFull(changed, "altitude", altitude, obs.altitude());
                speed    = trackFull(changed, "speed",    speed,    obs.speed());
                heading  = trackFull(changed, "heading",  heading,  obs.heading());
            } else {
                positionReportTime = track(changed, "positionReportTime", positionReportTime, obs.positionReportTime());
                tgtType  = track(changed, "tgtType",  tgtType,  obs.tgtType());
                callsign = track(changed, "callsign", callsign, obs.callsign());
                acType   = track(changed, "acType",   acType,   obs.acType());
                squawk   = track(changed, "squawk",   squawk,   obs.squawk());
                exitFix  = track(changed, "exitFix",  exitFix,  obs.exitFix());
                wake     = track(changed, "wake",     wake,     obs.wake());
                scratchpad1 = track(changed, "scratchpad1", scratchpad1, obs.scratchpad1());
                scratchpad2 = track(changed, "scratchpad2", scratchpad2, obs.scratchpad2());
                lat      = track(changed, "lat",      lat,      obs.lat());
                lon      = track(changed, "lon",      lon,      obs.lon());
                altitude = track(changed, "altitude", altitude, obs.altitude());
                speed    = track(changed, "speed",    speed,    obs.speed());
                heading  = track(changed, "heading",  heading,  obs.heading());
            }

            return changed;
        }

        // Partial: null incoming = keep current (no change recorded)
        private static String track(JsonObject out, String key, String current, String incoming) {
            if (incoming == null || Objects.equals(current, incoming)) return current;
            out.put(key, incoming);
            return incoming;
        }

        private static Double track(JsonObject out, String key, Double current, Double incoming) {
            if (incoming == null || Objects.equals(current, incoming)) return current;
            out.put(key, incoming);
            return incoming;
        }

        // Full: null incoming clears current (recorded as null in diff)
        private static String trackFull(JsonObject out, String key, String current, String incoming) {
            if (Objects.equals(current, incoming)) return current;
            if (incoming == null) out.putNull(key); else out.put(key, incoming);
            return incoming;
        }

        private static Double trackFull(JsonObject out, String key, Double current, Double incoming) {
            if (Objects.equals(current, incoming)) return current;
            if (incoming == null) out.putNull(key); else out.put(key, incoming);
            return incoming;
        }
    }
}
