package store;

import messages.FlightMessage;
import messages.JmsMetadata;
import messages.SfdpsEvent;

import java.time.Instant;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/** Mutable, single-event-loop ERAM flight state reduced from Flight FIXM events. */
final class FlightState {
    final String key;
    final String facility;
    final String identity;

    String lastMessageType;
    Instant receivedAt;
    Instant messageTime;
    JmsMetadata jms;

    String sfdpsGufi;
    String uuidGufi;
    String flightPlanId;
    String flightState;
    String aircraftIdentification;
    String previousAircraftIdentification;
    String computerId;
    String siteSpecificPlanId;
    String aircraftType;
    String departurePoint;
    String arrivalPoint;
    String beaconCode;
    FlightMessage.Altitude assignedAltitude;
    FlightMessage.Altitude interimAltitude;
    FlightMessage.Track track;
    FlightMessage.Control control;
    FlightMessage.FourthLine fourthLine;
    FlightMessage.PointOut pointOut;
    FlightMessage.Handoff handoff;
    FlightMessage.Hold hold;
    List<FlightMessage.RoutePoint> route = List.of();
    Map<String, String> nameValues = Map.of();
    boolean tracked;

    FlightState(String key, String facility, String identity) {
        this.key = key;
        this.facility = facility;
        this.identity = identity;
    }

    void apply(SfdpsEvent event) {
        FlightMessage msg = event.flight();
        if (msg == null) return;

        lastMessageType = upper(msg.messageType());
        receivedAt = event.receivedAt();
        messageTime = first(msg.messageTime(), messageTime);
        jms = event.jms();

        sfdpsGufi = choose(msg.sfdpsGufi(), sfdpsGufi);
        uuidGufi = choose(msg.uuidGufi(), uuidGufi);
        flightPlanId = choose(msg.flightPlanId(), flightPlanId);
        flightState = choose(msg.flightState(), flightState);
        aircraftIdentification = choose(msg.aircraftIdentification(), aircraftIdentification);
        previousAircraftIdentification = choose(msg.previousAircraftIdentification(), previousAircraftIdentification);
        computerId = choose(msg.computerId(), computerId);
        siteSpecificPlanId = choose(msg.siteSpecificPlanId(), siteSpecificPlanId);
        aircraftType = choose(msg.aircraftType(), aircraftType);
        departurePoint = choose(msg.departurePoint(), departurePoint);
        arrivalPoint = choose(msg.arrivalPoint(), arrivalPoint);
        beaconCode = choose(msg.beaconCode(), beaconCode);
        assignedAltitude = choose(msg.assignedAltitude(), assignedAltitude);
        control = choose(msg.control(), control);

        if (msg.track() != null) {
            track = msg.track();
            tracked = true;
        }
        if (!msg.route().isEmpty()) route = msg.route();
        if (!msg.nameValues().isEmpty()) nameValues = msg.nameValues();

        switch (lastMessageType == null ? "" : lastMessageType) {
            case "DH" -> { if (msg.flightState() == null) flightState = "ACTIVE"; }
            case "HV" -> { if (msg.flightState() == null) flightState = "LANDED"; }
            case "CL" -> { if (msg.flightState() == null) flightState = "CANCELLED"; }
            case "RH" -> tracked = false;
            case "LH" -> interimAltitude = msg.interimAltitude(); // null means explicit deletion.
            case "HF" -> fourthLine = msg.fourthLine();           // null means line cleared.
            case "HT", "PT" -> pointOut = msg.pointOut();
            case "OH" -> {
                handoff = msg.handoff();
                control = choose(msg.control(), control);
            }
            case "HH", "PH" -> applyHoldPatch(msg.hold());
            default -> {
                interimAltitude = choose(msg.interimAltitude(), interimAltitude);
                fourthLine = choose(msg.fourthLine(), fourthLine);
                pointOut = choose(msg.pointOut(), pointOut);
                handoff = choose(msg.handoff(), handoff);
                applyHoldPatch(msg.hold());
            }
        }
    }

    private void applyHoldPatch(FlightMessage.Hold incoming) {
        if (incoming == null) return;
        if (Boolean.FALSE.equals(incoming.airborne())) {
            hold = null;
            return;
        }
        if (hold == null) {
            hold = incoming;
            return;
        }
        hold = new FlightMessage.Hold(
                choose(incoming.fix(), hold.fix()),
                choose(incoming.expectFurtherClearanceTime(), hold.expectFurtherClearanceTime()),
                incoming.airborne() == null ? hold.airborne() : incoming.airborne()
        );
    }

    boolean terminal() {
        String state = upper(flightState);
        return "CANCELLED".equals(state) || "LANDED".equals(state) || "DROPPED".equals(state)
                || "CL".equals(lastMessageType) || "HV".equals(lastMessageType);
    }

    boolean droppedTrack() { return "RH".equals(lastMessageType) && !tracked; }

    FlightSnapshot snapshot() {
        return new FlightSnapshot(
                key, facility, identity, lastMessageType, receivedAt, messageTime, jms,
                sfdpsGufi, uuidGufi, flightPlanId, flightState, aircraftIdentification,
                previousAircraftIdentification, computerId, siteSpecificPlanId, aircraftType,
                departurePoint, arrivalPoint, beaconCode, assignedAltitude, interimAltitude,
                track, control, fourthLine, pointOut, handoff, hold, route, nameValues, tracked
        );
    }

    private static String upper(String value) { return value == null ? null : value.toUpperCase(Locale.ROOT); }
    private static String choose(String incoming, String current) { return incoming == null || incoming.isBlank() ? current : incoming; }
    private static <T> T choose(T incoming, T current) { return incoming == null ? current : incoming; }
    private static Instant first(Instant incoming, Instant current) { return incoming == null ? current : incoming; }
}
