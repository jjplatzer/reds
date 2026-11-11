package ingest;

import io.vertx.core.json.JsonArray;
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
import java.math.BigDecimal;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.DateTimeException;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.HexFormat;
import java.util.List;
import java.util.Locale;
import java.util.Objects;
import java.util.TreeSet;
import java.util.regex.Pattern;
import java.util.zip.GZIPInputStream;

/**
 * Product 9850 (Precipitation TRACON). Wire grids contain the declared rectangle,
 * re-encoded as whitespace-separated value,count pairs in source row-major order.
 * The padded source buffer is never stretched to that rectangle. No inverse
 * projection, row flip, rotation direction or centre/corner convention is inferred.
 */
public final class ItwsParser {
    public static final String ADDRESS = "faa.itws.products";
    public static final int MAX_BYTES = 32 << 20;
    private static final int MAX_CELLS = 4_000_000;
    private static final Pattern RUN = Pattern.compile("\\G\\s*(\\d+)\\s*,\\s*(\\d+)");

    private ItwsParser() { }

    public record Batch(List<JsonObject> products, int ignoredMessages, int invalidMessages) {
        public Batch { products = List.copyOf(products); }
    }

    public static Batch parse(byte[] payload, Instant receivedAt) throws Exception {
        return parse(payload, newDocumentBuilder(), receivedAt);
    }

    public static Batch parse(byte[] payload, DocumentBuilder builder, Instant receivedAt) throws Exception {
        if (payload.length > MAX_BYTES) throw new IllegalArgumentException("Oversized ITWS body");
        byte[] xml = payload;
        if (payload.length > 1 && payload[0] == (byte) 0x1f && payload[1] == (byte) 0x8b) {
            try (var gzip = new GZIPInputStream(new ByteArrayInputStream(payload))) {
                xml = gzip.readNBytes(MAX_BYTES + 1);
                if (xml.length > MAX_BYTES) throw new IllegalArgumentException("Oversized inflated ITWS body");
            }
        }
        return parse(builder.parse(new ByteArrayInputStream(xml)), receivedAt);
    }

    public static Batch parse(Document document, Instant receivedAt) {
        Objects.requireNonNull(receivedAt, "receivedAt");
        Element root = document.getDocumentElement();
        if (root == null || !"itws_msg".equals(name(root))) return new Batch(List.of(), 1, 0);
        try {
            Element header = requiredChild(root, "product_header");
            int productId = integer(requiredChild(header, "product_header_msg_id"), "product_msg_id");
            if (productId != 9850) return new Batch(List.of(), 1, 0);
            List<String> sites = codes(text(header, "product_header_itws_sites"));
            if (sites.isEmpty()) throw new IllegalArgumentException("Missing ITWS sites");
            String source = code(text(header, "product_header_source_id"));
            Instant generated = time(header, "product_header_generation_time", true);
            Instant expires = time(header, "product_header_expiration_time", true);
            if (!expires.isAfter(generated)) throw new IllegalArgumentException("Invalid ITWS validity interval");
            JsonObject grid = grid(requiredChild(root, "precip"));
            JsonObject flags = new JsonObject();
            for (String field : List.of("product_status", "archive_only", "displayable", "source_type", "acquisition_type")) {
                // Preserve numeric enums; notably, displayable=0 must not be interpreted as boolean false.
                flags.put(field, optionalLong(header, "product_header_" + field));
            }
            List<JsonObject> products = new ArrayList<>();
            for (String site : sites) {
                JsonObject product = new JsonObject().put("key", productId + ":" + site + ":" + source)
                        .put("productId", productId).put("productName", text(requiredChild(header, "product_header_msg_id"), "product_msg_name"))
                        .put("site", site).put("source", source).put("sites", new JsonArray(sites))
                        .put("airports", new JsonArray(codes(text(header, "product_header_airports"))))
                        .put("generationTime", generated.toString()).put("expirationTime", expires.toString())
                        .put("latencyStartTime", string(time(header, "product_header_latency_start", false)))
                        .put("flags", flags.copy()).put("grid", grid.copy());
                // Received time changes on redelivery and must not turn a duplicate into an update.
                product.put("contentHash", sha256(product.encode())).put("receivedAt", receivedAt.toString());
                products.add(product);
            }
            return new Batch(products, 0, 0);
        } catch (IllegalArgumentException | DateTimeException e) {
            return new Batch(List.of(), 0, 1);
        }
    }

