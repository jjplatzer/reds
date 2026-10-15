package messages;

import java.time.Instant;
import java.util.List;
import java.util.Map;

/** Normalized SFDPS Airspace AIXM entity (HR/SH/SU and reconstitution equivalents). */
public record AirspaceMessage(
        String messageType,
        String facility,
        Instant receivedAt,
        JmsMetadata jms,
        Kind kind,
        String identifier,
        String status,
        List<String> favNumbers,
        String scheduleId,
        String scheduleType,
        String activationTimeRaw,
        String deactivationTimeRaw,
        Instant activationTime,
        Instant deactivationTime,
        Double lowerAltitude,
        String lowerAltitudeUom,
        Double upperAltitude,
        String upperAltitudeUom,
        Map<String, String> fields
) {
    public AirspaceMessage {
        favNumbers = favNumbers == null ? List.of() : List.copyOf(favNumbers);
        fields = fields == null ? Map.of() : Map.copyOf(fields);
    }

    public enum Kind { ROUTE, SECTOR, SAA, UNKNOWN }

    public String entityKey() {
        String id = identifier == null || identifier.isBlank() ? "unknown" : identifier;
        return kind.name().toLowerCase() + ":" + id;
    }
}
