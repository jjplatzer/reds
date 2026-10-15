package ingest;

import messages.FlightMessage;
import messages.JmsMetadata;
import messages.SfdpsChannel;
import messages.SfdpsEvent;
import org.w3c.dom.Document;
import org.w3c.dom.Element;

import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/** Parser for SFDPS Flight FIXM / BATCH_TH_FIXM deliveries. */
final class FlightFixmParser {
    private FlightFixmParser() {}

    static List<SfdpsEvent> parse(Document doc, JmsMetadata jms, Instant receivedAt) {
        Element root = doc.getDocumentElement();
        if (root == null) return List.of();

        List<Element> flights = XmlSupport.descendants(root, "flight");
        List<SfdpsEvent> out = new ArrayList<>(flights.size());
        for (Element flight : flights) {
            Element message = XmlSupport.ancestor(flight, "message");
            Element metadata = message == null ? null : XmlSupport.firstDescendant(message, "metadata");

            String rawType = XmlSupport.firstNonBlank(
                    XmlSupport.attr(metadata, "messageType"),
                    jms == null ? null : jms.messageType()
            );
            String messageType = normalizeMessageType(rawType);
            String facility = XmlSupport.firstNonBlank(
                    jms == null ? null : jms.sourceFacility(),
                    XmlSupport.attr(flight, "centre"),
                    XmlSupport.attr(XmlSupport.firstDescendant(metadata, "provenance"), "center"),
                    XmlSupport.attr(XmlSupport.firstDescendant(metadata, "provenance"), "centre")
            );
            if (facility != null) facility = facility.toUpperCase(Locale.ROOT);

            Map<String, String> nameValues = XmlSupport.nameValueMap(flight);
            Element identification = XmlSupport.firstDescendant(flight, "flightIdentification");
            Element previousIdentification = XmlSupport.firstDescendant(flight, "flightIdentificationPrevious");
            Element flightPlan = XmlSupport.firstDescendant(flight, "flightPlan");
            Element aircraftDescription = XmlSupport.firstDescendant(flight, "aircraftDescription");
            Element aircraftContainer = aircraftDescription != null
                    ? aircraftDescription
                    : XmlSupport.firstDescendant(flight, "aircraft");
            Element aircraftType = XmlSupport.firstDescendant(aircraftContainer, "aircraftType");
            Element enRoute = XmlSupport.firstDescendant(flight, "enRoute");

            FlightMessage parsed = new FlightMessage(
                    messageType,
                    facility,
                    XmlSupport.firstInstant(
                            XmlSupport.parseInstant(XmlSupport.text(XmlSupport.firstDescendant(message, "messageDateTime"))),
                            jms == null ? null : jms.fdpsReceivedAt()
                    ),
                    findNameValue(nameValues, "FDPS_GUFI", "SFDPS_GUFI", "fdpsGufi"),
                    XmlSupport.text(XmlSupport.firstDescendant(flight, "gufi")),
                    XmlSupport.attr(flightPlan, "identifier"),
                    parseFlightState(flight),
                    XmlSupport.attr(identification, "aircraftIdentification"),
                    XmlSupport.attr(previousIdentification, "aircraftIdentification"),
                    XmlSupport.attr(identification, "computerId"),
                    XmlSupport.attr(identification, "siteSpecificPlanId"),
                    XmlSupport.firstNonBlank(
                            XmlSupport.attr(XmlSupport.firstDescendant(aircraftType, "icaoModelIdentifier"), "value"),
                            XmlSupport.text(XmlSupport.firstDescendant(aircraftType, "icaoModelIdentifier")),
                            XmlSupport.attr(XmlSupport.firstDescendant(aircraftType, "type"), "icaoAircraftTypeDesignator"),
                            XmlSupport.text(XmlSupport.firstDescendant(aircraftType, "type"))
                    ),
                    parseDeparturePoint(flight),
                    parseArrivalPoint(flight),
                    parseBeacon(enRoute),
                    parseAssignedAltitude(flight),
                    parseInterimAltitude(flight),
                    parseTrack(enRoute),
                    parseControl(flight, enRoute),
                    parseFourthLine(enRoute),
                    parsePointOut(enRoute),
                    parseHandoff(enRoute),
                    parseHold(flight, enRoute),
                    parseRoute(flight),
                    nameValues
            );

            out.add(new SfdpsEvent(
                    SfdpsChannel.FLIGHT_FIXM,
                    facility,
                    messageType,
                    receivedAt,
                    jms,
                    parsed,
                    null,
                    null,
                    null
            ));
        }
        return List.copyOf(out);
    }

