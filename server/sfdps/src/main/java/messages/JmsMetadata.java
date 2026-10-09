package messages;

import java.time.Instant;

/** SFDPS JMS properties used for routing, de-duplication and diagnostics. */
public record JmsMetadata(
        String sourceSystem,
        String sourceFacility,
        String messageType,
        String flightOperator,
        String flightId,
        String origin,
        String destination,
        Boolean oneMinuteFrequency,
        String sensitive,
        String restricted,
        Boolean authoritative,
        Instant fdpsReceivedAt,
        Instant fdpsSentAt,
        Long sequenceNumber,
        String dataType
) {}
