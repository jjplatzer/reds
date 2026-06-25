import ingest.PassThroughCodec;
import ingest.SmesConsumer;
import ingest.TargetBatch;
import io.vertx.core.DeploymentOptions;
import io.vertx.core.ThreadingModel;
import io.vertx.core.Vertx;
import live.WebSocketPush;
import playback.PlaybackRecorder;
import store.TargetStore;

/**
 * Entry point — wires the pipeline and deploys all verticles.
 *
 * Pipeline:
 *
 *   SmesConsumer ──── faa.ingest.observation ────► TargetStore ──── faa.persist.diff ────► WebSocketPush ──► UI
 *   (worker)                                       (merge, diff)   └─ faa.persist.record ─► PlaybackRecorder
 *
 * Deployment order:
 *   1. TargetStore — must be listening before observations arrive.
 *   2. PlaybackRecorder — records TargetStore frames when enabled.
 *   3. WebSocketPush — must be listening before diffs arrive.
 *   4. SmesConsumer — last, so downstream is ready to receive.
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
                .compose(v -> vertx.deployVerticle(new PlaybackRecorder()))
                .compose(v -> vertx.deployVerticle(new WebSocketPush()))
                .compose(v -> vertx.deployVerticle(new SmesConsumer(), workerOpts))
                .onFailure(err -> {
                    System.err.println("Startup failed: " + err.getMessage());
                    vertx.close();
                });
    }
}