    private static String normalizeMessageType(String raw) {
        raw = XmlSupport.blankToNull(raw);
        if (raw == null) return "UNKNOWN";
        raw = raw.toUpperCase(Locale.ROOT);
        if (raw.startsWith("BATCH_")) raw = raw.substring("BATCH_".length());
        if (raw.endsWith("_FIXM")) raw = raw.substring(0, raw.length() - "_FIXM".length());
        return raw;
    }

    private static String findNameValue(Map<String, String> values, String... names) {
        for (String name : names) {
            for (Map.Entry<String, String> e : values.entrySet()) {
                if (name.equalsIgnoreCase(e.getKey())) return e.getValue();
            }
        }
        return null;
    }

    private static String parseFlightState(Element flight) {
        Element status = XmlSupport.firstDescendant(flight, "flightStatus");
        return XmlSupport.firstNonBlank(
                XmlSupport.attr(status, "fdpsFlightStatus"),
                XmlSupport.attr(status, "flightStatus"),
                XmlSupport.text(status)
        );
    }

    private static String parseDeparturePoint(Element flight) {
        Element departure = XmlSupport.firstDescendant(flight, "departure");
        if (departure == null) return null;
        Element aerodrome = XmlSupport.firstDescendant(departure, "departureAerodrome");
        if (aerodrome == null) aerodrome = XmlSupport.firstDescendant(departure, "aerodrome");
        return XmlSupport.firstNonBlank(
                XmlSupport.attr(departure, "departurePoint"),
                XmlSupport.attr(aerodrome, "code"),
                XmlSupport.attr(aerodrome, "locationIndicator"),
                XmlSupport.attr(aerodrome, "name")
        );
    }

    private static String parseArrivalPoint(Element flight) {
        Element arrival = XmlSupport.firstDescendant(flight, "arrival");
        if (arrival == null) arrival = XmlSupport.firstDescendant(flight, "destination");
        if (arrival == null) return null;
        Element aerodrome = XmlSupport.firstDescendant(arrival, "arrivalAerodrome");
        if (aerodrome == null) aerodrome = XmlSupport.firstDescendant(arrival, "aerodrome");
        return XmlSupport.firstNonBlank(
                XmlSupport.attr(arrival, "arrivalPoint"),
                XmlSupport.attr(aerodrome, "code"),
                XmlSupport.attr(aerodrome, "locationIndicator"),
                XmlSupport.attr(aerodrome, "name")
        );
    }

    private static String parseBeacon(Element enRoute) {
        Element assignment = XmlSupport.firstDescendant(enRoute, "beaconCodeAssignment");
        return XmlSupport.firstNonBlank(
                XmlSupport.text(XmlSupport.firstDescendant(assignment, "currentBeaconCode")),
                XmlSupport.text(XmlSupport.firstDescendant(enRoute, "currentSsrCode"))
        );
    }

    private static FlightMessage.Altitude parseAssignedAltitude(Element flight) {
        Element assigned = XmlSupport.firstDescendant(flight, "assignedAltitude");
        if (assigned == null) return null;

        Element simple = XmlSupport.firstDescendant(assigned, "simple");
        if (simple != null) return altitude("simple", simple, null, null);
        Element block = XmlSupport.firstDescendant(assigned, "block");
        if (block != null) {
            Element below = XmlSupport.firstDescendant(block, "below");
            Element above = XmlSupport.firstDescendant(block, "above");
            return new FlightMessage.Altitude(
                    "block",
                    null,
                    XmlSupport.parseDouble(XmlSupport.text(below)),
                    XmlSupport.parseDouble(XmlSupport.text(above)),
                    XmlSupport.text(block),
                    XmlSupport.firstNonBlank(XmlSupport.attr(below, "uom"), XmlSupport.attr(above, "uom"))
            );
        }
        for (String kind : List.of("above", "vfrPlus", "vfrOnTopPlus", "vfr", "vfrOnTop")) {
            Element value = XmlSupport.firstDescendant(assigned, kind);
            if (value != null) return altitude(kind, value, null, null);
        }
        Element fix = XmlSupport.firstDescendant(assigned, "altFixAlt");
        if (fix != null) {
            Element pre = XmlSupport.firstDescendant(fix, "pre");
            Element post = XmlSupport.firstDescendant(fix, "post");
            return new FlightMessage.Altitude(
                    "altFixAlt",
                    null,
                    XmlSupport.parseDouble(XmlSupport.text(pre)),
                    XmlSupport.parseDouble(XmlSupport.text(post)),
                    XmlSupport.text(fix),
                    XmlSupport.firstNonBlank(XmlSupport.attr(pre, "uom"), XmlSupport.attr(post, "uom"))
            );
        }
        return new FlightMessage.Altitude("unknown", XmlSupport.parseDouble(XmlSupport.text(assigned)), null, null,
                XmlSupport.text(assigned), XmlSupport.attr(assigned, "uom"));
    }

