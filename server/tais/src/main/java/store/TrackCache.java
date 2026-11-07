package store;

import ingest.TaisObservation;

import java.time.Duration;
import java.time.Instant;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;

/**
 * In-memory current-state + raw-position cache for TAIS tracks.
 *
 * STARS history is a DISPLAY preference, not an ingest sampling rule. The
 * HISTORY control allows up to 10 dots, while H_RATE specifies the minimum
 * elapsed time before STARS is willing to add the next dot; the dot is added on
 * the next radar position update after that threshold. Therefore this cache
 * deliberately keeps every distinct TAIS mrtTime and leaves the 0..10 dot /
 * H_RATE selection to the client display. Pre-thinning here would lose data,
 * especially once a faster ADS-B source is fused later.
 *
 * The public Vice STARS implementation documents the same behavior and cites
 * STARS manual page 4-94 for H_RATE. Its default H_RATE is 4.5 s; with 1 s
 * FUSED radar updates the effective default history-dot cadence is 5 s.
 */
public final class TrackCache {

    public static final int STARS_MAX_HISTORY_DOTS = 10;
    public static final double STARS_DEFAULT_HISTORY_RATE_SECONDS = 4.5;
    public static final int DEFAULT_RAW_HISTORY_CAPACITY = 64;

    public static final int COAST_PHASE_NONE = 0;
    public static final int COAST_PHASE_1 = 1;
    public static final int COAST_PHASE_2 = 2;

    private final int historyCapacity;
    private final Map<String, State> tracks = new HashMap<>();
    private final Map<String, PlanState> plans = new HashMap<>();
    private final Map<String, String> targetPlans = new HashMap<>();
    private final Map<String, Instant> deletedPlans = new HashMap<>();

    private long positionSamples;
    private long duplicatePositions;
    private long outOfOrderPositions;
    private long historyResets;

    public TrackCache(int historyCapacity) {
        if (historyCapacity < STARS_MAX_HISTORY_DOTS) {
            throw new IllegalArgumentException("history capacity must be >= " + STARS_MAX_HISTORY_DOTS);
        }
        this.historyCapacity = historyCapacity;
    }

    public void accept(TaisObservation obs) {
        if (!obs.hasRadarTrack()) return;
        String key = obs.targetKey();
        State state = tracks.computeIfAbsent(key, ignored -> new State());
        TaisObservation.Track track = obs.track();
        Instant time = track.mrtTime();

        if (time != null && state.lastPositionTime != null) {
            if (time.isBefore(state.lastPositionTime)) {
                outOfOrderPositions++;
                return; // Do not regress current state with an older track report.
            }
            if (time.equals(state.lastPositionTime)) {
                duplicatePositions++;
                state.current = obs; // Flight-plan/status fields may still have changed.
                // TAIS exposes the source track status active/coast/drop. A same-MRT
                // coast status is sufficient to enter Phase 1, but a same-MRT active
                // flight-plan update must not resurrect a Phase 2 track; only a newer
                // radar position does that.
                if (isCoast(track.status()) && state.coastPhase < COAST_PHASE_2) {
                    state.coastPhase = COAST_PHASE_1;
                }
                return;
            }
        }

        // A new STARS track may reuse a track number that previously belonged
        // to another aircraft. Clear the old trail exactly once on the first
        // newer report carrying the explicit <new> flag.
        if (Boolean.TRUE.equals(track.newTrack()) && state.current != null) {
            state.history.clear();
            state.lastPositionTime = null;
            state.coastPhase = COAST_PHASE_NONE;
            state.coastPhase2At = null;
            historyResets++;
        }

        state.current = obs;

        if (!track.hasPosition()) {
            if (isCoast(track.status()) && state.coastPhase < COAST_PHASE_2) {
                state.coastPhase = COAST_PHASE_1;
            }
            return;
        }

        // A newer surveillance position is a STARS re-association and therefore
        // removes the target from Coast Phase 2. If TAIS itself already labels
        // that newer report as coast, start it in Phase 1 instead.
        state.coastPhase = isCoast(track.status()) ? COAST_PHASE_1 : COAST_PHASE_NONE;
        state.coastPhase2At = null;

        state.history.addLast(HistoryPosition.from(obs));
        state.lastPositionTime = time;
        positionSamples++;

        while (state.history.size() > historyCapacity) {
            state.history.removeFirst();
        }
    }

