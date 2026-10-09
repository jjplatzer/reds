package store;

import config.Env;
import io.vertx.core.AbstractVerticle;
import messages.AirspaceMessage;
import messages.GeneralMessage;
import messages.SfdpsBatch;
import messages.SfdpsEvent;
import messages.StatusMessage;
import wire.SfdpsWire;

import java.time.Duration;
import java.time.Instant;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/**
 * Single-owner current SFDPS state.
 *
 * Flight FIXM is reduced per source ARTCC, never globally by callsign. That is
 * important because the manual explicitly allows multiple ARTCCs to publish
 * copies of the same flight. The SFDPS GUFI is the preferred identity inside a
 * source facility; fallback identifiers are only used when a feed omits it.
 */
public final class SfdpsStore extends AbstractVerticle {
    public static final String EVENT_ADDRESS = "sfdps.store.event";
    public static final String SNAPSHOT_ADDRESS = "sfdps.store.snapshot";

    private final Map<String, FlightState> flights = new HashMap<>();
    private final Map<String, AirspaceMessage> airspace = new HashMap<>();
    private final Map<String, ArrayDeque<GeneralMessage>> general = new HashMap<>();
    private final Map<String, StatusMessage> statuses = new HashMap<>();
    private final Map<String, SequenceState> sequences = new HashMap<>();

    private long revision;
    private int generalHistory;
    private Duration terminalRetention;
    private Duration droppedTrackRetention;
    private Duration idleRetention;

    @Override
    public void start() {
        generalHistory = Math.max(16, intEnv("SFDPS_GENERAL_HISTORY", 256));
        terminalRetention = Duration.ofMillis(Math.max(60_000L, longEnv("SFDPS_TERMINAL_RETENTION_MS", 30L * 60_000L)));
        droppedTrackRetention = Duration.ofMillis(Math.max(60_000L, longEnv("SFDPS_DROPPED_TRACK_RETENTION_MS", 30L * 60_000L)));
        idleRetention = Duration.ofMillis(Math.max(60_000L, longEnv("SFDPS_IDLE_RETENTION_MS", 24L * 60L * 60_000L)));
        long sweepMs = Math.max(10_000L, longEnv("SFDPS_STORE_SWEEP_MS", 60_000L));

        vertx.eventBus().<SfdpsBatch>localConsumer(SfdpsBatch.ADDRESS, message -> acceptBatch(message.body()));

        vertx.eventBus().<String>consumer(SNAPSHOT_ADDRESS, message -> {
            String facility = normalizeFacility(message.body());
            message.reply(SfdpsWire.snapshot(
                    revision,
                    Instant.now(),
                    facility,
                    snapshotFlights(facility),
                    snapshotAirspace(facility),
                    snapshotGeneral(facility),
                    snapshotStatuses(facility)
            ));
        });

        vertx.setPeriodic(sweepMs, ignored -> sweepFlights());
        System.out.println("[SFDPS store] terminalRetention=" + terminalRetention.toMinutes() + "m" +
                " droppedTrackRetention=" + droppedTrackRetention.toMinutes() + "m" +
                " idleRetention=" + idleRetention.toHours() + "h");
    }

    private void sweepFlights() {
        Instant now = Instant.now();
        List<String> remove = new ArrayList<>();
        for (Map.Entry<String, FlightState> entry : flights.entrySet()) {
            FlightState state = entry.getValue();
            if (state.receivedAt == null) continue;
            Duration age = Duration.between(state.receivedAt, now);
            Duration retention = state.terminal() ? terminalRetention
                    : state.droppedTrack() ? droppedTrackRetention
                    : idleRetention;
            if (age.compareTo(retention) > 0) remove.add(entry.getKey());
        }
        for (String key : remove) {
            FlightState state = flights.remove(key);
            if (state == null) continue;
            revision++;
            vertx.eventBus().publish(EVENT_ADDRESS, SfdpsWire.flightRemove(revision, state.facility, state.key));
        }
    }

    private void acceptBatch(SfdpsBatch batch) {
        if (batch == null || batch.events().isEmpty()) return;

        // NEMS does not guarantee message ordering. FDPS_SequenceNo is monotonic
        // per source center except on producer restart/rollover. Because the four
        // SFDPS subscriptions use independent queues, ordering is guarded per
        // channel + facility rather than globally across channels.
        SfdpsEvent first = batch.events().getFirst();
        String facility = normalizeFacility(first.facility());
        if (facility.isEmpty()) facility = "ALL";
        if (isStaleBatch(batch, facility)) return;

        for (SfdpsEvent event : batch.events()) accept(event);
    }

    private boolean isStaleBatch(SfdpsBatch batch, String facility) {
        SfdpsEvent first = batch.events().getFirst();
        if (first.jms() == null || first.jms().sequenceNumber() == null) return false;

        long sequence = first.jms().sequenceNumber();
        Instant received = first.jms().fdpsReceivedAt();
        String key = batch.channel().wireName() + ":" + facility;
        SequenceState previous = sequences.get(key);
        if (previous == null) {
            sequences.put(key, new SequenceState(sequence, received));
            return false;
        }

        if (sequence > previous.sequence()) {
            sequences.put(key, new SequenceState(sequence, later(received, previous.receivedAt())));
            return false;
        }
        if (sequence == previous.sequence()) {
            // acceptBatch() is called once per JMS delivery. Normally equality
            // therefore means Solace redelivered the entire JMS message. A
            // strictly newer FDPS receive time is the one exception: the SFDPS
            // sequence counter may have restarted and reached the same value.
            if (received != null && previous.receivedAt() != null && received.isAfter(previous.receivedAt())) {
                sequences.put(key, new SequenceState(sequence, received));
                return false;
            }
            return true;
        }

        // Lower sequence with an older SFDPS receive timestamp is a genuine
        // out-of-order delivery. A lower sequence with a newer timestamp is a
        // documented counter reset/restart and becomes the new baseline.
        if (received != null && previous.receivedAt() != null && received.isBefore(previous.receivedAt())) {
            System.err.println("[SFDPS store] Dropping out-of-order " + batch.channel().wireName() +
                    " batch facility=" + facility + " seq=" + sequence + " last=" + previous.sequence());
            return true;
        }

        sequences.put(key, new SequenceState(sequence, later(received, previous.receivedAt())));
        return false;
    }

