package messages;

import java.util.List;

/** All logical SFDPS messages decoded from one JMS delivery. */
public record SfdpsBatch(SfdpsChannel channel, List<SfdpsEvent> events) {
    public static final String ADDRESS = "faa.sfdps.event";
    public SfdpsBatch { events = List.copyOf(events); }
}