    /** Apply independent flight-plan and radar lifecycles on the store event loop. */
    public List<Change> apply(TaisObservation obs) {
        Map<String, SnapshotPlan> before = new LinkedHashMap<>();
        Map<String, Change> changedTargets = new LinkedHashMap<>();
        String planKey = planKey(obs);
        Instant sourceTime = sourceTime(obs);
        PlanState plan = planKey == null ? null : plans.get(planKey);
        boolean stalePlan = plan != null && sourceTime.isBefore(plan.sourceTime);
        Instant deletedAt = planKey == null ? null : deletedPlans.get(planKey);
        if (deletedAt != null && !sourceTime.isAfter(deletedAt)) stalePlan = true;
        if (planKey != null && !stalePlan) {
            remember(before, planKey);
            var fp = obs.flightPlan();
            if (Boolean.TRUE.equals(fp.deleted()) || "terminated".equalsIgnoreCase(fp.status())) {
                if (plan != null) detach(plan, obs, before, changedTargets);
                plans.remove(planKey);
                deletedPlans.put(planKey, sourceTime);
                plan = null;
            } else {
                // Require both identities to change before treating an undeleted
                // SFPN as reused. GUFI enrichment alone may change source center;
                // an ACID amendment alone retains this plan's lifetime.
                if (plan != null && !Objects.equals(plan.flightPlan.acid(), fp.acid()) &&
                        differentGufi(plan.enhancedData, obs.enhancedData())) {
                    detach(plan, obs, before, changedTargets);
                    plan = null;
                }
                if (plan == null) {
                    plan = new PlanState(planKey, obs.facility(), obs.receivedAt());
                    plans.put(planKey, plan);
                }
                deletedPlans.remove(planKey);
                boolean wasVFR = isVFR(plan.flightPlan);
                plan.flightPlan = fp;
                if (obs.enhancedData() != null) plan.enhancedData = obs.enhancedData();
                plan.sourceTime = sourceTime;
                if (isVFR(fp) && !wasVFR) plan.vfrSince = obs.receivedAt();
                if (!isVFR(fp)) plan.vfrSince = null;
                if (plan.trackKey == null && obs.track() != null && Boolean.TRUE.equals(obs.track().pseudo())) {
                    plan.state = "pending";
                }
                if (Boolean.TRUE.equals(fp.suspended())) plan.state = "suspended";
                else if (plan.trackKey != null) {
                    State track = tracks.get(plan.trackKey);
                    plan.state = track != null && track.coastPhase >= COAST_PHASE_2 ? "coasting" : "tracked";
                } else if (plan.state.equals("suspended")) plan.state = "inactive";
                // A missing <track> updates the plan; it does not disassociate a radar target.
                if (plan.trackKey != null && !obs.hasRadarTrack()) patchTarget(plan.trackKey, plan, obs, changedTargets);
            }
        }

        if (obs.hasRadarTrack()) {
            String key = obs.targetKey();
            TaisObservation previous = current(key);
            // A delayed drop must not delete a recycled track number's newer aircraft.
            boolean staleTrack = "drop".equalsIgnoreCase(obs.track().status()) && previous != null &&
                    sourceTime.isBefore(sourceTime(previous));
            if (!staleTrack && "drop".equalsIgnoreCase(obs.track().status())) {
                unlinkTarget(key, obs, before);
                if (plan != null && plan.trackKey == null) {
                    // A first-seen dropped track is not a newly pending flight.
                    remember(before, plan.key);
                    plan.state = Boolean.TRUE.equals(plan.flightPlan.suspended()) ? "suspended" : "inactive";
                }
                if (remove(key)) changedTargets.put(key, new Change("remove", key));
            } else if (!staleTrack) {
                var fp = plan == null ? null : plan.flightPlan;
                if (stalePlan && plan != null && !key.equals(plan.trackKey)) fp = null;
                TaisObservation normalized = new TaisObservation(obs.facility(), obs.recordMeta(), obs.track(),
                        fp, plan == null ? obs.enhancedData() : plan.enhancedData, obs.receivedAt());
                accept(normalized);
                if (current(key) == normalized) {
                    String oldKey = targetPlans.get(key);
                    if (oldKey != null && (plan == null || !oldKey.equals(plan.key) || fp == null)) {
                        unlinkTarget(key, obs, before);
                    }
                    if (plan != null && fp != null && !stalePlan &&
                            (obs.track().hasPosition() || key.equals(plan.trackKey))) {
                        remember(before, plan.key);
                        if (plan.trackKey != null && !plan.trackKey.equals(key)) {
                            detach(plan, obs, before, changedTargets);
                        }
                        plan.trackKey = key;
                        targetPlans.put(key, plan.key);
                        State state = tracks.get(key);
                        plan.state = Boolean.TRUE.equals(fp.suspended()) ? "suspended" :
                                state.coastPhase >= COAST_PHASE_2 ? "coasting" : "tracked";
                    }
                    changedTargets.put(key, new Change("update", key));
                } else if (plan != null && !stalePlan && key.equals(plan.trackKey)) {
                    // A newer plan may accompany an older surveillance sample.
                    patchTarget(key, plan, obs, changedTargets);
                }
            }
        }
        List<Change> changes = new ArrayList<>(changedTargets.values());
        before.forEach((key, old) -> {
            SnapshotPlan next = snapshotPlan(key);
            if (!Objects.equals(old, next)) changes.add(new Change(next == null ? "plan_remove" : "plan_update", key));
        });
        return changes;
    }