    private static FlightMessage.Altitude parseInterimAltitude(Element flight) {
        Element interim = XmlSupport.firstDescendant(flight, "interimAltitude");
        if (interim == null) return null;
        String nil = XmlSupport.attr(interim, "nil");
        if (Boolean.TRUE.equals(XmlSupport.parseBoolean(nil)) || XmlSupport.text(interim) == null) return null;
        return altitude("interim", interim, null, null);
    }

    private static FlightMessage.Altitude altitude(String kind, Element value, Double lower, Double upper) {
        return new FlightMessage.Altitude(
                kind,
                XmlSupport.parseDouble(XmlSupport.text(value)),
                lower,
                upper,
                XmlSupport.text(value),
                XmlSupport.attr(value, "uom")
        );
    }

    private static FlightMessage.Track parseTrack(Element enRoute) {
        if (enRoute == null) return null;
        // SFDPS 2.1.4 maps TH to enRoute/position. Newer NAS-extension
        // producers may use enRoute/aircraftPosition, so retain both shapes.
        Element aircraftPosition = XmlSupport.firstDescendant(enRoute, "aircraftPosition");
        Element position = aircraftPosition != null
                ? aircraftPosition
                : XmlSupport.firstDescendant(enRoute, "position");
        if (position == null) return null;

        // The SFDPS 2.1.4 shape is position/position/location/pos. The newer
        // shape is aircraftPosition/position/.../pos. Do not fall through into
        // targetPosition when selecting the actual track point.
        Element positionPoint = XmlSupport.child(position, "position");
        Element location = XmlSupport.firstDescendant(positionPoint != null ? positionPoint : position, "location");
        Element posElement = location == null
                ? XmlSupport.firstDescendant(positionPoint != null ? positionPoint : position, "pos")
                : XmlSupport.firstDescendant(location, "pos");
        double[] pos = XmlSupport.parsePos(XmlSupport.text(posElement));
        Element altitude = directOrDescendant(position, "altitude");

        Element targetPosition = XmlSupport.firstDescendant(position, "targetPosition");
        double[] targetPos = XmlSupport.parsePos(XmlSupport.text(XmlSupport.firstDescendant(targetPosition, "pos")));
        Element targetAltitude = XmlSupport.firstDescendant(position, "targetAltitude");

        Element velocity = XmlSupport.firstDescendant(position, "trackVelocity");
        Element vx = XmlSupport.firstDescendant(velocity, "x");
        Element vy = XmlSupport.firstDescendant(velocity, "y");
        Element actualSpeed = XmlSupport.firstDescendant(position, "actualSpeed");
        Element surveillanceSpeed = XmlSupport.firstDescendant(actualSpeed, "surveillance");
        Element heading = XmlSupport.firstDescendant(position, "heading");

        return new FlightMessage.Track(
                XmlSupport.parseInstant(XmlSupport.firstNonBlank(XmlSupport.attr(position, "positionTime"), XmlSupport.text(XmlSupport.firstDescendant(position, "positionTime")))),
                pos == null ? null : pos[0],
                pos == null ? null : pos[1],
                XmlSupport.parseDouble(XmlSupport.text(altitude)),
                XmlSupport.attr(altitude, "uom"),
                targetPos == null ? null : targetPos[0],
                targetPos == null ? null : targetPos[1],
                XmlSupport.parseDouble(XmlSupport.text(targetAltitude)),
                XmlSupport.attr(targetAltitude, "uom"),
                parseInvalid(XmlSupport.attr(targetAltitude, "invalid")),
                XmlSupport.parseInstant(XmlSupport.firstNonBlank(XmlSupport.attr(position, "targetPositionTime"), XmlSupport.text(XmlSupport.firstDescendant(position, "targetPositionTime")))),
                XmlSupport.parseDouble(XmlSupport.text(vx)),
                XmlSupport.parseDouble(XmlSupport.text(vy)),
                XmlSupport.firstNonBlank(XmlSupport.attr(vx, "uom"), XmlSupport.attr(vy, "uom")),
                XmlSupport.parseDouble(XmlSupport.text(surveillanceSpeed)),
                XmlSupport.attr(surveillanceSpeed, "uom"),
                XmlSupport.parseDouble(XmlSupport.text(heading)),
                XmlSupport.attr(heading, "uom"),
                XmlSupport.attr(position, "coastIndicator"),
                XmlSupport.attr(position, "reportSource")
        );
    }

