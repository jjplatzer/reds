package ingest;

import org.w3c.dom.Document;
import org.w3c.dom.Element;
import org.w3c.dom.NamedNodeMap;
import org.w3c.dom.Node;
import org.w3c.dom.NodeList;

import javax.xml.parsers.DocumentBuilder;
import javax.xml.parsers.DocumentBuilderFactory;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/** Namespace-tolerant DOM helpers for the FAA XML dialects used by SFDPS. */
final class XmlSupport {
    private XmlSupport() {}

    static DocumentBuilder newDocumentBuilder() throws Exception {
        DocumentBuilderFactory dbf = DocumentBuilderFactory.newInstance();
        dbf.setNamespaceAware(true);
        dbf.setExpandEntityReferences(false);
        dbf.setXIncludeAware(false);
        tryFeature(dbf, "http://apache.org/xml/features/disallow-doctype-decl", true);
        tryFeature(dbf, "http://xml.org/sax/features/external-general-entities", false);
        tryFeature(dbf, "http://xml.org/sax/features/external-parameter-entities", false);
        tryAttribute(dbf, javax.xml.XMLConstants.ACCESS_EXTERNAL_DTD, "");
        tryAttribute(dbf, javax.xml.XMLConstants.ACCESS_EXTERNAL_SCHEMA, "");
        return dbf.newDocumentBuilder();
    }

    static String localName(Node node) {
        if (node == null) return null;
        String local = node.getLocalName();
        if (local != null) return local;
        String name = node.getNodeName();
        int colon = name == null ? -1 : name.indexOf(':');
        return colon >= 0 ? name.substring(colon + 1) : name;
    }

    static Element child(Element parent, String local) {
        if (parent == null) return null;
        NodeList nodes = parent.getChildNodes();
        for (int i = 0; i < nodes.getLength(); i++) {
            Node node = nodes.item(i);
            if (node instanceof Element element && local.equals(localName(element))) return element;
        }
        return null;
    }

    static List<Element> children(Element parent, String local) {
        List<Element> out = new ArrayList<>();
        if (parent == null) return out;
        NodeList nodes = parent.getChildNodes();
        for (int i = 0; i < nodes.getLength(); i++) {
            Node node = nodes.item(i);
            if (node instanceof Element element && (local == null || local.equals(localName(element)))) out.add(element);
        }
        return out;
    }

    static Element firstDescendant(Element root, String local) {
        if (root == null) return null;
        if (local.equals(localName(root))) return root;
        NodeList nodes = root.getElementsByTagNameNS("*", local);
        if (nodes.getLength() > 0 && nodes.item(0) instanceof Element element) return element;
        // Some malformed/legacy DOMs do not expose namespace wildcard lookup.
        NodeList all = root.getElementsByTagName("*");
        for (int i = 0; i < all.getLength(); i++) {
            if (all.item(i) instanceof Element element && local.equals(localName(element))) return element;
        }
        return null;
    }

    static List<Element> descendants(Element root, String local) {
        List<Element> out = new ArrayList<>();
        if (root == null) return out;
        NodeList all = root.getElementsByTagName("*");
        for (int i = 0; i < all.getLength(); i++) {
            if (all.item(i) instanceof Element element && local.equals(localName(element))) out.add(element);
        }
        if (local.equals(localName(root))) out.add(0, root);
        return out;
    }

    static Element firstPath(Element root, String... locals) {
        Element current = root;
        for (String local : locals) {
            current = child(current, local);
            if (current == null) return null;
        }
        return current;
    }

    static Element ancestor(Element element, String local) {
        Node node = element == null ? null : element.getParentNode();
        while (node instanceof Element parent) {
            if (local.equals(localName(parent))) return parent;
            node = parent.getParentNode();
        }
        return null;
    }

    static String text(Element element) {
        if (element == null) return null;
        String value = element.getTextContent();
        return blankToNull(value);
    }

    static String text(Element parent, String descendantLocal) {
        return text(firstDescendant(parent, descendantLocal));
    }