    private void remember(Map<String, SnapshotPlan> before, String key) {
        if (!before.containsKey(key)) before.put(key, snapshotPlan(key));
    }

    private void detach(PlanState plan, TaisObservation obs, Map<String, SnapshotPlan> before,
                        Map<String, Change> changedTargets) {
        remember(before, plan.key);
        if (plan.trackKey != null) {
            patchTarget(plan.trackKey, null, obs, changedTargets);
            targetPlans.remove(plan.trackKey);
            plan.trackKey = null;
        }
    }

    private void unlinkTarget(String key, TaisObservation obs, Map<String, SnapshotPlan> before) {
        String planKey = targetPlans.remove(key);
        PlanState plan = planKey == null ? null : plans.get(planKey);
        if (plan != null && key.equals(plan.trackKey)) {
            remember(before, planKey);
            plan.trackKey = null;
            plan.state = Boolean.TRUE.equals(plan.flightPlan.suspended()) ? "suspended" : "inactive";
            if (sourceTime(obs).isAfter(plan.sourceTime)) plan.sourceTime = sourceTime(obs);
        }
    }

    private void patchTarget(String key, PlanState plan, TaisObservation update, Map<String, Change> changes) {
        TaisObservation previous = current(key);
        if (previous == null) return;
        TaisObservation patched = new TaisObservation(previous.facility(), previous.recordMeta(), previous.track(),
                plan == null ? null : plan.flightPlan, plan == null ? null : plan.enhancedData, update.receivedAt());
        accept(patched); // Same MRT: updates data without adding a history dot or resetting coast phase.
        changes.put(key, new Change("update", key));
    }

    public SnapshotPlan markPlanCoasting(String targetKey) {
        PlanState plan = plans.get(targetPlans.get(targetKey));
        if (plan == null || plan.state.equals("coasting") || plan.state.equals("suspended")) return null;
        plan.state = "coasting";
        return plan.snapshot();
    }

    public SnapshotPlan snapshotPlan(String key) {
        PlanState plan = plans.get(key);
        return plan == null ? null : plan.snapshot();
    }

