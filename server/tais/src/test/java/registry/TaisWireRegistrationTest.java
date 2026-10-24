package registry;

import ingest.TaisObservation;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import store.TrackCache;
import wire.TaisWire;

import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;

import static org.junit.jupiter.api.Assertions.*;

final class TaisWireRegistrationTest {
    @TempDir Path temp;

    @Test
    void includesRegistrationOnAdsbSnapshotAndUpdateButNotOnOtherTracks() throws Exception {
        Path master = temp.resolve("MASTER.txt");
        Files.writeString(master, "N-NUMBER,MODE S CODE HEX\n172SP,A9D334\n");
        FaaRegistry registry = FaaRegistry.load(master);
        TrackCache cache = new TrackCache(10);
        cache.accept(track(true, "A9D334"));
        var target = cache.snapshot("N90").getFirst();

        var snapTrack = TaisWire.snapshot(1, Instant.now(), "N90", cache.snapshot("N90"), registry)
                .getJsonArray("targets").getJsonObject(0).getJsonObject("track");
        assertEquals("N172SP", snapTrack.getString("registration"));
        assertNull(snapTrack.getString("adsbFlightId")); // Never fabricate transmitted identity.

        var updateTrack = TaisWire.update(2, target, registry)
                .getJsonObject("target").getJsonObject("track");
        assertEquals("N172SP", updateTrack.getString("registration"));

        cache.accept(track(false, "A9D334"));
        var nonAdsb = TaisWire.update(3, cache.snapshot("N90").getFirst(), registry)
                .getJsonObject("target").getJsonObject("track");
        assertFalse(nonAdsb.containsKey("registration"));
    }

    private static TaisObservation track(boolean adsb, String address) {
        Instant time = Instant.parse("2026-10-24T16:29:46Z");
        return new TaisObservation("N90", null,
                new TaisObservation.Track("2243", time, "active", address,
                        null, null, 40.3, -74.0, 0, 10, 10,
                        null, null, null, false, false, false, adsb, "1200", 900),
                null, null, time);
    }
}
