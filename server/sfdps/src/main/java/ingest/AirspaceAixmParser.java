package ingest;

import messages.AirspaceMessage;
import messages.JmsMetadata;
import messages.SfdpsChannel;
import messages.SfdpsEvent;
import org.w3c.dom.Document;
import org.w3c.dom.Element;

import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

/** Parser for the SFDPS Airspace AIXM subscription (HR/SH/SU). */
final class AirspaceAixmParser {
    private AirspaceAixmParser() {}

    static List<SfdpsEvent> parse(Document doc, JmsMetadata jms, Instant receivedAt) {
        Element root = doc.getDocumentElement();
        if (root == null) return List.of();
        String rawType = jms == null ? null : jms.messageType();
        String messageType = normalizeMessageType(rawType);
        String facility = jms == null ? null : upper(jms.sourceFacility());

        List<SfdpsEvent> out = new ArrayList<>();
        for (Element route : XmlSupport.descendants(root, "RouteSegment")) {
            AirspaceMessage entity = parseRoute(messageType, facility, receivedAt, jms, route);
            out.add(event(entity, facility, messageType, receivedAt, jms));
        }
        for (Element airspace : XmlSupport.descendants(root, "Airspace")) {
            AirspaceMessage entity = parseAirspace(messageType, facility, receivedAt, jms, airspace);
            out.add(event(entity, facility, messageType, receivedAt, jms));
        }
        return List.copyOf(out);
    }

    private static AirspaceMessage parseRoute(String messageType, String facility, Instant receivedAt, JmsMetadata jms, Element route) {
        String identifier = XmlSupport.firstNonBlank(
                XmlSupport.text(XmlSupport.firstDescendant(route, "name")),
                XmlSupport.text(XmlSupport.firstDescendant(route, "designator")),
                XmlSupport.attr(route, "id")
        );
        String status = XmlSupport.text(XmlSupport.firstDescendant(route, "status"));
        return new AirspaceMessage(messageType, facility, receivedAt, jms, AirspaceMessage.Kind.ROUTE, identifier,
                normalizeRouteStatus(status), List.of(), null, null, null, null, null, null,
                null, null, null, null, XmlSupport.leafFields(route));
    }

    private static AirspaceMessage parseAirspace(String messageType, String facility, Instant receivedAt, JmsMetadata jms, Element airspace) {
        String type = XmlSupport.text(XmlSupport.firstDescendant(airspace, "type"));
        String identifier = XmlSupport.firstNonBlank(
                XmlSupport.text(XmlSupport.firstDescendant(airspace, "designator")),
                XmlSupport.text(XmlSupport.firstDescendant(airspace, "name")),
                XmlSupport.attr(airspace, "id")
        );
        AirspaceMessage.Kind kind = classify(messageType, type);
        String status = normalizeAirspaceStatus(kind, XmlSupport.text(XmlSupport.firstDescendant(airspace, "status")));

        List<String> favs = new ArrayList<>();
        for (Element fav : XmlSupport.descendants(airspace, "FAVNumber")) {
            String value = XmlSupport.text(fav);
            if (value != null) favs.add(value);
        }

        Element schedule = findScheduleExtension(airspace);
        String scheduleId = schedule == null ? null : XmlSupport.text(XmlSupport.firstDescendant(schedule, "ID"));
        String scheduleType = schedule == null ? null : XmlSupport.text(XmlSupport.firstDescendant(schedule, "type"));
        String activationRaw = schedule == null ? null : XmlSupport.text(XmlSupport.firstDescendant(schedule, "activationDateTime"));
        String deactivationRaw = schedule == null ? null : XmlSupport.text(XmlSupport.firstDescendant(schedule, "deactivationDateTime"));
        Instant activation = XmlSupport.parseInstant(activationRaw);
        Instant deactivation = XmlSupport.parseInstant(deactivationRaw);

        Element lowElement = firstFromAny(airspace, "lowerLimit", "lowerAltitude", "saaLowAlt");
        Element highElement = firstFromAny(airspace, "upperLimit", "upperAltitude", "saaHighAlt");
        Double low = XmlSupport.parseDouble(XmlSupport.text(lowElement));
        Double high = XmlSupport.parseDouble(XmlSupport.text(highElement));

        return new AirspaceMessage(messageType, facility, receivedAt, jms, kind, identifier, status, favs,
                scheduleId, scheduleType, activationRaw, deactivationRaw, activation, deactivation,
                low, XmlSupport.attr(lowElement, "uom"), high, XmlSupport.attr(highElement, "uom"),
                XmlSupport.leafFields(airspace));
    }

    private static Element findScheduleExtension(Element root) {
        for (Element element : XmlSupport.descendants(root, "SAAScheduleExtension")) return element;
        for (Element element : XmlSupport.descendants(root, "extension")) {
            if (XmlSupport.firstDescendant(element, "activationDateTime") != null) return element;
        }
        return null;
    }

    private static Element firstFromAny(Element root, String... names) {
        for (String name : names) {
            Element value = XmlSupport.firstDescendant(root, name);
            if (value != null) return value;
        }
        return null;
    }

    private static AirspaceMessage.Kind classify(String messageType, String type) {
        if (messageType.startsWith("HR") || "ROUTE".equalsIgnoreCase(type)) return AirspaceMessage.Kind.ROUTE;
        if (messageType.startsWith("SH") || "SECTOR".equalsIgnoreCase(type)) return AirspaceMessage.Kind.SECTOR;
        if (messageType.startsWith("SU") || "R".equalsIgnoreCase(type) || "RESTRICTED".equalsIgnoreCase(type)) return AirspaceMessage.Kind.SAA;
        return AirspaceMessage.Kind.UNKNOWN;
    }

    private static String normalizeRouteStatus(String status) {
        if (status == null) return null;
        if ("OPEN".equalsIgnoreCase(status)) return "ON";
        if ("CLSD".equalsIgnoreCase(status) || "CLOSED".equalsIgnoreCase(status)) return "OFF";
        return status;
    }

    private static String normalizeAirspaceStatus(AirspaceMessage.Kind kind, String status) {
        if (status == null) return null;
        if (kind != AirspaceMessage.Kind.SAA) return status;
        if ("ACTIVE".equalsIgnoreCase(status)) return "ON";
        if ("INACTIVE".equalsIgnoreCase(status)) return "OFF";
        if ("AVBL_FOR_ACTIVATION".equalsIgnoreCase(status)) return "SCHED";
        return status;
    }

    private static String normalizeMessageType(String raw) {
        raw = XmlSupport.blankToNull(raw);
        if (raw == null) return "UNKNOWN";
        raw = raw.toUpperCase(Locale.ROOT);
        if (raw.endsWith("_AIXM")) raw = raw.substring(0, raw.length() - 5);
        return raw;
    }

    private static String upper(String value) { return value == null ? null : value.toUpperCase(Locale.ROOT); }

    private static SfdpsEvent event(AirspaceMessage entity, String facility, String messageType,
                                    Instant receivedAt, JmsMetadata jms) {
        return new SfdpsEvent(SfdpsChannel.AIRSPACE_AIXM, facility, messageType, receivedAt,
                jms, null, entity, null, null);
    }
}