    public List<SnapshotPlan> snapshotPlans(String facility) {
        return plans.values().stream().filter(p -> facility == null || facility.isBlank() || facility.equalsIgnoreCase(p.facility))
                .map(PlanState::snapshot).sorted(java.util.Comparator.comparing(SnapshotPlan::key)).toList();
    }

    private static Instant sourceTime(TaisObservation obs) {
        return obs.recordMeta() != null && obs.recordMeta().safaReceiptTime() != null
                ? obs.recordMeta().safaReceiptTime() : obs.receivedAt();
    }

    private static String planKey(TaisObservation obs) {
        if (obs.flightPlan() == null) return null;
        Integer sfpn = obs.flightPlan().sfpn();
        if (sfpn != null && sfpn >= 0) return obs.facility() + ":FP:" + sfpn;
        String gufi = obs.enhancedData() == null ? null : obs.enhancedData().sfdpsGufi();
        return gufi == null || gufi.isBlank() ? null : obs.facility() + ":G:" + gufi;
    }

    private static boolean differentGufi(TaisObservation.EnhancedData a, TaisObservation.EnhancedData b) {
        return a != null && b != null && ((nonempty(a.sfdpsGufi()) && nonempty(b.sfdpsGufi()) && !a.sfdpsGufi().equals(b.sfdpsGufi())) ||
                (nonempty(a.eramGufi()) && nonempty(b.eramGufi()) && !a.eramGufi().equals(b.eramGufi())));
    }

    private static boolean nonempty(String value) { return value != null && !value.isBlank(); }
    private static boolean isVFR(TaisObservation.FlightPlan fp) {
        return fp != null && ("V".equalsIgnoreCase(fp.rawFlightRules()) || "V".equalsIgnoreCase(fp.flightRules()) ||
                "VFR".equalsIgnoreCase(fp.flightRules()));
    }

    public record Change(String type, String key) {}
    public record SnapshotPlan(String key, String facility, Instant firstSeen, Instant vfrSince,
                               String state, String trackKey, TaisObservation.FlightPlan flightPlan,
                               TaisObservation.EnhancedData enhancedData) {}

    private static final class PlanState {
        final String key, facility;
        final Instant firstSeen;
        Instant sourceTime, vfrSince;
        String state = "pending", trackKey;
        TaisObservation.FlightPlan flightPlan;
        TaisObservation.EnhancedData enhancedData;

        PlanState(String key, String facility, Instant firstSeen) {
            this.key = key;
            this.facility = facility;
            this.firstSeen = firstSeen;
        }

        SnapshotPlan snapshot() {
            return new SnapshotPlan(key, facility, firstSeen, vfrSince, state, trackKey, flightPlan, enhancedData);
        }
    }

    public int trackCount() {
        return tracks.size();
    }

    public long historySampleCount() {
        long count = 0;
        for (State state : tracks.values()) count += state.history.size();
        return count;
    }

    public Stats stats() {
        return new Stats(trackCount(), historySampleCount(), positionSamples,
                duplicatePositions, outOfOrderPositions, historyResets);
    }

    /** Read-only copy, newest position last. Useful for the future display/fusion layer. */
    public List<HistoryPosition> history(String targetKey) {
        State state = tracks.get(targetKey);
        return state == null ? List.of() : List.copyOf(new ArrayList<>(state.history));
    }

    public TaisObservation current(String targetKey) {
        State state = tracks.get(targetKey);
        return state == null ? null : state.current;
    }

    /** Remove a source-declared dropped STARS track from current state and history. */
    public boolean remove(String targetKey) {
        return tracks.remove(targetKey) != null;
    }

