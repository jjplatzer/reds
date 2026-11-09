import ingest.AptcConsumer;
import ingest.AptcParser;
import io.vertx.core.Vertx;
import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;
import live.WebSocketPush;
import store.AptcStore;

import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.util.concurrent.TimeUnit;

/** Standalone TFMData/APTC consumer -> latest airport reports -> HTTP/WebSocket snapshots and updates. */
public final class Main {
    private Main() {}

    public static void main(String[] args) throws Exception {
        if (args.length > 0) {
            if ("--help".equals(args[0])) {
                System.out.println("java -jar reds-aptc-1.0-SNAPSHOT.jar [--parse FILE.xml ...]\n"
                        + "Without arguments, starts the APTC JMS/HTTP/WebSocket service using .env/environment.\n"
                        + "--parse prints normalized reports from local XML files without connecting to SWIM.");
                return;
            }
            if (!"--parse".equals(args[0]) || args.length < 2)
                throw new IllegalArgumentException("Usage: --parse FILE.xml [...], or no arguments to start the service");
            JsonArray configurations = new JsonArray();
            int ignored = 0, invalid = 0;
            for (int i = 1; i < args.length; i++) {
                AptcParser.Batch batch = AptcParser.parse(Files.readAllBytes(Path.of(args[i])), Instant.now());
                batch.configurations().forEach(configurations::add);
                ignored += batch.ignoredMessages();
                invalid += batch.invalidMessages();
            }
            System.out.println(new JsonObject().put("configurations", configurations)
                    .put("ignoredMessages", ignored).put("invalidMessages", invalid).encodePrettily());
            return;
        }

        Vertx vertx = Vertx.vertx();
        AptcConsumer consumer = new AptcConsumer();
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            try { vertx.close().toCompletionStage().toCompletableFuture().get(5, TimeUnit.SECONDS); }
            catch (Exception ignored) {}
        }, "aptc-shutdown"));
        vertx.deployVerticle(new AptcStore())
                .compose(ignored -> vertx.deployVerticle(new WebSocketPush(consumer)))
                .compose(ignored -> vertx.deployVerticle(consumer))
                .onSuccess(ignored -> System.out.println("[APTC] Service started"))
                .onFailure(error -> {
                    System.err.println("[APTC] Startup failed: " + error.getMessage());
                    vertx.close().onComplete(ignored -> System.exit(1));
                });
    }
}
