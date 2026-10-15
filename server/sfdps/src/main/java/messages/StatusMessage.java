package messages;

import java.time.Instant;
import java.util.Map;

/** SFDPS service-status SimpleXML message. */
public record StatusMessage(
        String center,
        String classification,
        Instant receivedAt,
        Instant time,
        JmsMetadata jms,
        String statusType,
        String source,
        Map<String, String> artccStates,
        Map<String, String> softwareStates,
        String process,
        String details,
        Map<String, String> fields
) {
    public StatusMessage {
        artccStates = artccStates == null ? Map.of() : Map.copyOf(artccStates);
        softwareStates = softwareStates == null ? Map.of() : Map.copyOf(softwareStates);
        fields = fields == null ? Map.of() : Map.copyOf(fields);
    }
}
