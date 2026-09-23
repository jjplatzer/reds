package store;

import ingest.TaisObservation;

import java.time.Instant;

/** One raw, time-stamped TAIS position retained for later STARS history rendering/fusion. */
public record HistoryPosition(
        Instant time,
        double lat,
        double lon,
        Integer reportedAltitude,
        Integer vx,
        Integer vy,
        Integer verticalRate,
        Boolean adsb
) {
    static HistoryPosition from(TaisObservation obs) {
        TaisObservation.Track t = obs.track();
        return new HistoryPosition(
                t.mrtTime(), t.lat(), t.lon(), t.reportedAltitude(),
                t.vx(), t.vy(), t.verticalRate(), t.adsb()
        );
    }
}