    private static Element directOrDescendant(Element parent, String local) {
        Element direct = XmlSupport.child(parent, local);
        return direct != null ? direct : XmlSupport.firstDescendant(parent, local);
    }

    private static Boolean parseInvalid(String value) {
        value = XmlSupport.blankToNull(value);
        if (value == null) return null;
        if ("INV".equalsIgnoreCase(value)) return true;
        return XmlSupport.parseBoolean(value);
    }

    private static FlightMessage.Control parseControl(Element flight, Element enRoute) {
        Element controlling = XmlSupport.firstDescendant(flight, "controllingUnit");
        Element handoff = XmlSupport.firstDescendant(enRoute, "handoff");
        Element receiving = XmlSupport.firstDescendant(handoff, "receivingUnit");
        Element accepting = XmlSupport.firstDescendant(handoff, "acceptingUnit");
        if (controlling == null && receiving == null && accepting == null) return null;
        return new FlightMessage.Control(
                unit(controlling), sector(controlling),
                unit(receiving), sector(receiving),
                unit(accepting), sector(accepting)
        );
    }

    private static FlightMessage.FourthLine parseFourthLine(Element enRoute) {
        Element cleared = XmlSupport.firstDescendant(enRoute, "cleared");
        if (cleared == null) return null;
        String heading = XmlSupport.firstNonBlank(XmlSupport.attr(cleared, "clearanceHeading"), XmlSupport.text(XmlSupport.firstDescendant(cleared, "clearanceHeading")));
        String speed = XmlSupport.firstNonBlank(XmlSupport.attr(cleared, "clearanceSpeed"), XmlSupport.text(XmlSupport.firstDescendant(cleared, "clearanceSpeed")));
        String text = XmlSupport.firstNonBlank(XmlSupport.attr(cleared, "clearanceText"), XmlSupport.text(XmlSupport.firstDescendant(cleared, "clearanceText")));
        if (heading == null && speed == null && text == null) return null;
        return new FlightMessage.FourthLine(heading, speed, text);
    }

    private static FlightMessage.PointOut parsePointOut(Element enRoute) {
        Element pointout = XmlSupport.firstDescendant(enRoute, "pointout");
        if (pointout == null) return null;
        Element origin = XmlSupport.firstDescendant(pointout, "originatingUnit");
        Element receiving = XmlSupport.firstDescendant(pointout, "receivingUnit");
        return new FlightMessage.PointOut(unit(origin), sector(origin), unit(receiving), sector(receiving));
    }

    private static FlightMessage.Handoff parseHandoff(Element enRoute) {
        Element handoff = XmlSupport.firstDescendant(enRoute, "handoff");
        if (handoff == null) return null;
        Element transferring = XmlSupport.firstDescendant(handoff, "transferringUnit");
        Element receiving = XmlSupport.firstDescendant(handoff, "receivingUnit");
        Element accepting = XmlSupport.firstDescendant(handoff, "acceptingUnit");
        return new FlightMessage.Handoff(
                XmlSupport.attr(handoff, "event"),
                unit(transferring), sector(transferring),
                unit(receiving), sector(receiving),
                unit(accepting), sector(accepting)
        );
    }