    static String attr(Element element, String local) {
        if (element == null) return null;
        NamedNodeMap attrs = element.getAttributes();
        for (int i = 0; i < attrs.getLength(); i++) {
            Node attr = attrs.item(i);
            if (local.equals(localName(attr))) return blankToNull(attr.getNodeValue());
        }
        return null;
    }

    static Instant firstInstant(Instant... values) {
        for (Instant value : values) if (value != null) return value;
        return null;
    }

    static String firstNonBlank(String... values) {
        for (String value : values) {
            value = blankToNull(value);
            if (value != null) return value;
        }
        return null;
    }

    static String blankToNull(String value) {
        if (value == null) return null;
        value = value.strip();
        return value.isEmpty() ? null : value;
    }

    static Instant parseInstant(String value) {
        value = blankToNull(value);
        if (value == null) return null;
        try { return Instant.parse(value); } catch (DateTimeParseException ignored) {}
        try { return OffsetDateTime.parse(value).toInstant(); } catch (DateTimeParseException ignored) {}
        return null;
    }

    static Double parseDouble(String value) {
        value = blankToNull(value);
        if (value == null) return null;
        try { return Double.parseDouble(value); } catch (NumberFormatException ignored) { return null; }
    }

    static Long parseLong(String value) {
        value = blankToNull(value);
        if (value == null) return null;
        try { return Long.parseLong(value); } catch (NumberFormatException ignored) { return null; }
    }

    static Boolean parseBoolean(String value) {
        value = blankToNull(value);
        if (value == null) return null;
        return switch (value.toLowerCase(Locale.ROOT)) {
            case "true", "1", "yes", "y" -> true;
            case "false", "0", "no", "n" -> false;
            default -> null;
        };
    }

    static double[] parsePos(String value) {
        value = blankToNull(value);
        if (value == null) return null;
        String[] parts = value.replace(',', ' ').trim().split("\\s+");
        if (parts.length < 2) return null;
        Double a = parseDouble(parts[0]), b = parseDouble(parts[1]);
        return a == null || b == null ? null : new double[] { a, b };
    }

    static Map<String, String> nameValueMap(Element root) {
        Map<String, String> out = new LinkedHashMap<>();
        for (Element nv : descendants(root, "nameValue")) {
            String name = firstNonBlank(attr(nv, "name"), text(child(nv, "name")));
            String value = firstNonBlank(attr(nv, "value"), text(child(nv, "value")), text(nv));
            if (name != null && value != null) out.put(name, value);
        }
        return out;
    }

    /**
     * Preserve simple-schema fields we do not yet model explicitly. Repeated
     * names are suffixed #2, #3, ... rather than silently overwritten.
     */
    static Map<String, String> leafFields(Element root) {
        Map<String, String> out = new LinkedHashMap<>();
        collectLeafFields(root, out);
        return out;
    }

    private static void collectLeafFields(Element element, Map<String, String> out) {
        boolean hasElementChild = false;
        NodeList children = element.getChildNodes();
        for (int i = 0; i < children.getLength(); i++) {
            if (children.item(i) instanceof Element child) {
                hasElementChild = true;
                collectLeafFields(child, out);
            }
        }
        if (hasElementChild) return;

        String value = text(element);
        if (value == null) return;
        String base = localName(element);
        String key = base;
        for (int i = 2; out.containsKey(key); i++) key = base + "#" + i;
        out.put(key, value);
    }

    static String firstField(Map<String, String> fields, String... names) {
        for (String name : names) {
            String direct = fields.get(name);
            if (direct != null) return direct;
            for (Map.Entry<String, String> e : fields.entrySet()) {
                String key = e.getKey();
                int hash = key.indexOf('#');
                if (hash >= 0) key = key.substring(0, hash);
                if (key.equalsIgnoreCase(name)) return e.getValue();
            }
        }
        return null;
    }

    private static void tryFeature(DocumentBuilderFactory dbf, String feature, boolean value) {
        try { dbf.setFeature(feature, value); } catch (Exception ignored) {}
    }

    private static void tryAttribute(DocumentBuilderFactory dbf, String attribute, String value) {
        try { dbf.setAttribute(attribute, value); } catch (Exception ignored) {}
    }
}
