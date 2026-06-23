package store;

import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;

import java.util.Collections;
import java.util.HashSet;
import java.util.Set;

/**
 * Fast airport gate for the TargetStore.
 *
 * Holds the set of ICAO codes currently selected in the UI. Observations for
 * airports outside this set are discarded before any merge or diff work begins.
 *
 * <ul>
 *   <li>Empty active set — accept none until the UI selects an airport.</li>
 *   <li>Non-empty active set — only listed airports pass through.</li>
 * </ul>
 *
 * Thread safety: {@link #accepts} reads a volatile reference — safe from any thread.
 * {@link #update} writes atomically via reference replacement.
 *
 * EventBus address: {@value #ADDRESS}
 */
public final class AirportFilter {

    /** EventBus address for filter-set messages from the UI. */
    public static final String ADDRESS = "faa.filter.set";

    private volatile Set<String> active = Set.of();

    /**
     * Returns true if observations for this airport should be processed.
     * Fast path: single volatile read + set lookup.
     */
    public boolean accepts(String airport) {
        Set<String> a = active;
        return airport != null && a.contains(airport);
    }

    /**
     * Applies a filter-set message and returns the new active set.
     * Replaces the active reference atomically.
     */
    public Set<String> update(JsonObject msg) {
        JsonArray arr = msg.getJsonArray("airports");
        if (arr == null || arr.isEmpty()) {
            active = Set.of();
            return active;
        }
        Set<String> next = new HashSet<>(arr.size() * 2);
        for (int i = 0; i < arr.size(); i++) {
            String icao = arr.getString(i);
            if (icao != null && !icao.isBlank()) {
                next.add(icao.strip().toUpperCase());
            }
        }
        active = Collections.unmodifiableSet(next);
        return active;
    }

    public Set<String> active() { return active; }
}
