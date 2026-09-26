package ingest;

import org.w3c.dom.Document;
import org.w3c.dom.Element;
import org.w3c.dom.NodeList;

import java.time.Instant;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

/** Namespace-aware parser for the STDDS TAIS SimpleXML TATrackAndFlightPlan message. */
public final class TaisParser {

    private static final String ROOT = "TATrackAndFlightPlan";

    private TaisParser() {}

    public static TaisBatch parse(Document doc, Instant receivedAt) {
        Element root = doc.getDocumentElement();
        if (root == null || !ROOT.equals(localName(root))) {
            return new TaisBatch(null, List.of());
        }

        // Current live TAIS v4 uses <src>. srcTracon/tracon are kept as
        // compatibility fallbacks because older STDDS documentation describes
        // those header names. The official FIXM mapping explicitly maps
        // /TATrackAndFlightPlan/src to FIXM provenance/tracon.
        String facility = firstNonBlank(
                text(child(root, "src")),
                text(child(root, "srcTracon")),
                text(child(root, "tracon"))
        );
        if (facility == null) {
            throw new IllegalArgumentException("TATrackAndFlightPlan has no source facility");
        }
        facility = facility.toUpperCase(Locale.ROOT);

        List<TaisObservation> observations = new ArrayList<>();
        NodeList nodes = root.getChildNodes();
        for (int i = 0; i < nodes.getLength(); i++) {
            if (!(nodes.item(i) instanceof Element record) || !"record".equals(localName(record))) continue;

            TaisObservation obs = parseRecord(facility, record, receivedAt);
            if (obs != null) observations.add(obs);
        }

        return new TaisBatch(facility, observations);
    }

    private static TaisObservation parseRecord(String facility, Element record, Instant receivedAt) {
        Element trackEl = child(record, "track");
        if (trackEl == null) return null;

        String trackNum = blankToNull(text(child(trackEl, "trackNum")));
        if (trackNum == null) return null;

        TaisObservation.RecordMeta meta = new TaisObservation.RecordMeta(
                parseLong(text(child(record, "recSeqNum"))),
                blankToNull(text(child(record, "recSrc"))),
                parseInteger(text(child(record, "recType"))),
                parseLong(text(child(record, "recSTARSTimestamp"))),
                parseInteger(text(child(record, "recSTARSSrcID"))),
                parseBoolean(text(child(record, "recSTARSTimeSync"))),
                parseInstant(text(child(record, "recSAFAReceiptTime"))),
                parseBoolean(text(child(record, "recSAFATimeSync")))
        );

        TaisObservation.Track track = new TaisObservation.Track(
                trackNum,
                parseInstant(text(child(trackEl, "mrtTime"))),
                blankToNull(text(child(trackEl, "status"))),
                normalizeHexAddress(text(child(trackEl, "acAddress"))),
                parseInteger(text(child(trackEl, "xPos"))),
                parseInteger(text(child(trackEl, "yPos"))),
                parseDouble(text(child(trackEl, "lat"))),
                parseDouble(text(child(trackEl, "lon"))),
                parseInteger(text(child(trackEl, "vVert"))),
                parseInteger(text(child(trackEl, "vx"))),
                parseInteger(text(child(trackEl, "vy"))),
                parseInteger(text(child(trackEl, "vVertRaw"))),
                parseInteger(text(child(trackEl, "vxRaw"))),
                parseInteger(text(child(trackEl, "vyRaw"))),
                parseBoolean(text(child(trackEl, "frozen"))),
                parseBoolean(text(child(trackEl, "new"))),
                parseBoolean(text(child(trackEl, "pseudo"))),
                parseBoolean(text(child(trackEl, "adsb"))),
                blankToNull(text(child(trackEl, "reportedBeaconCode"))),
                parseInteger(text(child(trackEl, "reportedAltitude")))
        );

        Element fpEl = child(record, "flightPlan");
        TaisObservation.FlightPlan flightPlan = fpEl == null ? null : new TaisObservation.FlightPlan(
                parseInteger(text(child(fpEl, "sfpn"))),
                blankToNull(text(child(fpEl, "ocr"))),
                parseInteger(text(child(fpEl, "rnav"))),
                blankToNull(text(child(fpEl, "scratchPad1"))),
                blankToNull(text(child(fpEl, "scratchPad2"))),
                blankToNull(text(child(fpEl, "cps"))),
                blankToNull(text(child(fpEl, "runway"))),
                blankToNull(text(child(fpEl, "assignedBeaconCode"))),
                parseInteger(text(child(fpEl, "requestedAltitude"))),
                parseInteger(text(child(fpEl, "assignedAltitude"))),
                blankToNull(text(child(fpEl, "category"))),
                blankToNull(text(child(fpEl, "dbi"))),
                blankToNull(text(child(fpEl, "acid"))),
                blankToNull(text(child(fpEl, "acType"))),
                blankToNull(text(child(fpEl, "entryFix"))),
                blankToNull(text(child(fpEl, "exitFix"))),
                blankToNull(text(child(fpEl, "airport"))),
                blankToNull(text(child(fpEl, "flightRules"))),
                blankToNull(text(child(fpEl, "rawFlightRules"))),
                blankToNull(text(child(fpEl, "type"))),
                blankToNull(text(child(fpEl, "ptdTime"))),
                blankToNull(text(child(fpEl, "status"))),
                parseBoolean(text(child(fpEl, "delete"))),
                parseBoolean(text(child(fpEl, "suspended"))),
                blankToNull(text(child(fpEl, "lld"))),
                blankToNull(text(child(fpEl, "ECID"))),
                blankToNull(text(child(fpEl, "eqptSuffix")))
        );

        Element enhancedEl = child(record, "enhancedData");
        TaisObservation.EnhancedData enhanced = enhancedEl == null ? null : new TaisObservation.EnhancedData(
                blankToNull(text(child(enhancedEl, "eramGufi"))),
                blankToNull(text(child(enhancedEl, "sfdpsGufi"))),
                blankToNull(text(child(enhancedEl, "departureAirport"))),
                blankToNull(text(child(enhancedEl, "destinationAirport")))
        );

        return new TaisObservation(facility, meta, track, flightPlan, enhanced, receivedAt);
    }

