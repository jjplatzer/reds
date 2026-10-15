package ingest;

import messages.JmsMetadata;
import messages.SfdpsChannel;
import messages.SfdpsEvent;
import messages.StatusMessage;
import org.w3c.dom.Document;
import org.w3c.dom.Element;

import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/** Parser for the SFDPS Status SimpleXML subscription. */
final class StatusParser {
    private StatusParser() {}

    static List<SfdpsEvent> parse(Document doc, JmsMetadata jms, Instant receivedAt) {
        Element root = doc.getDocumentElement();
        if (root == null) return List.of();
        Map<String, String> fields = XmlSupport.leafFields(root);

        String center = XmlSupport.firstNonBlank(
                XmlSupport.firstField(fields, "center", "Center"),
                jms == null ? null : jms.sourceFacility()
        );
        if (center != null) center = center.toUpperCase(Locale.ROOT);

        Map<String, String> artccStates = parseStatePairs(root, "artcc");
        Map<String, String> softwareStates = parseStatePairs(root, "software");
        StatusMessage status = new StatusMessage(
                center,
                XmlSupport.firstField(fields, "classification", "Classification"),
                receivedAt,
                XmlSupport.firstInstant(
                        XmlSupport.parseInstant(XmlSupport.firstField(fields, "time", "Time")),
                        jms == null ? null : jms.fdpsReceivedAt(),
                        receivedAt
                ),
                jms,
                XmlSupport.firstField(fields, "statusType", "StatusType", "type"),
                XmlSupport.firstField(fields, "source", "Source"),
                artccStates,
                softwareStates,
                XmlSupport.firstField(fields, "process", "Process"),
                XmlSupport.firstField(fields, "details", "Details"),
                fields
        );
        String messageType = jms != null && jms.messageType() != null ? jms.messageType() : "STATUS";
        return List.of(new SfdpsEvent(SfdpsChannel.STATUS, center, messageType, receivedAt,
                jms, null, null, null, status));
    }

    private static Map<String, String> parseStatePairs(Element root, String contains) {
        Map<String, String> out = new LinkedHashMap<>();
        var nodes = root.getElementsByTagName("*");
        for (int i = 0; i < nodes.getLength(); i++) {
            if (!(nodes.item(i) instanceof Element element)) continue;
            String local = XmlSupport.localName(element);
            if (local == null || !local.toLowerCase(Locale.ROOT).contains(contains)) continue;
            String state = XmlSupport.firstNonBlank(
                    XmlSupport.attr(element, "state"),
                    XmlSupport.text(XmlSupport.child(element, "state")),
                    XmlSupport.text(XmlSupport.child(element, "status"))
            );
            String id = XmlSupport.firstNonBlank(
                    XmlSupport.attr(element, "center"), XmlSupport.attr(element, "id"),
                    XmlSupport.attr(element, "name"), XmlSupport.text(XmlSupport.child(element, "center")),
                    XmlSupport.text(XmlSupport.child(element, "name"))
            );
            if (id != null && state != null) out.put(id, state);
        }
        return out;
    }
}
