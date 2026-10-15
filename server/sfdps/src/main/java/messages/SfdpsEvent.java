package messages;

import java.time.Instant;

/** One normalized logical SFDPS event, possibly one of many in a JMS batch. */
public record SfdpsEvent(
        SfdpsChannel channel,
        String facility,
        String messageType,
        Instant receivedAt,
        JmsMetadata jms,
        FlightMessage flight,
        AirspaceMessage airspace,
        GeneralMessage general,
        StatusMessage status
) {}
