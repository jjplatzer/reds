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
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

public final class PlaybackBootstrapReader {
    private PlaybackBootstrapReader() {}

    public static BootstrapResult bootstrap(
            PlaybackConfig config,
            String airportRaw,
            String atRaw
    ) {
        if (!config.enabled) {
            return BootstrapResult.error(503, "playback_disabled");
        }

        String airport = normalizeAirport(airportRaw);
        if (airport == null) {
            return BootstrapResult.error(400, "invalid_airport");
        }

        Instant at;
        try {
            at = Instant.parse(atRaw);
        } catch (Exception e) {
            return BootstrapResult.error(400, "invalid_time");
        }

        Instant from = at.minus(Duration.ofMinutes(config.bootstrapLookbackMinutes));

        try {
            BootstrapState state = new BootstrapState(airport, at);
            List<Chunk> chunks = chunksForRange(config, airport, from, at.plusMillis(1));

            for (Chunk chunk : chunks) {
                readChunk(chunk.path, chunk.compressed, from, at, state);
            }

            if (state.baselineTime == null) {
                return BootstrapResult.error(404, "no_snapshot_before_time");
            }

            return BootstrapResult.ok(state.response());
        } catch (Exception e) {
            String message = e.getMessage();
            return BootstrapResult.error(500, message == null || message.isBlank() ? "bootstrap_failed" : message);
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
            Instant at,
            BootstrapState state
    ) throws Exception {
        try (InputStream raw = Files.newInputStream(path);
             InputStream input = compressed ? new ZstdInputStream(raw) : raw;
             BufferedReader reader = new BufferedReader(
                     new InputStreamReader(input, StandardCharsets.UTF_8)
             )) {
            String line;
            while ((line = reader.readLine()) != null) {
                if (line.isBlank()) continue;

                JsonObject record;
                try {
                    record = new JsonObject(line);
                } catch (Exception ignored) {
                    continue;
                }

                String updatedAtRaw = record.getString("updatedAt");
                if (updatedAtRaw == null || updatedAtRaw.isBlank()) continue;

                Instant updatedAt;
                try {
                    updatedAt = Instant.parse(updatedAtRaw);
                } catch (Exception ignored) {
                    continue;
                }

                if (updatedAt.isBefore(from) || updatedAt.isAfter(at)) {
                    continue;
                }

                state.apply(record, updatedAt);
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

    private static final class BootstrapState {
        private final String airport;
        private final Instant at;
        private final Map<String, JsonObject> targets = new LinkedHashMap<>();
        private Instant baselineTime;
        private int appliedRecords;

        private BootstrapState(String airport, Instant at) {
            this.airport = airport;
            this.at = at;
        }

        private void apply(JsonObject record, Instant updatedAt) {
            if (!airport.equals(record.getString("airport"))) return;

            String key = record.getString("key");
            if (key == null || key.isBlank()) return;

            String recordType = record.getString("recordType", "diff");

            if ("snapshot".equals(recordType)) {
                applySnapshot(record, updatedAt, key);
                return;
            }

            if (baselineTime == null || !updatedAt.isAfter(baselineTime)) {
                return;
            }

            if ("removed".equals(recordType) || record.getBoolean("removed", false)) {
                targets.remove(key);
                appliedRecords++;
                return;
            }

            JsonObject changed = record.getJsonObject("changed");
            if (changed == null || changed.isEmpty()) return;

            JsonObject target = targets.computeIfAbsent(key, ignored -> new JsonObject());
            mergeInto(target, changed);
            appliedRecords++;
        }

        private void applySnapshot(JsonObject record, Instant updatedAt, String key) {
            if (baselineTime == null || updatedAt.isAfter(baselineTime)) {
                baselineTime = updatedAt;
                targets.clear();
            }

            if (!updatedAt.equals(baselineTime)) {
                return;
            }

            JsonObject changed = record.getJsonObject("changed");
            if (changed == null || changed.isEmpty()) return;

            targets.put(key, changed.copy());
            appliedRecords++;
        }

        private JsonObject response() {
            JsonObject outTargets = new JsonObject();
            for (Map.Entry<String, JsonObject> e : targets.entrySet()) {
                outTargets.put(e.getKey(), e.getValue());
            }

            return new JsonObject()
                    .put("airport", airport)
                    .put("at", at.toString())
                    .put("baselineTime", baselineTime.toString())
                    .put("targetCount", targets.size())
                    .put("appliedRecords", appliedRecords)
                    .put("targets", outTargets);
        }
    }

    private static void mergeInto(JsonObject target, JsonObject changed) {
        for (String field : changed.fieldNames()) {
            Object value = changed.getValue(field);
            if (value == null) {
                target.remove(field);
            } else {
                target.put(field, value);
            }
        }
    }

    public record BootstrapResult(int statusCode, String contentType, String body) {
        static BootstrapResult ok(JsonObject body) {
            return new BootstrapResult(200, "application/json", body.encode());
        }

        static BootstrapResult error(int statusCode, String reason) {
            JsonObject body = new JsonObject()
                    .put("error", reason);
            return new BootstrapResult(statusCode, "application/json", body.encode());
        }
    }
}
