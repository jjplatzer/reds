import ingest.PassThroughCodec;
import ingest.SfdpsConsumer;
import io.vertx.core.Vertx;
import live.WebSocketPush;
import messages.SfdpsBatch;
import store.SfdpsStore;

/**
 * SFDPS service entry point.
 *
 * Four independent SWIM subscriptions:
 *   Flight FIXM ─┐
 *   Airspace AIXM├─► SfdpsConsumer/parsers ─► SfdpsStore ─► WebSocketPush
 *   General XML ─┤                              │
 *   Status XML ──┘                              └─ future FUSION consumer
 *
 * The service intentionally ends at authoritative ERAM/SFDPS state. adsb.lol
 * fusion belongs downstream of SfdpsStore so FAA state is never overwritten by
 * external surveillance.
 */
public final class Main {
    private Main() {}

    public static void main(String[] args) {
        Vertx vertx = Vertx.vertx();
        vertx.eventBus().registerDefaultCodec(SfdpsBatch.class, new PassThroughCodec<>("SfdpsBatch"));

        vertx.deployVerticle(new SfdpsStore())
                .compose(ignored -> vertx.deployVerticle(new WebSocketPush()))
                .compose(ignored -> vertx.deployVerticle(new SfdpsConsumer()))
                .onSuccess(ignored -> System.out.println("[SFDPS] Service started"))
                .onFailure(err -> {
                    System.err.println("[SFDPS] Startup failed: " + err.getMessage());
                    vertx.close();
                });
    }
}
