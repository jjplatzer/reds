package ingest;

import java.time.Instant;

/**
 * Normalized representation of one TAIS TATrackAndFlightPlan/record.
 *
 * The shape intentionally stays close to FAA SimpleXML instead of prematurely
 * converting units or inferring controller-display state. This will make the
 * later TAIS + ADS-B fusion step explicit and testable.
 *
 * FAA "FIXM Mediated STDDS Data Overview v2", section 2.3.4, maps the
 * SimpleXML fields used here (mrtTime, acAddress, vx/vy, beacon code,
 * altitude, lat/lon, flight-plan fields and trackNum) into their FIXM
 * equivalents. The live v4 TAIS feed identifies the source facility with the
 * root-level <src> element.
 */
public record TaisObservation(
        String facility,
        RecordMeta recordMeta,
        Track track,
        FlightPlan flightPlan,
        EnhancedData enhancedData,
        Instant receivedAt
) {
    /** STARS track numbers are only unique within their source facility. */
    public String targetKey() {
        return facility + ":T" + track.trackNum();
    }

    public record RecordMeta(
            Long sequenceNumber,
            String source,
            Integer type,
            Long starsTimestamp,
            Integer starsSourceId,
            Boolean starsTimeSync,
            Instant safaReceiptTime,
            Boolean safaTimeSync
    ) {}

    public record Track(
            String trackNum,
            Instant mrtTime,
            String status,
            String acAddress,
            Integer xPos,
            Integer yPos,
            Double lat,
            Double lon,
            Integer verticalRate,
            Integer vx,
            Integer vy,
            Integer verticalRateRaw,
            Integer vxRaw,
            Integer vyRaw,
            Boolean frozen,
            Boolean newTrack,
            Boolean pseudo,
            Boolean adsb,
            String reportedBeaconCode,
            Integer reportedAltitude
    ) {
        public boolean hasPosition() {
            return mrtTime != null && lat != null && lon != null;
        }
    }

    public record FlightPlan(
            Integer sfpn,
            String ocr,
            Integer rnav,
            String scratchPad1,
            String scratchPad2,
            String cps,
            String runway,
            String assignedBeaconCode,
            Integer requestedAltitude,
            Integer assignedAltitude,
            String category,
            String dbi,
            String acid,
            String acType,
            String entryFix,
            String exitFix,
            String airport,
            String flightRules,
            String rawFlightRules,
            String type,
            String ptdTime,
            String status,
            Boolean deleted,
            Boolean suspended,
            String lld,
            String ecid,
            String eqptSuffix
    ) {}

    public record EnhancedData(
            String eramGufi,
            String sfdpsGufi,
            String departureAirport,
            String destinationAirport
    ) {}
}
