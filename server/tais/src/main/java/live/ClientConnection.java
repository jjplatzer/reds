package live;

import io.vertx.core.http.ServerWebSocket;
import io.vertx.core.json.JsonObject;

import java.util.ArrayDeque;

/**
 * One TAIS WebSocket client.
 *
 * A newly connected client buffers live events until its authoritative store
 * snapshot has arrived. Events at or before the snapshot revision are then
 * discarded; newer events are flushed in order. This closes the otherwise
 * unavoidable snapshot-vs-live race.
 */
final class ClientConnection {

    private final ServerWebSocket socket;
    private final int pendingMax;
    private final ArrayDeque<JsonObject> pending = new ArrayDeque<>();

    private boolean ready;
    private boolean closed;

    ClientConnection(ServerWebSocket socket, int pendingMax) {
        this.socket = socket;
        this.pendingMax = pendingMax;
    }

    void onLiveFrame(JsonObject frame) {
        if (closed) return;

        if (ready) {
            send(frame);
            return;
        }

        if (pending.size() >= pendingMax) {
            // A slow client should reconnect and obtain a fresh snapshot rather
            // than receive an arbitrarily stale airborne picture.
            System.err.println("[TAIS ws] Closing client: pre-snapshot buffer full");
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
            Long revision = frame.getLong("revision");
            if (revision != null && revision > snapshotRevision) {
                send(frame);
            }
        }

        ready = !closed;
    }

    void markClosed() {
        closed = true;
        pending.clear();
    }

    void close() {
        if (closed) return;
        closed = true;
        pending.clear();
        try {
            socket.close();
        } catch (Exception ignored) {
        }
    }

    private boolean send(JsonObject frame) {
        if (closed) return false;

        // For a real-time scope, disconnecting and resnapshotting is preferable
        // to building an unbounded stale write backlog.
        if (socket.writeQueueFull()) {
            System.err.println("[TAIS ws] Closing slow client: socket write queue full");
            close();
            return false;
        }

        socket.writeTextMessage(frame.encode());
        return true;
    }
}
