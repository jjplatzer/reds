package messages;

/** The four SFDPS subscriptions REDS currently consumes independently. */
public enum SfdpsChannel {
    FLIGHT_FIXM("flight", "FlightFIXM"),
    AIRSPACE_AIXM("airspace", "AirspaceAIXM"),
    GENERAL("general", "ERGMP"),
    STATUS("status", "STATUS");

    private final String wireName;
    private final String dataType;

    SfdpsChannel(String wireName, String dataType) {
        this.wireName = wireName;
        this.dataType = dataType;
    }

    public String wireName() { return wireName; }
    public String dataType() { return dataType; }

    public boolean matchesDataType(String value) {
        return value == null || value.isBlank() || dataType.equalsIgnoreCase(value.strip());
    }
}