    private static FlightMessage.Hold parseHold(Element flight, Element enRoute) {
        // SFDPS 2.1.4 maps HH holdDataFix_21a to
        // flight/agreed/route/holdFix and holdDataTime_21d to
        // flight/enRoute/expectedFurtherClearanceTime/@time. Some newer FIXM
        // variants wrap this in an explicit hold element, so support both.
        Element agreed = XmlSupport.firstDescendant(flight, "agreed");
        Element route = XmlSupport.firstDescendant(agreed, "route");
        Element holdFix = XmlSupport.firstDescendant(route, "holdFix");
        Element efc = XmlSupport.firstDescendant(enRoute, "expectedFurtherClearanceTime");

        Element hold = XmlSupport.firstDescendant(enRoute, "hold");
        Element status = XmlSupport.firstDescendant(flight, "status");
        Boolean airborne = XmlSupport.parseBoolean(XmlSupport.attr(status, "airborneHold"));
        String fix = XmlSupport.firstNonBlank(
                pointLabel(holdFix),
                XmlSupport.attr(hold, "fix"),
                pointLabel(XmlSupport.firstDescendant(hold, "fix"))
        );
        String time = XmlSupport.firstNonBlank(
                XmlSupport.attr(efc, "time"),
                XmlSupport.text(efc),
                XmlSupport.attr(hold, "expectFurtherClearanceTime"),
                XmlSupport.text(XmlSupport.firstDescendant(hold, "expectFurtherClearanceTime"))
        );
        if (fix == null && time == null && hold == null && airborne == null) return null;
        return new FlightMessage.Hold(fix, time, airborne);
    }

    private static List<FlightMessage.RoutePoint> parseRoute(Element flight) {
        Element agreed = XmlSupport.firstDescendant(flight, "agreed");
        Element route = XmlSupport.firstDescendant(agreed, "route");
        Element expanded = XmlSupport.firstDescendant(route, "expandedRoute");
        if (expanded == null) return List.of();

        List<FlightMessage.RoutePoint> out = new ArrayList<>();
        for (Element routePoint : XmlSupport.descendants(expanded, "routePoint")) {
            Element point = XmlSupport.firstDescendant(routePoint, "point");
            String designator = pointLabel(point);
            Instant estimatedTime = XmlSupport.parseInstant(XmlSupport.firstNonBlank(
                    XmlSupport.attr(routePoint, "estimatedTime"),
                    XmlSupport.text(XmlSupport.firstDescendant(routePoint, "estimatedTime"))
            ));
            if (designator != null || estimatedTime != null) {
                out.add(new FlightMessage.RoutePoint(designator, estimatedTime));
            }
        }
        return List.copyOf(out);
    }

    private static String pointLabel(Element point) {
        if (point == null) return null;
        String direct = XmlSupport.firstNonBlank(
                XmlSupport.attr(point, "designator"),
                XmlSupport.attr(point, "locationIndicator"),
                XmlSupport.attr(point, "name"),
                XmlSupport.text(XmlSupport.child(point, "designator")),
                XmlSupport.text(XmlSupport.child(point, "name"))
        );
        if (direct != null) return direct;

        for (String name : List.of("designatedPoint", "navaid", "aerodrome", "position", "location")) {
            Element nested = XmlSupport.firstDescendant(point, name);
            if (nested == null || nested == point) continue;
            String value = XmlSupport.firstNonBlank(
                    XmlSupport.attr(nested, "designator"),
                    XmlSupport.attr(nested, "locationIndicator"),
                    XmlSupport.attr(nested, "name"),
                    XmlSupport.text(XmlSupport.firstDescendant(nested, "designator")),
                    XmlSupport.text(XmlSupport.firstDescendant(nested, "pos"))
            );
            if (value != null) return value;
        }

        // A simple FIXM point may itself just contain its designator text.
        if (XmlSupport.children(point, null).isEmpty()) return XmlSupport.text(point);
        return null;
    }

    private static String unit(Element element) {
        return XmlSupport.firstNonBlank(XmlSupport.attr(element, "unitIdentifier"), XmlSupport.attr(element, "facilityIdentifier"));
    }

    private static String sector(Element element) {
        return XmlSupport.attr(element, "sectorIdentifier");
    }
}