    /**
     * Advance stale radar tracks into STARS Coast Phase 2 after Total Coast
     * Time. Phase 2 is deliberately retained in current state: TI 6191.409
     * moves the track from the radar display to the Coast/Suspend List at this
     * point; it does not define a second display-side expiration timer. TAIS
     * exposes a distinct source track status of "drop", and that later source
     * event is what removes the record from this cache.
     */
    public List<SnapshotTarget> advanceCoastPhases(Instant now, Duration totalCoastTime) {
        if (totalCoastTime.isNegative() || totalCoastTime.isZero()) {
            throw new IllegalArgumentException("total coast time must be positive");
        }

        List<SnapshotTarget> transitioned = new ArrayList<>();
        for (State state : tracks.values()) {
            if (state.current == null || state.lastPositionTime == null || state.coastPhase >= COAST_PHASE_2) {
                continue;
            }
            Instant phase2At = state.lastPositionTime.plus(totalCoastTime);
            if (phase2At.isAfter(now)) {
                continue;
            }
            state.coastPhase = COAST_PHASE_2;
            state.coastPhase2At = phase2At;
            transitioned.add(snapshotTarget(state));
        }

        transitioned.sort((a, b) -> a.target().targetKey().compareTo(b.target().targetKey()));
        return List.copyOf(transitioned);
    }

    /**
     * Legacy destructive coast-expiry API retained for source compatibility
     * with older callers/tests. Live TrackStore no longer uses this method;
     * production STARS behavior uses advanceCoastPhases() and waits for the
     * source TAIS drop event before final removal.
     */
    @Deprecated(forRemoval = true)
    public List<String> removeCoastedOutTracks(Instant now, Duration totalCoastTime) {
        if (totalCoastTime.isNegative() || totalCoastTime.isZero()) {
            throw new IllegalArgumentException("total coast time must be positive");
        }
        Instant cutoff = now.minus(totalCoastTime);
        List<String> removed = new ArrayList<>();
        var iterator = tracks.entrySet().iterator();
        while (iterator.hasNext()) {
            Map.Entry<String, State> entry = iterator.next();
            Instant lastPositionTime = entry.getValue().lastPositionTime;
            if (lastPositionTime != null && !lastPositionTime.isAfter(cutoff)) {
                removed.add(entry.getKey());
                iterator.remove();
            }
        }
        removed.sort(String::compareTo);
        return List.copyOf(removed);
    }

    public SnapshotTarget snapshotTarget(String targetKey) {
        State state = tracks.get(targetKey);
        return state == null || state.current == null ? null : snapshotTarget(state);
    }

    /**
     * Immutable point-in-time copy used by a newly connected WebSocket client.
     * The mutable cache itself never crosses Vert.x event-loop ownership.
     */
    public List<SnapshotTarget> snapshot() {
        return snapshot(null);
    }

    /**
     * Immutable point-in-time copy restricted to one TAIS source facility.
     * A null/blank facility preserves the legacy all-facility snapshot for
     * internal callers and tests; public WebSocket clients always request a
     * concrete facility.
     */
    public List<SnapshotTarget> snapshot(String facility) {
        boolean scoped = facility != null && !facility.isBlank();
        List<SnapshotTarget> out = new ArrayList<>(tracks.size());

        for (State state : tracks.values()) {
            if (state.current == null) continue;
            if (scoped && !facility.equalsIgnoreCase(state.current.facility())) continue;
            out.add(snapshotTarget(state));
        }

        out.sort((a, b) -> a.target().targetKey().compareTo(b.target().targetKey()));
        return List.copyOf(out);
    }

    private SnapshotTarget snapshotTarget(State state) {
        return new SnapshotTarget(
                state.current,
                List.copyOf(new ArrayList<>(state.history)),
                state.coastPhase,
                state.coastPhase2At
        );
    }

    private static boolean isCoast(String status) {
        return status != null && status.equalsIgnoreCase("coast");
    }

    public record SnapshotTarget(
            TaisObservation target,
            List<HistoryPosition> history,
            int coastPhase,
            Instant coastPhase2At
    ) {
        public SnapshotTarget {
            history = List.copyOf(history);
        }
    }

    public record Stats(
            int tracks,
            long cachedHistorySamples,
            long acceptedPositionSamples,
            long duplicatePositions,
            long outOfOrderPositions,
            long historyResets
    ) {}

    private static final class State {
        TaisObservation current;
        Instant lastPositionTime;
        int coastPhase;
        Instant coastPhase2At;
        final ArrayDeque<HistoryPosition> history = new ArrayDeque<>();
    }
}
