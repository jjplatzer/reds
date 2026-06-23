import ingest.PassThroughCodec;
import ingest.SmesConsumer;
import ingest.TargetBatch;
import io.vertx.core.DeploymentOptions;
import io.vertx.core.ThreadingModel;
import io.vertx.core.Vertx;
import push.WebSocketPush;
import store.TargetStore;

/**
 * Entry point — wires the pipeline and deploys all verticles.
 *
 * Pipeline:
 *
 *   SmesConsumer ──── faa.ingest.observation ────► TargetStore ──── faa.persist.diff ────► WebSocketPush ──► UI
 *   (worker)                                       (merge, diff)                           (WebSocket)
 *
 * Deployment order:
 *   1. TargetStore — must be listening before observations arrive.
 *   2. WebSocketPush — must be listening before diffs arrive.
 *   3. SmesConsumer — last, so downstream is ready to receive.
 */
public final class Main {

    public static void main(String[] args) {
        Vertx vertx = Vertx.vertx();

        // Register pass-through codec for local EventBus delivery.
        vertx.eventBus().registerDefaultCodec(TargetBatch.class,
                new PassThroughCodec<>("TargetBatch"));

        DeploymentOptions workerOpts = new DeploymentOptions()
                .setThreadingModel(ThreadingModel.WORKER)
                .setMaxWorkerExecuteTime(Long.MAX_VALUE);

        vertx.deployVerticle(new TargetStore())
                .compose(v -> vertx.deployVerticle(new WebSocketPush()))
                .compose(v -> vertx.deployVerticle(new SmesConsumer(), workerOpts))
                .onFailure(err -> {
                    System.err.println("Startup failed: " + err.getMessage());
                    vertx.close();
                });
    }
}
