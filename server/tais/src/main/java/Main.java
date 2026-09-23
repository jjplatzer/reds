import ingest.PassThroughCodec;
import ingest.TaisBatch;
import ingest.TaisConsumer;
import io.vertx.core.Vertx;
import live.WebSocketPush;
import store.TrackStore;

/**
 * TAIS service entry point.
 *
 *   FAA SWIM / STDDS TAIS
 *       -> TaisConsumer
 *       -> EventBus
 *       -> TrackStore
 *       -> WebSocketPush
 *
 * The future ADS-B fusion layer should consume the normalized TAIS state
 * downstream of ingest rather than being mixed into the XML/JMS code.
 */
public final class Main {

    private Main() {}

    public static void main(String[] args) {
        Vertx vertx = Vertx.vertx();

        // TaisBatch is only sent inside this JVM, so pass it by reference rather
        // than serializing tens/hundreds of records for each SWIM message.
        vertx.eventBus().registerDefaultCodec(
                TaisBatch.class,
                new PassThroughCodec<>("TaisBatch")
        );

        // Store first so snapshots always have an authoritative owner. Start
        // the WebSocket listener before the JMS consumer, then no live update
        // can arrive before the push layer is subscribed.
        vertx.deployVerticle(new TrackStore())
                .compose(ignored -> vertx.deployVerticle(new WebSocketPush()))
                .compose(ignored -> vertx.deployVerticle(new TaisConsumer()))
                .onSuccess(ignored -> System.out.println("[TAIS] Service started"))
                .onFailure(err -> {
                    System.err.println("[TAIS] Startup failed: " + err.getMessage());
                    vertx.close();
                });
    }
}
