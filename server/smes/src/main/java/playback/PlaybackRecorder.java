package playback;

import com.github.luben.zstd.ZstdOutputStream;
import io.vertx.core.AbstractVerticle;
import io.vertx.core.json.JsonObject;
import store.TargetStore;

import java.io.BufferedWriter;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.DirectoryStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.time.ZoneOffset;
import java.time.ZonedDateTime;
import java.util.HashMap;
import java.util.Iterator;
import java.util.Map;
import java.util.Objects;

public final class PlaybackRecorder extends AbstractVerticle {
    private final Map<String, ChunkWriter> writers = new HashMap<>();
    private PlaybackConfig config;

    @Override
    public void start() {
        config = PlaybackConfig.fromEnv();

        if (!config.enabled) {
            System.out.println("[Playback] Disabled");
            return;
        }

        try {
            Files.createDirectories(config.dir);
        } catch (Exception e) {
            throw new RuntimeException("Could not create playback dir " + config.dir, e);
        }

        System.out.println("[Playback] Recording to " + config.dir
                + " chunkMinutes=" + config.chunkMinutes
                + " retentionHours=" + config.localRetentionHours
                + " zstd=" + config.zstdEnabled);

        vertx.eventBus().<JsonObject>consumer(TargetStore.RECORD_ADDRESS, msg -> {
            try {
                record(msg.body());
            } catch (Exception e) {
                System.err.println("[Playback] Record failed: " + e.getMessage());
            }
        });

        vertx.setPeriodic(Math.max(1, config.flushIntervalSeconds) * 1000L, ignored -> flushAll());
        vertx.setPeriodic(60_000L, ignored -> cleanupOldFiles());
    }

    @Override
    public void stop() {
        for (ChunkWriter writer : writers.values()) {
            try {
                writer.closeAndCompress(config);
            } catch (Exception ignored) {
            }
        }
        writers.clear();
    }

    private void record(JsonObject frame) throws Exception {
        String airport = frame.getString("airport", "").strip().toUpperCase();
        if (!airport.matches("[A-Z]{4}")) return;

        Instant timestamp = parseInstant(frame.getString("updatedAt"));
        ChunkId chunk = ChunkId.from(airport, timestamp, config.chunkMinutes);

        String mapKey = airport + ":" + chunk.fileStem;
        ChunkWriter writer = writers.get(mapKey);

        if (writer == null || !writer.chunk.equals(chunk)) {
            closeOldWriterForAirport(airport);
            writer = ChunkWriter.open(config, chunk);
            writers.put(mapKey, writer);
        }

        writer.write(frame.encode());
    }

    private void closeOldWriterForAirport(String airport) {
        Iterator<Map.Entry<String, ChunkWriter>> it = writers.entrySet().iterator();
        while (it.hasNext()) {
            Map.Entry<String, ChunkWriter> entry = it.next();
            ChunkWriter writer = entry.getValue();
            if (!writer.chunk.airport.equals(airport)) continue;

            try {
                writer.closeAndCompress(config);
            } catch (Exception e) {
                System.err.println("[Playback] Close/compress failed: " + e.getMessage());
            }
            it.remove();
        }
    }

    private void flushAll() {
        for (ChunkWriter writer : writers.values()) {
            try {
                writer.flush();
            } catch (Exception e) {
                System.err.println("[Playback] Flush failed: " + e.getMessage());
            }
        }
    }

    private void cleanupOldFiles() {
        if (config.localRetentionHours <= 0) return;

        Instant cutoff = Instant.now().minusSeconds(config.localRetentionHours * 3600L);
        try {
            cleanupRecursive(config.dir, cutoff);
        } catch (Exception e) {
            System.err.println("[Playback] Cleanup failed: " + e.getMessage());
        }
    }

    private void cleanupRecursive(Path dir, Instant cutoff) throws Exception {
        if (!Files.exists(dir)) return;

        try (DirectoryStream<Path> stream = Files.newDirectoryStream(dir)) {
            for (Path path : stream) {
                if (Files.isDirectory(path)) {
                    cleanupRecursive(path, cutoff);
                    tryDeleteEmptyDir(path);
                    continue;
                }

                String name = path.getFileName().toString();
                if (!name.endsWith(".ndjson.zst") && !name.endsWith(".ndjson.tmp")) continue;

                Instant modified = Files.getLastModifiedTime(path).toInstant();
                if (modified.isBefore(cutoff)) {
                    Files.deleteIfExists(path);
                }
            }
        }
    }

