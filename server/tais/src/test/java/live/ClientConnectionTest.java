package live;

import io.vertx.core.Future;
import io.vertx.core.Handler;
import io.vertx.core.http.ServerWebSocket;
import io.vertx.core.json.JsonObject;
import org.junit.jupiter.api.Test;

import java.lang.reflect.InvocationHandler;
import java.lang.reflect.Proxy;
import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.*;

class ClientConnectionTest {

    @Test
    void snapshotComesFirstAndOnlyNewerRevisionsAreReplayedAfterDrain() {
        FakeSocket fake = new FakeSocket();
        fake.full = true;
        ClientConnection client = new ClientConnection(fake.socket, "N90", 8, 8, 1024 * 1024);

        client.onLiveFrame(update(9));   // already covered by snapshot
        client.onLiveFrame(update(11));
        client.onLiveFrame(update(12));
        client.onLiveFrame(new JsonObject().put("facility", "A90").put("revision", 13));
        client.onSnapshot(snapshot(10));

        assertTrue(fake.sent.isEmpty(), "transport is full: nothing should be submitted");
        fake.full = false;
        fake.drain();

        assertEquals(List.of("snapshot:10", "update:11", "update:12"), fake.sent);
        assertNull(fake.closeCode);
    }

    @Test
    void pausesAfterSnapshotWhenTransportBecomesFullThenResumesInOrder() {
        FakeSocket fake = new FakeSocket();
        fake.fillAfterFirstWrite = true;
        ClientConnection client = new ClientConnection(fake.socket, "N90", 8, 8, 1024 * 1024);
        client.onLiveFrame(update(6));
        client.onSnapshot(snapshot(5));
        assertEquals(List.of("snapshot:5"), fake.sent);

        client.onLiveFrame(update(7));
        assertEquals(List.of("snapshot:5"), fake.sent);
        assertNull(fake.closeCode, "a full write queue alone must not disconnect");

        fake.full = false;
        fake.drain();
        assertEquals(List.of("snapshot:5", "update:6", "update:7"), fake.sent);
    }

    @Test
    void boundedApplicationQueueClosesWithRetryableCode() {
        FakeSocket fake = new FakeSocket();
        fake.full = true;
        ClientConnection client = new ClientConnection(fake.socket, "N90", 8, 2, 1024 * 1024);
        client.onLiveFrame(update(11));
        client.onLiveFrame(update(12));
        client.onSnapshot(snapshot(10));

        assertEquals(Short.valueOf((short) 1013), fake.closeCode);
        assertTrue(fake.sent.isEmpty());
        fake.full = false;
        fake.drain();
        assertTrue(fake.sent.isEmpty(), "closed clients must not flush stale data");
    }

    @Test
    void byteBudgetIsEnforcedIndependentlyOfFrameCount() {
        FakeSocket fake = new FakeSocket();
        fake.full = true;
        ClientConnection client = new ClientConnection(fake.socket, "N90", 8, 8, 150);
        client.onSnapshot(snapshot(1));
        for (int i = 2; i < 20 && fake.closeCode == null; i++) {
            client.onLiveFrame(update(i));
        }
        assertEquals(Short.valueOf((short) 1013), fake.closeCode);
    }

    private static JsonObject snapshot(long revision) {
        return new JsonObject()
                .put("type", "snapshot").put("facility", "N90")
                .put("revision", revision);
    }

    private static JsonObject update(long revision) {
        return new JsonObject()
                .put("type", "update").put("facility", "N90")
                .put("revision", revision);
    }

    private static final class FakeSocket implements InvocationHandler {
        final List<String> sent = new ArrayList<>();
        final ServerWebSocket socket = (ServerWebSocket) Proxy.newProxyInstance(
                ServerWebSocket.class.getClassLoader(),
                new Class<?>[]{ServerWebSocket.class}, this);
        Handler<Void> drainHandler;
        boolean full;
        boolean fillAfterFirstWrite;
        Short closeCode;

        void drain() {
            assertNotNull(drainHandler);
            drainHandler.handle(null);
        }

        @Override
        @SuppressWarnings("unchecked")
        public Object invoke(Object proxy, java.lang.reflect.Method method, Object[] args) {
            switch (method.getName()) {
                case "drainHandler":
                    drainHandler = (Handler<Void>) args[0];
                    return socket;
                case "writeQueueFull":
                    return full;
                case "writeTextMessage":
                    JsonObject frame = new JsonObject((String) args[0]);
                    sent.add(frame.getString("type") + ":" + frame.getLong("revision"));
                    if (fillAfterFirstWrite && sent.size() == 1) full = true;
                    return Future.succeededFuture();
                case "close":
                    closeCode = args != null && args.length > 0 ? (Short) args[0] : (short) 1000;
                    return Future.succeededFuture();
                case "toString":
                    return "FakeSocket";
                default:
                    throw new AssertionError("Unexpected WebSocket method: " + method.getName());
            }
        }
    }
}
