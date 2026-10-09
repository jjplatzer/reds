package wire;

import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;
import messages.AirspaceMessage;
import messages.FlightMessage;
import messages.GeneralMessage;
import messages.JmsMetadata;
import messages.StatusMessage;
import store.FlightSnapshot;

import java.time.Instant;
import java.util.List;
import java.util.Map;

/** Stable protocol-v1 JSON for the REDS SFDPS service. */
public final class SfdpsWire {
    public static final int PROTOCOL = 1;
    public static final String FEED = "sfdps";

    private SfdpsWire() {}

    public static JsonObject snapshot(
            long revision,
            Instant generatedAt,
            String facility,
            List<FlightSnapshot> flights,
            List<AirspaceMessage> airspace,
            List<GeneralMessage> general,
            List<StatusMessage> status
    ) {
        JsonArray flightArray = new JsonArray();
        for (FlightSnapshot value : flights) flightArray.add(flight(value));
        JsonArray airspaceArray = new JsonArray();
        for (AirspaceMessage value : airspace) airspaceArray.add(airspace(value));
        JsonArray generalArray = new JsonArray();
        for (GeneralMessage value : general) generalArray.add(general(value));
        JsonArray statusArray = new JsonArray();
        for (StatusMessage value : status) statusArray.add(status(value));

        return envelope("snapshot", revision, facility, null)
                .put("generatedAt", generatedAt.toString())
                .put("flights", flightArray)
                .put("airspace", airspaceArray)
                .put("general", generalArray)
                .put("status", statusArray);
    }

    public static JsonObject flightUpdate(long revision, String facility, String messageType, FlightSnapshot value) {
        return envelope("update", revision, facility, messageType)
                .put("channel", "flight")
                .put("flight", flight(value));
    }

    public static JsonObject flightRemove(long revision, String facility, String key) {
        return envelope("remove", revision, facility, null)
                .put("channel", "flight")
                .put("key", key);
    }

    public static JsonObject airspaceUpdate(long revision, String facility, String messageType, AirspaceMessage value) {
        return envelope("update", revision, facility, messageType)
                .put("channel", "airspace")
                .put("airspaceEntity", airspace(value));
    }

    public static JsonObject generalUpdate(long revision, String facility, String messageType, GeneralMessage value) {
        return envelope("update", revision, facility, messageType)
                .put("channel", "general")
                .put("generalMessage", general(value));
    }

    public static JsonObject statusUpdate(long revision, String facility, String messageType, StatusMessage value) {
        return envelope("update", revision, facility, messageType)
                .put("channel", "status")
                .put("statusMessage", status(value));
    }

    private static JsonObject envelope(String type, long revision, String facility, String messageType) {
        JsonObject out = new JsonObject()
                .put("type", type)
                .put("protocol", PROTOCOL)
                .put("feed", FEED)
                .put("revision", revision);
        put(out, "facility", facility);
        put(out, "messageType", messageType);
        return out;
    }

    private static JsonObject flight(FlightSnapshot value) {
        JsonObject out = new JsonObject()
                .put("key", value.key())
                .put("facility", value.facility())
                .put("identity", value.identity())
                .put("tracked", value.tracked());
        put(out, "lastMessageType", value.lastMessageType());
        putInstant(out, "receivedAt", value.receivedAt());
        putInstant(out, "messageTime", value.messageTime());
        if (value.jms() != null) out.put("jms", jms(value.jms()));
        put(out, "sfdpsGufi", value.sfdpsGufi());
        put(out, "uuidGufi", value.uuidGufi());
        put(out, "flightPlanId", value.flightPlanId());
        put(out, "flightState", value.flightState());
        put(out, "aircraftIdentification", value.aircraftIdentification());
        put(out, "previousAircraftIdentification", value.previousAircraftIdentification());
        put(out, "computerId", value.computerId());
        put(out, "siteSpecificPlanId", value.siteSpecificPlanId());
        put(out, "aircraftType", value.aircraftType());
        put(out, "departurePoint", value.departurePoint());
        put(out, "arrivalPoint", value.arrivalPoint());
        put(out, "beaconCode", value.beaconCode());
        if (value.assignedAltitude() != null) out.put("assignedAltitude", altitude(value.assignedAltitude()));
        if (value.interimAltitude() != null) out.put("interimAltitude", altitude(value.interimAltitude()));
        if (value.track() != null) out.put("track", track(value.track()));
        if (value.control() != null) out.put("control", control(value.control()));
        if (value.fourthLine() != null) out.put("fourthLine", fourthLine(value.fourthLine()));
        if (value.pointOut() != null) out.put("pointOut", pointOut(value.pointOut()));
        if (value.handoff() != null) out.put("handoff", handoff(value.handoff()));
        if (value.hold() != null) out.put("hold", hold(value.hold()));
        if (!value.route().isEmpty()) {
            JsonArray route = new JsonArray();
            for (FlightMessage.RoutePoint point : value.route()) {
                JsonObject item = new JsonObject();
                put(item, "designator", point.designator());
                putInstant(item, "estimatedTime", point.estimatedTime());
                route.add(item);
            }
            out.put("route", route);
        }
        if (!value.nameValues().isEmpty()) out.put("nameValues", stringMap(value.nameValues()));
        return out;
    }

