package store;

import messages.FlightMessage;
import messages.JmsMetadata;

import java.time.Instant;
import java.util.List;
import java.util.Map;

/** Immutable copy of one source-ARTCC SFDPS flight for wire/snapshot use. */
public record FlightSnapshot(
        String key,
        String facility,
        String identity,
        String lastMessageType,
        Instant receivedAt,
        Instant messageTime,
        JmsMetadata jms,
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
        FlightMessage.Altitude assignedAltitude,
        FlightMessage.Altitude interimAltitude,
        FlightMessage.Track track,
        FlightMessage.Control control,
        FlightMessage.FourthLine fourthLine,
        FlightMessage.PointOut pointOut,
        FlightMessage.Handoff handoff,
        FlightMessage.Hold hold,
        List<FlightMessage.RoutePoint> route,
        Map<String, String> nameValues,
        boolean tracked
) {
    public FlightSnapshot {
        route = route == null ? List.of() : List.copyOf(route);
        nameValues = nameValues == null ? Map.of() : Map.copyOf(nameValues);
    }
}
