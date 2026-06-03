package ingest;

/**
 * Lean parsed observation from one SMES position report.
 *
 * This is NOT the current state of a target — that is store/TargetStore's job.
 * Null fields mean the field was absent in this particular report.
 * Partial reports omit unchanged fields; downstream merges them into current truth.
 *
 * EventBus address: {@value #ADDRESS}
 */
public record SurfaceTarget(
        String  airport,    // ICAO airport code from <airport> element
        String  track,      // from <track> — part of composite key
        String  stid,       // from <stid> — part of composite key
        boolean isFull,     // from positionReport[@full="true"]

        String  positionReportTime, // from positionReport/time or adsb basicReport/time

        // Identity
        String  tgtType,    // "aircraft", "vehicle", or "unknown"
        String  callsign,   // flightId/aircraftId (UNKN filtered out)
        String  acType,     // flightInfo/acType
        String  squawk,     // flightId/mode3ACode
        String  exitFix,    // flightInfo/fix
        String  wake,       // flightInfo/wake
        String  scratchpad1,// manual/scratchpad1
        String  scratchpad2,// manual/scratchpad2

        // Position + kinematics
        Double  lat,
        Double  lon,
        Double  altitude,
        Double  speed,
        Double  heading
) {
    /** EventBus address for observations from all ingest sources. */
    public static final String ADDRESS = "faa.ingest.observation";

    /** Composite target key: "{airport}:{track}:{stid}". */
    public String targetKey() {
        String a = airport != null ? airport : "UNKN";
        String t = track   != null ? track   : "?";
        String s = stid    != null ? stid    : "?";
        return a + ":" + t + ":" + s;
    }
}