    private static JsonObject grid(Element precip) {
        Element levels = requiredChild(precip, "prcp_nws_levels");
        if (!"Simple Run-Length Encoding".equals(text(levels, "prcp_grid_compression_encoding_scheme"))) {
            throw new IllegalArgumentException("Unsupported ITWS grid encoding");
        }
        int width = integer(levels, "prcp_grid_max_x"), height = integer(levels, "prcp_grid_max_y");
        int rows = integer(precip, "prcp_nrows"), columns = integer(precip, "prcp_ncols");
        long cells = (long) width * height;
        if (width <= 0 || height <= 0 || cells > MAX_CELLS || rows <= 0 || rows > height || columns <= 0 || columns > width
                || integer(precip, "prcp_nlevels") != 1) throw new IllegalArgumentException("Invalid ITWS grid dimensions");
        int attenuated = special(precip, "prcp_attenuated"), ap = special(precip, "prcp_ap_detected");
        int bad = special(precip, "prcp_bad_value"), noCoverage = special(precip, "prcp_no_coverage");
        if (new TreeSet<>(List.of(attenuated, ap, bad, noCoverage)).size() != 4) {
            throw new IllegalArgumentException("Overlapping ITWS status codes");
        }
        double latitude = scaled(precip, "prcp_TRP_latitude", "degrees");
        double longitude = scaled(precip, "prcp_TRP_longitude", "degrees");
        double rotation = scaled(precip, "prcp_rotation", "degrees");
        double dx = scaled(precip, "prcp_dx", "meters"), dy = scaled(precip, "prcp_dy", "meters");
        if (latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 || Math.abs(rotation) > 360 || dx <= 0 || dy <= 0) {
            throw new IllegalArgumentException("Invalid ITWS grid coordinates");
        }
        byte[] raster = new byte[(int) cells];
        String compressed = requiredText(levels, "prcp_grid_compressed");
        var matcher = RUN.matcher(compressed);
        int offset = 0, end = 0;
        while (matcher.find()) {
            int value = Integer.parseInt(matcher.group(1));
            long count = Long.parseLong(matcher.group(2));
            if (value > 15 || count <= 0 || count > raster.length - offset) throw new IllegalArgumentException("Invalid ITWS RLE run");
            if (value > 6 && value != attenuated && value != ap && value != bad && value != noCoverage) {
                throw new IllegalArgumentException("Unknown ITWS grid value");
            }
            Arrays.fill(raster, offset, offset + (int) count, (byte) value);
            offset += (int) count;
            end = matcher.end();
        }
        if (offset != raster.length || !compressed.substring(end).isBlank()) throw new IllegalArgumentException("Incomplete/overlong ITWS RLE");

        // Repack the declared rectangle, preserving decoded row order and every status/clear cell.
        StringBuilder rle = new StringBuilder();
        long[] counts = new long[16];
        int previous = -1, count = 0, maxLevel = 0;
        for (int row = 0; row < height; row++) {
            for (int column = 0; column < width; column++) {
                int value = raster[row * width + column];
                if (row >= rows || column >= columns) {
                    if (value != noCoverage) throw new IllegalArgumentException("Valid ITWS data outside declared rectangle");
                    continue;
                }
                counts[value]++;
                if (value <= 6) maxLevel = Math.max(maxLevel, value);
                if (value == previous) count++;
                else {
                    appendRun(rle, previous, count);
                    previous = value;
                    count = 1;
                }
            }
        }
        appendRun(rle, previous, count);
        if (maxLevel != integer(levels, "prcp_grid_max_precip_level")) throw new IllegalArgumentException("Inconsistent ITWS maximum level");
        JsonObject histogram = new JsonObject();
        for (int value = 0; value < counts.length; value++) if (counts[value] > 0) histogram.put(Integer.toString(value), counts[value]);
        return new JsonObject().put("rows", rows).put("columns", columns)
                .put("sourceBufferRows", height).put("sourceBufferColumns", width)
                .put("trpLatitude", latitude).put("trpLongitude", longitude)
                .put("xOffsetMeters", scaled(precip, "prcp_xoffset", "meters"))
                .put("yOffsetMeters", scaled(precip, "prcp_yoffset", "meters"))
                .put("dxMeters", dx).put("dyMeters", dy).put("rotationDegrees", rotation)
                .put("volumeScanNumber", optionalLong(precip, "prcp_volume_scan_num"))
                .put("maxPrecipitationLevel", maxLevel).put("counts", histogram)
                .put("clearValue", 0).put("attenuatedValue", attenuated).put("apDetectedValue", ap)
                .put("badValue", bad).put("noCoverageValue", noCoverage)
                .put("encoding", "rle-value-count").put("rle", rle.toString()).put("rowOrder", "source")
                .put("georeferencingVerified", false);
    }

    private static void appendRun(StringBuilder target, int value, int count) {
        if (count == 0) return;
        if (!target.isEmpty()) target.append(' ');
        target.append(value).append(',').append(count);
    }