    private static Element child(Element parent, String wanted) {
        if (parent == null) return null;
        NodeList nodes = parent.getChildNodes();
        for (int i = 0; i < nodes.getLength(); i++) {
            if (nodes.item(i) instanceof Element el && wanted.equals(localName(el))) return el;
        }
        return null;
    }

    private static String localName(Element el) {
        String local = el.getLocalName();
        if (local != null && !local.isBlank()) return local;
        String name = el.getNodeName();
        if (name == null) return "";
        int colon = name.indexOf(':');
        return colon >= 0 ? name.substring(colon + 1) : name;
    }

    private static String text(Element el) {
        return el == null ? null : el.getTextContent();
    }

    private static String firstNonBlank(String... values) {
        for (String value : values) {
            value = blankToNull(value);
            if (value != null) return value;
        }
        return null;
    }

    private static String blankToNull(String value) {
        if (value == null) return null;
        value = value.strip();
        return value.isEmpty() ? null : value;
    }

    private static String normalizeHexAddress(String value) {
        value = blankToNull(value);
        return value == null ? null : value.toLowerCase(Locale.ROOT);
    }

    private static Instant parseInstant(String value) {
        value = blankToNull(value);
        if (value == null) return null;
        try {
            return Instant.parse(value);
        } catch (DateTimeParseException ignored) {
            return null;
        }
    }

    private static Long parseLong(String value) {
        value = blankToNull(value);
        if (value == null) return null;
        try {
            return Long.parseLong(value);
        } catch (NumberFormatException ignored) {
            return null;
        }
    }

    private static Integer parseInteger(String value) {
        value = blankToNull(value);
        if (value == null) return null;
        try {
            return Integer.parseInt(value);
        } catch (NumberFormatException ignored) {
            return null;
        }
    }

    private static Double parseDouble(String value) {
        value = blankToNull(value);
        if (value == null) return null;
        try {
            return Double.parseDouble(value);
        } catch (NumberFormatException ignored) {
            return null;
        }
    }

    private static Boolean parseBoolean(String value) {
        value = blankToNull(value);
        if (value == null) return null;
        return switch (value.toLowerCase(Locale.ROOT)) {
            case "1", "true", "yes" -> true;
            case "0", "false", "no" -> false;
            default -> null;
        };
    }
}
