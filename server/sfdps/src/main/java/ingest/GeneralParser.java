package ingest;

import messages.GeneralMessage;
import messages.JmsMetadata;
import messages.SfdpsChannel;
import messages.SfdpsEvent;
import org.w3c.dom.Document;
import org.w3c.dom.Element;

import java.time.Instant;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/** Parser for En Route General Message Publication SimpleXML. */
final class GeneralParser {
    private GeneralParser() {}

    static List<SfdpsEvent> parse(Document doc, JmsMetadata jms, Instant receivedAt) {
        Element root = doc.getDocumentElement();
        if (root == null) return List.of();
        Map<String, String> fields = XmlSupport.leafFields(root);
        String messageType = XmlSupport.firstNonBlank(
                jms == null ? null : jms.messageType(),
                XmlSupport.firstField(fields, "propMessageType", "messageType", "msgType")
        );
        if (messageType == null) messageType = "UNKNOWN";
        messageType = messageType.toUpperCase(Locale.ROOT);

        String facility = XmlSupport.firstNonBlank(
                jms == null ? null : jms.sourceFacility(),
                XmlSupport.firstField(fields, "center", "propSourceFacility", "sourceFacility")
        );
        if (facility != null) facility = facility.toUpperCase(Locale.ROOT);

        Element remarksElement = XmlSupport.firstDescendant(root, "remarks_11c");
        GeneralMessage general = new GeneralMessage(
                messageType,
                facility,
                receivedAt,
                XmlSupport.firstInstant(
                        XmlSupport.parseInstant(XmlSupport.firstField(fields, "time", "propRcvdTime", "sourceTime")),
                        jms == null ? null : jms.fdpsReceivedAt(),
                        receivedAt
                ),
                jms,
                XmlSupport.firstField(fields, "flightId_02a", "flightId"),
                XmlSupport.firstField(fields, "computerId_02d", "computerId"),
                XmlSupport.firstField(fields, "sspId_167a", "sspId"),
                XmlSupport.firstNonBlank(XmlSupport.text(remarksElement), XmlSupport.firstField(fields, "remarks_11c", "remarks")),
                XmlSupport.attr(remarksElement, "remarktype"),
                XmlSupport.parseDouble(XmlSupport.firstField(fields, "interimAlt_76b", "interimAltitude")),
                XmlSupport.firstField(fields, "holdDataFix_21a", "holdFix"),
                XmlSupport.firstField(fields, "holdDataTime_21d", "holdTime"),
                XmlSupport.firstField(fields, "FAV_29d", "fav"),
                fields
        );
        return List.of(new SfdpsEvent(SfdpsChannel.GENERAL, facility, messageType, receivedAt,
                jms, null, null, general, null));
    }
}
