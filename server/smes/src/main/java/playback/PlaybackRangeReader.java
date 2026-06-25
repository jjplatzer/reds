package playback;

import com.github.luben.zstd.ZstdInputStream;
import io.vertx.core.json.JsonObject;

import java.io.BufferedReader;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.time.LocalDate;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;

public final class PlaybackRangeReader {
    private PlaybackRangeReader() {}

    public static RangeResult readRange(
            PlaybackConfig config,
            String airportRaw,
            String fromRaw,
            String toRaw
    ) {
        if (!config.enabled) {
            return RangeResult.error(503, "playback_disabled");
        }

        String airport = normalizeAirport(airportRaw);
        if (airport == null) {
            return RangeResult.error(400, "invalid_airport");
        }

        Instant from;
        Instant to;
        try {
            from = Instant.parse(fromRaw);
            to = Instant.parse(toRaw);
        } catch (Exception e) {
            return RangeResult.error(400, "invalid_time");
        }

        if (!to.isAfter(from)) {
            return RangeResult.error(400, "invalid_range");
        }

        Duration range = Duration.between(from, to);
        if (range.compareTo(Duration.ofMinutes(config.maxRangeMinutes)) > 0) {
            return RangeResult.error(413, "range_too_large");
        }

        try {
            List<Chunk> chunks = chunksForRange(config, airport, from, to);
            OutputAccumulator out = new OutputAccumulator(config.maxResponseBytes);

            for (Chunk chunk : chunks) {
                readChunk(chunk.path, chunk.compressed, from, to, out);
            }

            return RangeResult.ok(out.body());
        } catch (RangeTooLargeException e) {
            return RangeResult.error(413, "response_too_large");
        } catch (Exception e) {
            String message = e.getMessage();
            return RangeResult.error(500, message == null || message.isBlank() ? "range_read_failed" : message);
        }
    }

    private static List<Chunk> chunksForRange(
            PlaybackConfig config,
            String airport,
            Instant from,
            Instant to
    ) throws Exception {
        Path airportDir = config.dir.resolve(airport);
        List<Chunk> chunks = new ArrayList<>();

        if (!Files.exists(airportDir)) {
            return chunks;
        }

        try (var paths = Files.walk(airportDir)) {
            paths.filter(Files::isRegularFile)
                    .filter(path -> {
                        String name = path.getFileName().toString();
                        return name.endsWith(".ndjson.zst") || name.endsWith(".ndjson.tmp");
                    })
                    .forEach(path -> {
                        Chunk chunk = parseChunk(config, airport, path);
                        if (chunk == null) return;
                        if (chunk.end.isAfter(from) && chunk.start.isBefore(to)) {
                            chunks.add(chunk);
                        }
                    });
        }

        chunks.sort(Comparator.comparing(chunk -> chunk.start));
        return chunks;
    }

    private static Chunk parseChunk(PlaybackConfig config, String airport, Path path) {
        try {
            Path relative = config.dir.relativize(path);
            if (relative.getNameCount() != 5) return null;

            String relAirport = relative.getName(0).toString();
            if (!airport.equals(relAirport)) return null;

            int year = Integer.parseInt(relative.getName(1).toString());
            int month = Integer.parseInt(relative.getName(2).toString());
            int day = Integer.parseInt(relative.getName(3).toString());

            String filename = relative.getName(4).toString();
            boolean compressed;
            String stem;

            if (filename.matches("[0-9]{4}\\.ndjson\\.zst")) {
                compressed = true;
                stem = filename.substring(0, 4);
            } else if (filename.matches("[0-9]{4}\\.ndjson\\.tmp")) {
                compressed = false;
                stem = filename.substring(0, 4);
            } else {
                return null;
            }

            int hour = Integer.parseInt(stem.substring(0, 2));
            int minute = Integer.parseInt(stem.substring(2, 4));

            Instant start = LocalDate.of(year, month, day)
                    .atTime(hour, minute)
                    .toInstant(ZoneOffset.UTC);
            Instant end = start.plusSeconds(config.chunkMinutes * 60L);

            return new Chunk(path, start, end, compressed);
        } catch (Exception ignored) {
            return null;
        }
    }

    private static void readChunk(
            Path path,
            boolean compressed,
            Instant from,
            Instant to,
            OutputAccumulator out
    ) throws Exception {
        try (InputStream raw = Files.newInputStream(path);
             InputStream input = compressed ? new ZstdInputStream(raw) : raw;
             BufferedReader reader = new BufferedReader(
                     new InputStreamReader(input, StandardCharsets.UTF_8)
             )) {
            String line;
            while ((line = reader.readLine()) != null) {
                if (line.isBlank()) continue;

                JsonObject obj;
                try {
                    obj = new JsonObject(line);
                } catch (Exception ignored) {
                    continue;
                }

                String updatedAtRaw = obj.getString("updatedAt");
                if (updatedAtRaw == null || updatedAtRaw.isBlank()) continue;

                Instant updatedAt;
                try {
                    updatedAt = Instant.parse(updatedAtRaw);
                } catch (Exception ignored) {
                    continue;
                }

                if (updatedAt.isBefore(from) || !updatedAt.isBefore(to)) {
                    continue;
                }

                out.appendLine(line);
            }
        }
    }

    private static String normalizeAirport(String airport) {
        if (airport == null) return null;
        airport = airport.strip().toUpperCase();
        if (!airport.matches("[A-Z]{4}")) return null;
        return airport;
    }

    private record Chunk(Path path, Instant start, Instant end, boolean compressed) {}

    private static final class OutputAccumulator {
        private final StringBuilder body = new StringBuilder();
        private final int maxBytes;
        private int bytes;

        private OutputAccumulator(int maxBytes) {
            this.maxBytes = Math.max(1, maxBytes);
        }

        private void appendLine(String line) {
            int nextBytes = line.getBytes(StandardCharsets.UTF_8).length + 1;
            if (bytes + nextBytes > maxBytes) {
                throw new RangeTooLargeException();
            }

            body.append(line).append('\n');
            bytes += nextBytes;
        }

        private String body() {
            return body.toString();
        }
    }

    private static final class RangeTooLargeException extends RuntimeException {}

    public record RangeResult(int statusCode, String contentType, String body) {
        static RangeResult ok(String body) {
            return new RangeResult(200, "application/x-ndjson", body);
        }

        static RangeResult error(int statusCode, String reason) {
            JsonObject body = new JsonObject()
                    .put("error", reason);
            return new RangeResult(statusCode, "application/json", body.encode());
        }
    }
}
