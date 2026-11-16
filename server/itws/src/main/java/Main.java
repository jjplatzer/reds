import ingest.ItwsConsumer;
import ingest.ItwsParser;
import io.vertx.core.Vertx;
import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;
import live.WebSocketPush;
import store.ItwsStore;

import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.util.concurrent.TimeUnit;

/** ITWS queue -> validated precipitation grids -> expiring HTTP/WebSocket products. */
public final class Main {
    private Main() {}

    public static void main(String[] args) throws Exception {
        if (args.length > 0) {
            if ("--help".equals(args[0])) {
                System.out.println("java -jar reds-itws-1.0-SNAPSHOT.jar [--parse FILE.xml ...]\n"
                        + "Without arguments, starts the ITWS JMS/HTTP/WebSocket service using .env/environment.\n"
                        + "--parse prints normalized product-9850 grids without connecting to SWIM.\n"
                        + "Defaults: 127.0.0.1:8084; /itws/ws, /itws/products, /itws/healthz.\n"
                        + "Snapshots/WebSockets accept ?site=I90 (comma-separated sites or ALL).\n"
                        + "Grid rows retain source order; geographic projection conventions remain unverified.");
                return;
            }
            if (!"--parse".equals(args[0]) || args.length < 2)
                throw new IllegalArgumentException("Usage: --parse FILE.xml [...], or no arguments to start the service");
            JsonArray products = new JsonArray();
            int ignored = 0, invalid = 0;
            for (int i = 1; i < args.length; i++) {
                Path file = Path.of(args[i]);
                if (Files.size(file) > ItwsParser.MAX_BYTES) throw new IllegalArgumentException("Oversized XML input");
                ItwsParser.Batch batch = ItwsParser.parse(Files.readAllBytes(file), Instant.now());
                batch.products().forEach(products::add);
                ignored += batch.ignoredMessages();
                invalid += batch.invalidMessages();
            }
            System.out.println(new JsonObject().put("products", products).put("ignoredMessages", ignored)
                    .put("invalidMessages", invalid).encodePrettily());
            return;
        }

        Vertx vertx = Vertx.vertx();
        ItwsConsumer consumer = new ItwsConsumer();
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            try { vertx.close().toCompletionStage().toCompletableFuture().get(5, TimeUnit.SECONDS); }
            catch (Exception ignored) { }
        }, "itws-shutdown"));
        vertx.deployVerticle(new ItwsStore())
                .compose(ignored -> vertx.deployVerticle(new WebSocketPush(consumer)))
                .compose(ignored -> vertx.deployVerticle(consumer))
                .onSuccess(ignored -> System.out.println("[ITWS] Service started"))
                .onFailure(error -> {
                    System.err.println("[ITWS] Startup failed (" + error.getClass().getSimpleName() + ")");
                    vertx.close().onComplete(ignored -> System.exit(1));
                });
    }
}
