package live;

import io.vertx.core.http.ServerWebSocket;
import io.vertx.core.json.JsonObject;

import java.util.ArrayDeque;

/** Snapshot/live race-safe SFDPS WebSocket connection scoped to one ARTCC. */
final class ClientConnection {
    private final ServerWebSocket socket;
    private final String facility;
    private final int pendingMax;
    private final ArrayDeque<JsonObject> pending = new ArrayDeque<>();
    private boolean ready;
    private boolean closed;

    ClientConnection(ServerWebSocket socket, String facility, int pendingMax) {
        this.socket = socket;
        this.facility = facility;
        this.pendingMax = pendingMax;
    }

    String facility() { return facility; }

    void onLiveFrame(JsonObject frame) {
        if (closed || !matchesFacility(frame)) return;
        if (ready) { send(frame); return; }
        if (pending.size() >= pendingMax) {
            System.err.println("[SFDPS ws] Closing client: pre-snapshot buffer full");
            close();
            return;
        }
        pending.addLast(frame);
    }

    void onSnapshot(JsonObject snapshot) {
        if (closed) return;
        Long revisionValue = snapshot.getLong("revision");
        long snapshotRevision = revisionValue == null ? 0L : revisionValue;
        if (!send(snapshot)) return;
        while (!pending.isEmpty() && !closed) {
            JsonObject frame = pending.removeFirst();
            Long frameRevision = frame.getLong("revision");
            if (frameRevision != null && frameRevision > snapshotRevision) send(frame);
        }
        ready = !closed;
    }

    private boolean matchesFacility(JsonObject frame) {
        String value = frame.getString("facility");
        if (value == null) return false;
        String normalized = value.strip().toUpperCase(java.util.Locale.ROOT);
        if ("ALL".equals(normalized) || "NA".equals(normalized) || facility.equalsIgnoreCase(normalized)) return true;
        for (String part : normalized.split("[\\s,;/]+")) {
            if (facility.equalsIgnoreCase(part)) return true;
        }
        return false;
    }

    void markClosed() { closed = true; pending.clear(); }
    void close() {
        if (closed) return;
        closed = true;
        pending.clear();
        try { socket.close(); } catch (Exception ignored) {}
    }

    private boolean send(JsonObject frame) {
        if (closed) return false;
        if (socket.writeQueueFull()) {
            System.err.println("[SFDPS ws] Closing slow client: socket write queue full");
            close();
            return false;
        }
        socket.writeTextMessage(frame.encode());
        return true;
    }
}
