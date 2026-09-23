import ingest.PassThroughCodec;
import ingest.TaisBatch;
import ingest.TaisConsumer;
import io.vertx.core.Vertx;
import store.TrackStore;

/**
 * Local TAIS service entry point.
 *
 * For now this deliberately stops at the ingest/cache boundary:
 *
 *   FAA SWIM / STDDS TAIS -> TaisConsumer -> EventBus -> TrackStore
 *
 * The future ADS-B fusion layer should consume the same normalized TAIS
 * observations rather than being mixed into the XML/JMS code.
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

        // Store first so no observations can arrive before the history cache is
        // listening. The consumer owns a dedicated blocking JMS thread.
        vertx.deployVerticle(new TrackStore())
                .compose(ignored -> vertx.deployVerticle(new TaisConsumer()))
                .onSuccess(ignored -> System.out.println("[TAIS] Service started"))
                .onFailure(err -> {
                    System.err.println("[TAIS] Startup failed: " + err.getMessage());
                    vertx.close();
                });
    }
}