    private static JsonObject jms(JmsMetadata value) {
        JsonObject out = new JsonObject();
        put(out, "sourceSystem", value.sourceSystem());
        put(out, "sourceFacility", value.sourceFacility());
        put(out, "messageType", value.messageType());
        put(out, "flightOperator", value.flightOperator());
        put(out, "flightId", value.flightId());
        put(out, "origin", value.origin());
        put(out, "destination", value.destination());
        put(out, "oneMinuteFrequency", value.oneMinuteFrequency());
        put(out, "sensitive", value.sensitive());
        put(out, "restricted", value.restricted());
        put(out, "authoritative", value.authoritative());
        putInstant(out, "fdpsReceivedAt", value.fdpsReceivedAt());
        putInstant(out, "fdpsSentAt", value.fdpsSentAt());
        put(out, "sequenceNumber", value.sequenceNumber());
        put(out, "dataType", value.dataType());
        return out;
    }

    private static JsonObject altitude(FlightMessage.Altitude value) {
        JsonObject out = new JsonObject();
        put(out, "kind", value.kind());
        put(out, "value", value.value());
        put(out, "lower", value.lower());
        put(out, "upper", value.upper());
        put(out, "raw", value.raw());
        put(out, "uom", value.uom());
        return out;
    }

    private static JsonObject track(FlightMessage.Track value) {
        JsonObject out = new JsonObject();
        putInstant(out, "positionTime", value.positionTime());
        put(out, "lat", value.lat());
        put(out, "lon", value.lon());
        put(out, "reportedAltitude", value.reportedAltitude());
        put(out, "reportedAltitudeUom", value.reportedAltitudeUom());
        put(out, "targetLat", value.targetLat());
        put(out, "targetLon", value.targetLon());
        put(out, "targetAltitude", value.targetAltitude());
        put(out, "targetAltitudeUom", value.targetAltitudeUom());
        put(out, "targetAltitudeInvalid", value.targetAltitudeInvalid());
        putInstant(out, "targetPositionTime", value.targetPositionTime());
        put(out, "velocityX", value.velocityX());
        put(out, "velocityY", value.velocityY());
        put(out, "velocityUom", value.velocityUom());
        put(out, "groundSpeed", value.groundSpeed());
        put(out, "groundSpeedUom", value.groundSpeedUom());
        put(out, "heading", value.heading());
        put(out, "headingUom", value.headingUom());
        put(out, "coastIndicator", value.coastIndicator());
        put(out, "reportSource", value.reportSource());
        return out;
    }

    private static JsonObject control(FlightMessage.Control value) {
        JsonObject out = new JsonObject();
        put(out, "controllingFacility", value.controllingFacility());
        put(out, "controllingSector", value.controllingSector());
        put(out, "receivingFacility", value.receivingFacility());
        put(out, "receivingSector", value.receivingSector());
        put(out, "acceptingFacility", value.acceptingFacility());
        put(out, "acceptingSector", value.acceptingSector());
        return out;
    }

    private static JsonObject fourthLine(FlightMessage.FourthLine value) {
        JsonObject out = new JsonObject();
        put(out, "heading", value.heading());
        put(out, "speed", value.speed());
        put(out, "text", value.text());
        return out;
    }

    private static JsonObject pointOut(FlightMessage.PointOut value) {
        JsonObject out = new JsonObject();
        put(out, "originatingFacility", value.originatingFacility());
        put(out, "originatingSector", value.originatingSector());
        put(out, "receivingFacility", value.receivingFacility());
        put(out, "receivingSector", value.receivingSector());
        return out;
    }

