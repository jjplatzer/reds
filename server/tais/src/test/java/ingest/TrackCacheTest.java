package ingest;

import org.junit.jupiter.api.Test;
import store.TrackCache;

import java.time.Instant;

import static org.junit.jupiter.api.Assertions.assertEquals;

final class TrackCacheTest {

    @Test
    void cachesDistinctMrtPositionsAndIgnoresOlderOnes() {
        TrackCache cache = new TrackCache(10);
        cache.accept(observation("2026-09-23T13:00:00Z", false));
        cache.accept(observation("2026-09-23T13:00:04Z", false));
        cache.accept(observation("2026-09-23T13:00:04Z", false)); // duplicate position
        cache.accept(observation("2026-09-23T12:59:59Z", false)); // out of order

        assertEquals(2, cache.history("N90:T42").size());
        assertEquals(1, cache.stats().duplicatePositions());
        assertEquals(1, cache.stats().outOfOrderPositions());
    }

    @Test
    void newTrackFlagResetsHistoryForReusedTrackNumber() {
        TrackCache cache = new TrackCache(10);
        cache.accept(observation("2026-09-23T13:00:00Z", false));
        cache.accept(observation("2026-09-23T13:00:04Z", false));
        cache.accept(observation("2026-09-23T13:10:00Z", true));

        assertEquals(1, cache.history("N90:T42").size());
        assertEquals(1, cache.stats().historyResets());
    }

    private static TaisObservation observation(String time, boolean isNew) {
        return new TaisObservation(
                "N90",
                new TaisObservation.RecordMeta(null, null, null, null, null, null, null, null),
                new TaisObservation.Track(
                        "42", Instant.parse(time), "active", "abcdef",
                        null, null, 40.0, -73.0,
                        0, 100, 100, null, null, null,
                        false, isNew, false, true, "1200", 5000
                ),
                null,
                null,
                Instant.parse(time)
        );
    }
}