    private void accept(SfdpsEvent event) {
        if (event == null) return;
        String facility = normalizeFacility(event.facility());
        if (facility.isEmpty()) facility = "ALL";

        switch (event.channel()) {
            case FLIGHT_FIXM -> acceptFlight(event, facility);
            case AIRSPACE_AIXM -> acceptAirspace(event, facility);
            case GENERAL -> acceptGeneral(event, facility);
            case STATUS -> acceptStatus(event, facility);
        }
    }

    private void acceptFlight(SfdpsEvent event, String facility) {
        if (event.flight() == null) return;
        String identity = event.flight().identity();
        if (identity == null) {
            String fallback = event.jms() == null ? null : event.jms().flightId();
            if (fallback == null || fallback.isBlank()) {
                System.err.println("[SFDPS store] Ignoring flight event without stable identity; type=" + event.messageType());
                return;
            }
            identity = "J:" + fallback;
        }

        final String stableIdentity = identity;
        String key = facility + ":" + stableIdentity;
        FlightState state = flights.computeIfAbsent(key, ignored -> new FlightState(key, facility, stableIdentity));
        state.apply(event);
        revision++;
        vertx.eventBus().publish(EVENT_ADDRESS, SfdpsWire.flightUpdate(revision, facility, event.messageType(), state.snapshot()));
    }

    private void acceptAirspace(SfdpsEvent event, String facility) {
        AirspaceMessage entity = event.airspace();
        if (entity == null) return;
        String key = facility + ":" + entity.entityKey();
        airspace.put(key, entity);
        revision++;
        vertx.eventBus().publish(EVENT_ADDRESS, SfdpsWire.airspaceUpdate(revision, facility, event.messageType(), entity));
    }

    private void acceptGeneral(SfdpsEvent event, String facility) {
        GeneralMessage value = event.general();
        if (value == null) return;
        ArrayDeque<GeneralMessage> history = general.computeIfAbsent(facility, ignored -> new ArrayDeque<>());
        history.addLast(value);
        while (history.size() > generalHistory) history.removeFirst();
        revision++;
        vertx.eventBus().publish(EVENT_ADDRESS, SfdpsWire.generalUpdate(revision, facility, event.messageType(), value));
    }

    private void acceptStatus(SfdpsEvent event, String facility) {
        StatusMessage value = event.status();
        if (value == null) return;
        String key = facility + ":" + nullSafe(value.statusType()) + ":" + nullSafe(value.source());
        statuses.put(key, value);
        revision++;
        vertx.eventBus().publish(EVENT_ADDRESS, SfdpsWire.statusUpdate(revision, facility, event.messageType(), value));
    }

    private List<FlightSnapshot> snapshotFlights(String facility) {
        List<FlightSnapshot> out = new ArrayList<>();
        for (FlightState state : flights.values()) if (facility.equals(state.facility)) out.add(state.snapshot());
        out.sort((a, b) -> a.key().compareTo(b.key()));
        return List.copyOf(out);
    }

    private List<AirspaceMessage> snapshotAirspace(String facility) {
        List<AirspaceMessage> out = new ArrayList<>();
        for (Map.Entry<String, AirspaceMessage> e : airspace.entrySet()) {
            if (e.getKey().startsWith(facility + ":")) out.add(e.getValue());
        }
        out.sort((a, b) -> a.entityKey().compareTo(b.entityKey()));
        return List.copyOf(out);
    }

    private List<GeneralMessage> snapshotGeneral(String facility) {
        List<GeneralMessage> out = new ArrayList<>();
        append(out, general.get(facility));
        if (!"ALL".equals(facility)) append(out, general.get("ALL"));
        return List.copyOf(out);
    }

    private List<StatusMessage> snapshotStatuses(String facility) {
        List<StatusMessage> out = new ArrayList<>();
        for (Map.Entry<String, StatusMessage> e : statuses.entrySet()) {
            if (e.getKey().startsWith(facility + ":") || e.getKey().startsWith("ALL:") || e.getKey().startsWith("NA:")) {
                out.add(e.getValue());
            }
        }
        return List.copyOf(out);
    }

    private static <T> void append(List<T> out, ArrayDeque<T> values) {
        if (values != null) out.addAll(values);
    }

    private static String normalizeFacility(String facility) {
        return facility == null ? "" : facility.strip().toUpperCase(Locale.ROOT);
    }

    private static String nullSafe(String value) { return value == null ? "" : value; }
    private static Instant later(Instant incoming, Instant current) {
        if (incoming == null) return current;
        if (current == null || incoming.isAfter(current)) return incoming;
        return current;
    }

    private record SequenceState(long sequence, Instant receivedAt) {}

    private static int intEnv(String key, int def) {
        try {
            String value = Env.get(key);
            return value == null ? def : Integer.parseInt(value.strip());
        } catch (Exception ignored) { return def; }
    }

    private static long longEnv(String key, long def) {
        try {
            String value = Env.get(key);
            return value == null ? def : Long.parseLong(value.strip());
        } catch (Exception ignored) { return def; }
    }
}
