package store;

import ingest.TaisObservation;

import java.time.Instant;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

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

    private final int historyCapacity;
    private final Map<String, State> tracks = new HashMap<>();

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
                state.current = obs; // Flight-plan fields may still have changed.
                return;
            }
        }

        // A new STARS track may reuse a track number that previously belonged
        // to another aircraft. Clear the old trail exactly once on the first
        // newer report carrying the explicit <new> flag.
        if (Boolean.TRUE.equals(track.newTrack()) && state.current != null) {
            state.history.clear();
            state.lastPositionTime = null;
            historyResets++;
        }

        state.current = obs;

        if (!track.hasPosition()) return;

        state.history.addLast(HistoryPosition.from(obs));
        state.lastPositionTime = time;
        positionSamples++;

        while (state.history.size() > historyCapacity) {
            state.history.removeFirst();
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
        final ArrayDeque<HistoryPosition> history = new ArrayDeque<>();
    }
}
