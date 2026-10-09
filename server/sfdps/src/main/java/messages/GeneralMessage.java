package messages;

import java.time.Instant;
import java.util.Map;

/** General-message SimpleXML state/event. Unknown fields are retained in fields. */
public record GeneralMessage(
        String messageType,
        String facility,
        Instant receivedAt,
        Instant messageTime,
        JmsMetadata jms,
        String flightId,
        String computerId,
        String siteSpecificPlanId,
        String remarks,
        String remarkType,
        Double interimAltitude,
        String holdFix,
        String holdTime,
        String fav,
        Map<String, String> fields
) {
    public GeneralMessage { fields = fields == null ? Map.of() : Map.copyOf(fields); }
}