    private static JsonObject handoff(FlightMessage.Handoff value) {
        JsonObject out = new JsonObject();
        put(out, "event", value.event());
        put(out, "transferringFacility", value.transferringFacility());
        put(out, "transferringSector", value.transferringSector());
        put(out, "receivingFacility", value.receivingFacility());
        put(out, "receivingSector", value.receivingSector());
        put(out, "acceptingFacility", value.acceptingFacility());
        put(out, "acceptingSector", value.acceptingSector());
        return out;
    }

    private static JsonObject hold(FlightMessage.Hold value) {
        JsonObject out = new JsonObject();
        put(out, "fix", value.fix());
        put(out, "expectFurtherClearanceTime", value.expectFurtherClearanceTime());
        put(out, "airborne", value.airborne());
        return out;
    }

    private static JsonObject airspace(AirspaceMessage value) {
        JsonObject out = new JsonObject();
        put(out, "messageType", value.messageType());
        put(out, "facility", value.facility());
        putInstant(out, "receivedAt", value.receivedAt());
        if (value.jms() != null) out.put("jms", jms(value.jms()));
        put(out, "kind", value.kind() == null ? null : value.kind().name().toLowerCase());
        put(out, "identifier", value.identifier());
        put(out, "status", value.status());
        if (!value.favNumbers().isEmpty()) out.put("favNumbers", new JsonArray(value.favNumbers()));
        put(out, "scheduleId", value.scheduleId());
        put(out, "scheduleType", value.scheduleType());
        put(out, "activationTimeRaw", value.activationTimeRaw());
        put(out, "deactivationTimeRaw", value.deactivationTimeRaw());
        putInstant(out, "activationTime", value.activationTime());
        putInstant(out, "deactivationTime", value.deactivationTime());
        put(out, "lowerAltitude", value.lowerAltitude());
        put(out, "lowerAltitudeUom", value.lowerAltitudeUom());
        put(out, "upperAltitude", value.upperAltitude());
        put(out, "upperAltitudeUom", value.upperAltitudeUom());
        if (!value.fields().isEmpty()) out.put("fields", stringMap(value.fields()));
        return out;
    }

    private static JsonObject general(GeneralMessage value) {
        JsonObject out = new JsonObject();
        put(out, "messageType", value.messageType());
        put(out, "facility", value.facility());
        putInstant(out, "receivedAt", value.receivedAt());
        putInstant(out, "messageTime", value.messageTime());
        if (value.jms() != null) out.put("jms", jms(value.jms()));
        put(out, "flightId", value.flightId());
        put(out, "computerId", value.computerId());
        put(out, "siteSpecificPlanId", value.siteSpecificPlanId());
        put(out, "remarks", value.remarks());
        put(out, "remarkType", value.remarkType());
        put(out, "interimAltitude", value.interimAltitude());
        put(out, "holdFix", value.holdFix());
        put(out, "holdTime", value.holdTime());
        put(out, "fav", value.fav());
        if (!value.fields().isEmpty()) out.put("fields", stringMap(value.fields()));
        return out;
    }

    private static JsonObject status(StatusMessage value) {
        JsonObject out = new JsonObject();
        put(out, "center", value.center());
        put(out, "classification", value.classification());
        putInstant(out, "receivedAt", value.receivedAt());
        putInstant(out, "time", value.time());
        if (value.jms() != null) out.put("jms", jms(value.jms()));
        put(out, "statusType", value.statusType());
        put(out, "source", value.source());
        if (!value.artccStates().isEmpty()) out.put("artccStates", stringMap(value.artccStates()));
        if (!value.softwareStates().isEmpty()) out.put("softwareStates", stringMap(value.softwareStates()));
        put(out, "process", value.process());
        put(out, "details", value.details());
        if (!value.fields().isEmpty()) out.put("fields", stringMap(value.fields()));
        return out;
    }

    private static JsonObject stringMap(Map<String, String> values) {
        JsonObject out = new JsonObject();
        for (Map.Entry<String, String> entry : values.entrySet()) out.put(entry.getKey(), entry.getValue());
        return out;
    }

    private static void put(JsonObject out, String key, Object value) { if (value != null) out.put(key, value); }
    private static void putInstant(JsonObject out, String key, Instant value) { if (value != null) out.put(key, value.toString()); }
}