    private void tryDeleteEmptyDir(Path dir) {
        try (DirectoryStream<Path> stream = Files.newDirectoryStream(dir)) {
            if (!stream.iterator().hasNext()) Files.deleteIfExists(dir);
        } catch (Exception ignored) {
        }
    }

    private static Instant parseInstant(String value) {
        if (value == null || value.isBlank()) return Instant.now();
        try {
            return Instant.parse(value);
        } catch (Exception ignored) {
            return Instant.now();
        }
    }

    private static final class ChunkId {
        final String airport;
        final int year;
        final int month;
        final int day;
        final int hour;
        final int minuteBucket;
        final String fileStem;

        private ChunkId(String airport, int year, int month, int day, int hour, int minuteBucket) {
            this.airport = airport;
            this.year = year;
            this.month = month;
            this.day = day;
            this.hour = hour;
            this.minuteBucket = minuteBucket;
            this.fileStem = String.format("%02d%02d", hour, minuteBucket);
        }

        static ChunkId from(String airport, Instant timestamp, int chunkMinutes) {
            ZonedDateTime z = timestamp.atZone(ZoneOffset.UTC);
            int minute = z.getMinute();
            int bucket = (minute / chunkMinutes) * chunkMinutes;

            return new ChunkId(
                    airport,
                    z.getYear(),
                    z.getMonthValue(),
                    z.getDayOfMonth(),
                    z.getHour(),
                    bucket
            );
        }

        Path tmpPath(PlaybackConfig config) {
            return config.dir
                    .resolve(airport)
                    .resolve(String.format("%04d", year))
                    .resolve(String.format("%02d", month))
                    .resolve(String.format("%02d", day))
                    .resolve(fileStem + ".ndjson.tmp");
        }

        Path zstPath(PlaybackConfig config) {
            return config.dir
                    .resolve(airport)
                    .resolve(String.format("%04d", year))
                    .resolve(String.format("%02d", month))
                    .resolve(String.format("%02d", day))
                    .resolve(fileStem + ".ndjson.zst");
        }

        @Override
        public boolean equals(Object other) {
            if (this == other) return true;
            if (!(other instanceof ChunkId chunkId)) return false;
            return year == chunkId.year
                    && month == chunkId.month
                    && day == chunkId.day
                    && hour == chunkId.hour
                    && minuteBucket == chunkId.minuteBucket
                    && airport.equals(chunkId.airport);
        }

        @Override
        public int hashCode() {
            return Objects.hash(airport, year, month, day, hour, minuteBucket);
        }
    }

    private static final class ChunkWriter {
        final ChunkId chunk;
        final Path tmpPath;
        final BufferedWriter writer;

        private ChunkWriter(ChunkId chunk, Path tmpPath, BufferedWriter writer) {
            this.chunk = chunk;
            this.tmpPath = tmpPath;
            this.writer = writer;
        }

        static ChunkWriter open(PlaybackConfig config, ChunkId chunk) throws Exception {
            Path tmp = chunk.tmpPath(config);
            Files.createDirectories(tmp.getParent());

            BufferedWriter writer = Files.newBufferedWriter(
                    tmp,
                    StandardCharsets.UTF_8,
                    java.nio.file.StandardOpenOption.CREATE,
                    java.nio.file.StandardOpenOption.APPEND
            );

            return new ChunkWriter(chunk, tmp, writer);
        }

        void write(String line) throws Exception {
            writer.write(line);
            writer.newLine();
        }

        void flush() throws Exception {
            writer.flush();
        }

        void closeAndCompress(PlaybackConfig config) throws Exception {
            writer.flush();
            writer.close();

            if (!Files.exists(tmpPath) || Files.size(tmpPath) == 0) {
                Files.deleteIfExists(tmpPath);
                return;
            }

            if (!config.zstdEnabled) return;

            Path zst = chunk.zstPath(config);
            Files.createDirectories(zst.getParent());

            try (InputStream in = Files.newInputStream(tmpPath);
                 OutputStream raw = Files.newOutputStream(
                         zst,
                         java.nio.file.StandardOpenOption.CREATE,
                         java.nio.file.StandardOpenOption.TRUNCATE_EXISTING
                 );
                 ZstdOutputStream out = new ZstdOutputStream(raw)) {
                in.transferTo(out);
            }

            Files.deleteIfExists(tmpPath);
        }
    }
}
