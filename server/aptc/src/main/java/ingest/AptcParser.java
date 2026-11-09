package ingest;

import io.vertx.core.json.JsonObject;
import org.w3c.dom.Document;
import org.w3c.dom.Element;
import org.w3c.dom.Node;
import org.xml.sax.SAXParseException;
import org.xml.sax.helpers.DefaultHandler;

import javax.xml.XMLConstants;
import javax.xml.parsers.DocumentBuilder;
import javax.xml.parsers.DocumentBuilderFactory;
import java.io.ByteArrayInputStream;
import java.time.Instant;
import java.time.DateTimeException;
import java.time.OffsetDateTime;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.Objects;

/** Parses TFMData fiOutput/fiMessage[@msgType='APTC']/airportConfigMessage. */
public final class AptcParser {
    public static final String ADDRESS = "faa.aptc.configurations";
    private static final String[] TEXT_FIELDS = {
            "arrRunwayConf", "depRunwayConf", "weather", "aarAdjust", "remarks"
    };
    private static final String[] INTEGER_FIELDS = {"arrRate", "depRate", "stratAar", "remarksGroupColor"};
    private static final String[] FLAG_FIELDS = {
            "dynArrEnteredOnPanel", "adrEnteredOnPanel", "airportInFile", "arrUserSpecified", "depUserSpecified"
    };
    private static final String[] TIME_FIELDS = {"eventTime", "entryTime", "updateTime"};

    private AptcParser() {}

    public record Batch(List<JsonObject> configurations, int ignoredMessages, int invalidMessages) {
        public Batch { configurations = List.copyOf(configurations); }
    }

    public static Batch parse(byte[] xml, Instant receivedAt) throws Exception {
        return parse(newDocumentBuilder().parse(new ByteArrayInputStream(xml)), receivedAt);
    }

    public static Batch parse(Document document, Instant receivedAt) {
        Objects.requireNonNull(receivedAt, "receivedAt");
        Element root = document.getDocumentElement();
        if (root == null || !"tfmDataService".equals(localName(root))) return new Batch(List.of(), 1, 0);
        List<JsonObject> configurations = new ArrayList<>();
        int ignored = 0, invalid = 0;
        for (Element output : children(root, "fiOutput")) {
            for (Element message : children(output, "fiMessage")) {
                if (!"APTC".equalsIgnoreCase(message.getAttribute("msgType").strip())) {
                    ignored++;
                    continue;
                }
                try {
                    Element body = child(message, "airportConfigMessage");
                    if (body == null) throw new IllegalArgumentException("Missing airportConfigMessage");
                    String airport = code(text(body, "airport"));
                    if (airport == null || !airport.matches("[A-Z0-9]{1,16}"))
                        throw new IllegalArgumentException("Missing or invalid airport");

                    String sourceFacility = code(attribute(message, "sourceFacility"));
                    String facility = code(text(body, "facility"));
                    JsonObject configuration = new JsonObject()
                            .put("airport", airport)
                            .put("facility", facility == null ? sourceFacility : facility)
                            .put("enteringFacility", code(text(body, "enteringFacility")))
                            .put("msgType", "APTC")
                            .put("sourceFacility", sourceFacility)
                            .put("sourceTimeStamp", timestamp(attribute(message, "sourceTimeStamp")))
                            .put("refresh", attribute(message, "refresh"))
                            .put("sensitivity", attribute(message, "sensitivity"))
                            .put("destinationCodes", attribute(message, "destinationCodes"))
                            .put("receivedAt", receivedAt.toString());
                    // Runway descriptions are text, including qualifiers such as "OFFSET".
                    for (String field : TEXT_FIELDS) configuration.put(field, text(body, field));
                    for (String field : INTEGER_FIELDS) configuration.put(field, integer(text(body, field)));
                    for (String field : FLAG_FIELDS) configuration.put(field, flag(text(body, field)));
                    for (String field : TIME_FIELDS) configuration.put(field, timestamp(text(body, field)));
                    configurations.add(configuration);
                } catch (IllegalArgumentException e) {
                    // One malformed logical message must not discard valid siblings in the same envelope.
                    invalid++;
                }
            }
        }
        return new Batch(configurations, ignored, invalid);
    }

    public static DocumentBuilder newDocumentBuilder() throws Exception {
        DocumentBuilderFactory factory = DocumentBuilderFactory.newInstance();
        factory.setNamespaceAware(true);
        factory.setExpandEntityReferences(false);
        factory.setXIncludeAware(false);
        factory.setFeature("http://apache.org/xml/features/disallow-doctype-decl", true);
        factory.setFeature("http://xml.org/sax/features/external-general-entities", false);
        factory.setFeature("http://xml.org/sax/features/external-parameter-entities", false);
        factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_DTD, "");
        factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_SCHEMA, "");
        DocumentBuilder builder = factory.newDocumentBuilder();
        builder.setErrorHandler(new DefaultHandler() {
            @Override public void error(SAXParseException e) throws SAXParseException { throw e; }
            @Override public void fatalError(SAXParseException e) throws SAXParseException { throw e; }
        });
        return builder;
    }

    private static String timestamp(String value) {
        if (value == null || value.isBlank()) return null;
        try { return OffsetDateTime.parse(value).toInstant().toString(); }
        catch (DateTimeException e) { throw new IllegalArgumentException("Invalid timestamp", e); }
    }

    private static Integer integer(String value) {
        return value == null || value.isBlank() ? null : Integer.valueOf(value);
    }

    private static Boolean flag(String value) {
        if (value == null || value.isBlank()) return null;
        return switch (value.toLowerCase(Locale.ROOT)) {
            case "1", "true" -> true;
            case "0", "false" -> false;
            default -> throw new IllegalArgumentException("Invalid flag");
        };
    }

    private static String code(String value) {
        return value == null || value.isBlank() ? null : value.toUpperCase(Locale.ROOT);
    }

    private static String attribute(Element element, String name) {
        return element.hasAttribute(name) ? element.getAttribute(name).strip() : null;
    }

    private static String text(Element element, String name) {
        Element child = child(element, name);
        return child == null ? null : child.getTextContent().strip();
    }

    private static Element child(Element parent, String name) {
        for (Node node = parent.getFirstChild(); node != null; node = node.getNextSibling()) {
            if (node instanceof Element element && name.equals(localName(element))) return element;
        }
        return null;
    }

    private static List<Element> children(Element parent, String name) {
        List<Element> result = new ArrayList<>();
        for (Node node = parent.getFirstChild(); node != null; node = node.getNextSibling()) {
            if (node instanceof Element element && name.equals(localName(element))) result.add(element);
        }
        return result;
    }

    private static String localName(Element element) {
        return element.getLocalName() == null ? element.getNodeName() : element.getLocalName();
    }
}
