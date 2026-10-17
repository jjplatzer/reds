package live;

import io.vertx.core.http.ServerWebSocket;
import io.vertx.core.json.JsonObject;

import java.nio.charset.StandardCharsets;
import java.util.ArrayDeque;

/**
 * One TAIS WebSocket client; all methods run on the WebSocket verticle's event loop.
 *
 * A new client buffers live frames until the authoritative snapshot arrives.
 * Frames with revisions already represented by that snapshot are discarded.
 * Thereafter one bounded FIFO feeds the asynchronous WebSocket writer. A full
 * Vert.x write queue pauses the drain; it is NOT an immediate disconnect.
 */
final class ClientConnection {

    // Match net/tais.go: taisMaxMessageBytes. Protocol-v1 has no snapshot
    // chunking, so fail explicitly if a single message exceeds the reader cap.
    private static final int MAX_FRAME_BYTES = 16 << 20;

    private record EncodedFrame(String text, int bytes) {}

    private final ServerWebSocket socket;
    private final String facility;
    private final int pendingMax;
    private final int outboundMaxFrames;
    private final long outboundMaxBytes;
    private final ArrayDeque<JsonObject> pending = new ArrayDeque<>();
    private final ArrayDeque<EncodedFrame> outbound = new ArrayDeque<>();

    private long outboundBytes;
    private boolean ready;
    private boolean closed;

    ClientConnection(ServerWebSocket socket, String facility, int pendingMax,
                     int outboundMaxFrames, long outboundMaxBytes) {
        this.socket = socket;
        this.facility = facility;
        this.pendingMax = pendingMax;
        this.outboundMaxFrames = outboundMaxFrames;
        this.outboundMaxBytes = outboundMaxBytes;
        // Vert.x calls this after the underlying write queue becomes writable.
        socket.drainHandler(ignored -> flush());
    }

    String facility() {
        return facility;
    }

    void onLiveFrame(JsonObject frame) {
        if (closed || !matchesFacility(frame)) return;

        if (ready) {
            if (enqueue(frame)) flush();
            return;
        }

        if (pending.size() >= pendingMax) {
            System.err.println("[TAIS ws] Closing client: pre-snapshot buffer full" +
                    " facility=" + facility + " pending=" + pending.size());
            close((short) 1013, "TAIS snapshot backlog exceeded");
            return;
        }

        pending.addLast(frame);
    }

    void onSnapshot(JsonObject snapshot) {
        if (closed) return;
        long started = System.nanoTime();
        Long revisionValue = snapshot.getLong("revision");
        long snapshotRevision = revisionValue == null ? 0L : revisionValue;
        int targetCount = snapshot.getJsonArray("targets") == null
                ? 0 : snapshot.getJsonArray("targets").size();
        int pendingCount = pending.size();

        // Encode and enqueue the snapshot *before* subsequent revisions.
        // Do not flood the Vert.x socket with pending frames before checking
        // whether its write queue is actually writable.
        if (!enqueue(snapshot)) return;
        int snapshotBytes = outbound.peekLast().bytes();
        // Submit the snapshot first. It is commonly much larger than a live
        // update, and retaining it alongside the entire replay burst wastes
        // memory and can exceed the application queue's byte budget.
        flush();
        if (closed) return;

        while (!pending.isEmpty() && !closed) {
            JsonObject frame = pending.removeFirst();
            Long revision = frame.getLong("revision");
            if (revision != null && revision > snapshotRevision && !enqueue(frame)) {
                return;
            }
        }

        ready = !closed;
        System.out.println("[TAIS ws] Snapshot queued; facility=" + facility +
                " revision=" + snapshotRevision + " targets=" + targetCount +
                " bytes=" + snapshotBytes + " pending=" + pendingCount +
                " queuedFrames=" + outbound.size() +
                " queuedBytes=" + outboundBytes +
                " durationMs=" + ((System.nanoTime() - started) / 1_000_000));
        flush();
    }

    private boolean matchesFacility(JsonObject frame) {
        String frameFacility = frame.getString("facility");
        return frameFacility != null && facility.equalsIgnoreCase(frameFacility);
    }

    void markClosed() {
        closed = true;
        pending.clear();
        outbound.clear();
        outboundBytes = 0;
    }

    void close() {
        close((short) 1000, "");
    }

    private void close(short code, String reason) {
        if (closed) return;
        markClosed();
        try {
            socket.close(code, reason);
        } catch (Exception ignored) {
            // The exception/close handler will remove the client regardless.
        }
    }

    private boolean enqueue(JsonObject frame) {
        if (closed) return false;

        // Retain encoded text rather than JSON trees while waiting for drain.
        // The temporary UTF-8 conversion gives an exact wire-size bound.
        String text = frame.encode();
        int bytes = text.getBytes(StandardCharsets.UTF_8).length;
        if (bytes > MAX_FRAME_BYTES) {
            System.err.println("[TAIS ws] Closing client: frame exceeds Go read limit" +
                    " facility=" + facility + " bytes=" + bytes);
            close((short) 1009, "TAIS message too large");
            return false;
        }
        if (outbound.size() >= outboundMaxFrames || bytes > outboundMaxBytes - outboundBytes) {
            System.err.println("[TAIS ws] Closing slow client: outbound backlog exceeded" +
                    " facility=" + facility + " frames=" + outbound.size() +
                    " bytes=" + outboundBytes + " nextBytes=" + bytes);
            close((short) 1013, "TAIS write backlog exceeded");
            return false;
        }

        outbound.addLast(new EncodedFrame(text, bytes));
        outboundBytes += bytes;
        return true;
    }

    private void flush() {
        // No busy waiting: leave the remainder in our bounded FIFO and rely on
        // drainHandler to resume when the transport's buffer has drained.
        while (!closed && !outbound.isEmpty() && !socket.writeQueueFull()) {
            EncodedFrame frame = outbound.removeFirst();
            outboundBytes -= frame.bytes();
            try {
                socket.writeTextMessage(frame.text()).onFailure(err -> {
                    if (!closed) {
                        System.err.println("[TAIS ws] Write failed; facility=" + facility +
                                " error=" + err.getMessage());
                        close((short) 1011, "TAIS write failure");
                    }
                });
            } catch (Exception err) {
                System.err.println("[TAIS ws] Write failed; facility=" + facility +
                        " error=" + err.getMessage());
                close((short) 1011, "TAIS write failure");
            }
        }
    }
}
