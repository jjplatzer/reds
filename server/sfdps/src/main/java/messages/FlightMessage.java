package messages;

import java.time.Instant;
import java.util.List;
import java.util.Map;

/**
 * Normalized subset of SFDPS Flight FIXM needed by ERAM.
 *
 * Fields are nullable because each SFDPS message type carries only the portion
 * of the flight state that changed. The store applies message-type-aware merge
 * rules; parsers do not infer display state.
 */
public record FlightMessage(
        String messageType,
        String facility,
        Instant messageTime,
        String sfdpsGufi,
        String uuidGufi,
        String flightPlanId,
        String flightState,
        String aircraftIdentification,
        String previousAircraftIdentification,
        String computerId,
        String siteSpecificPlanId,
        String aircraftType,
        String departurePoint,
        String arrivalPoint,
        String beaconCode,
        Altitude assignedAltitude,
        Altitude interimAltitude,
        Track track,
        Control control,
        FourthLine fourthLine,
        PointOut pointOut,
        Handoff handoff,
        Hold hold,
        List<RoutePoint> route,
        Map<String, String> nameValues
) {
    public FlightMessage {
        route = route == null ? List.of() : List.copyOf(route);
        nameValues = nameValues == null ? Map.of() : Map.copyOf(nameValues);
    }

    public String identity() {
        if (notBlank(sfdpsGufi)) return "S:" + sfdpsGufi;
        if (notBlank(uuidGufi)) return "U:" + uuidGufi;
        if (notBlank(flightPlanId)) return "F:" + flightPlanId;
        if (notBlank(siteSpecificPlanId)) return "P:" + siteSpecificPlanId;
        if (notBlank(computerId)) return "C:" + computerId;
        if (notBlank(aircraftIdentification)) return "A:" + aircraftIdentification;
        return null;
    }

    private static boolean notBlank(String value) { return value != null && !value.isBlank(); }

    public record Altitude(String kind, Double value, Double lower, Double upper, String raw, String uom) {}

    public record Track(
            Instant positionTime,
            Double lat,
            Double lon,
            Double reportedAltitude,
            String reportedAltitudeUom,
            Double targetLat,
            Double targetLon,
            Double targetAltitude,
            String targetAltitudeUom,
            Boolean targetAltitudeInvalid,
            Instant targetPositionTime,
            Double velocityX,
            Double velocityY,
            String velocityUom,
            Double groundSpeed,
            String groundSpeedUom,
            Double heading,
            String headingUom,
            String coastIndicator,
            String reportSource
    ) {}

    public record Control(
            String controllingFacility,
            String controllingSector,
            String receivingFacility,
            String receivingSector,
            String acceptingFacility,
            String acceptingSector
    ) {}

    public record FourthLine(String heading, String speed, String text) {}
    public record PointOut(String originatingFacility, String originatingSector, String receivingFacility, String receivingSector) {}
    public record Handoff(String event, String transferringFacility, String transferringSector,
                          String receivingFacility, String receivingSector,
                          String acceptingFacility, String acceptingSector) {}
    public record Hold(String fix, String expectFurtherClearanceTime, Boolean airborne) {}
    public record RoutePoint(String designator, Instant estimatedTime) {}
}
