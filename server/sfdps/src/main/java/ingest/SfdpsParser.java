package ingest;

import messages.JmsMetadata;
import messages.SfdpsChannel;
import messages.SfdpsEvent;
import org.w3c.dom.Document;

import java.time.Instant;
import java.util.List;

/** Dispatches one parsed XML document to the parser for its subscription. */
final class SfdpsParser {
    private SfdpsParser() {}

    static List<SfdpsEvent> parse(SfdpsChannel channel, Document doc, JmsMetadata jms, Instant receivedAt) {
        return switch (channel) {
            case FLIGHT_FIXM -> FlightFixmParser.parse(doc, jms, receivedAt);
            case AIRSPACE_AIXM -> AirspaceAixmParser.parse(doc, jms, receivedAt);
            case GENERAL -> GeneralParser.parse(doc, jms, receivedAt);
            case STATUS -> StatusParser.parse(doc, jms, receivedAt);
        };
    }
}