    public static DocumentBuilder newDocumentBuilder() throws Exception {
        DocumentBuilderFactory factory = DocumentBuilderFactory.newInstance();
        factory.setNamespaceAware(true);
        factory.setExpandEntityReferences(false);
        factory.setXIncludeAware(false);
        factory.setFeature(XMLConstants.FEATURE_SECURE_PROCESSING, true);
        factory.setFeature("http://apache.org/xml/features/disallow-doctype-decl", true);
        factory.setFeature("http://xml.org/sax/features/external-general-entities", false);
        factory.setFeature("http://xml.org/sax/features/external-parameter-entities", false);
        factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_DTD, "");
        factory.setAttribute(XMLConstants.ACCESS_EXTERNAL_SCHEMA, "");
        factory.setAttribute("http://www.oracle.com/xml/jaxp/properties/maxElementDepth", "128");
        DocumentBuilder builder = factory.newDocumentBuilder();
        builder.setErrorHandler(new DefaultHandler() {
            @Override public void error(SAXParseException e) throws SAXParseException { throw e; }
            @Override public void fatalError(SAXParseException e) throws SAXParseException { throw e; }
        });
        return builder;
    }

    private static double scaled(Element parent, String field, String unit) {
        Element value = requiredChild(parent, field);
        if (!unit.equals(value.getAttribute("unit"))) throw new IllegalArgumentException("Unsupported ITWS coordinate unit");
        BigDecimal precision = new BigDecimal(value.hasAttribute("precision") ? value.getAttribute("precision") : "1");
        if (precision.signum() <= 0) throw new IllegalArgumentException("Invalid ITWS coordinate precision");
        double result = new BigDecimal(value.getTextContent().strip()).multiply(precision).doubleValue();
        if (!Double.isFinite(result)) throw new IllegalArgumentException("Non-finite ITWS coordinate");
        return result;
    }

    private static int special(Element parent, String field) {
        int value = integer(parent, field);
        if (value < 7 || value > 15) throw new IllegalArgumentException("Invalid ITWS status code");
        return value;
    }

    private static Instant time(Element header, String prefix, boolean required) {
        String seconds = text(header, prefix + "_seconds");
        if (seconds == null && !required) return null;
        String millis = text(header, prefix + "_milliseconds");
        int fraction = millis == null ? 0 : Integer.parseInt(millis);
        if (fraction < 0 || fraction > 999) throw new IllegalArgumentException("Invalid ITWS fractional timestamp");
        if (seconds == null) throw new IllegalArgumentException("Missing ITWS timestamp");
        return Instant.ofEpochSecond(Long.parseLong(seconds), fraction * 1_000_000L);
    }

    private static String string(Instant time) { return time == null ? null : time.toString(); }

    private static int integer(Element parent, String field) { return Integer.parseInt(requiredText(parent, field)); }

    private static Long optionalLong(Element parent, String field) {
        String value = text(parent, field);
        return value == null ? null : Long.valueOf(value);
    }

    private static List<String> codes(String value) {
        TreeSet<String> result = new TreeSet<>();
        if (value != null) for (String item : value.split("[,\\s]+")) if (!item.isBlank()) result.add(code(item));
        if (result.size() > 64) throw new IllegalArgumentException("Too many ITWS sites");
        return List.copyOf(result);
    }

    private static String code(String value) {
        if (value == null || !value.matches("[A-Za-z0-9_-]{1,32}")) throw new IllegalArgumentException("Invalid ITWS site/source");
        return value.toUpperCase(Locale.ROOT);
    }

    private static String requiredText(Element parent, String field) {
        String value = text(parent, field);
        if (value == null) throw new IllegalArgumentException("Missing ITWS field");
        return value;
    }

    private static String text(Element parent, String field) {
        Element child = child(parent, field);
        if (child == null) return null;
        String value = child.getTextContent().strip();
        return value.isEmpty() ? null : value;
    }

    private static Element requiredChild(Element parent, String field) {
        Element child = child(parent, field);
        if (child == null) throw new IllegalArgumentException("Missing ITWS element");
        return child;
    }

    private static Element child(Element parent, String field) {
        for (Node node = parent.getFirstChild(); node != null; node = node.getNextSibling()) {
            if (node instanceof Element element && field.equals(name(element))) return element;
        }
        return null;
    }

    private static String name(Element element) { return element.getLocalName() == null ? element.getNodeName() : element.getLocalName(); }

    private static String sha256(String value) {
        try { return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(value.getBytes(StandardCharsets.UTF_8))); }
        catch (NoSuchAlgorithmException e) { throw new IllegalStateException(e); }
    }
}
