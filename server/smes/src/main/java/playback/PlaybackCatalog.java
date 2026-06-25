package playback;

import io.vertx.core.json.JsonArray;
import io.vertx.core.json.JsonObject;

import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.time.LocalDate;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;

public final class PlaybackCatalog {
    private PlaybackCatalog() {}

    public static JsonObject availability(PlaybackConfig config, String airport) {
        final String normalizedAirport = normalizeAirport(airport);
        JsonArray chunks = new JsonArray();

        if (normalizedAirport == null || !config.enabled) {
            return new JsonObject()
                    .put("airport", normalizedAirport == null ? "" : normalizedAirport)
                    .put("chunks", chunks);
        }

        Path airportDir = config.dir.resolve(normalizedAirport);
        if (!Files.exists(airportDir)) {
            return new JsonObject()
                    .put("airport", normalizedAirport)
                    .put("chunks", chunks);
        }

        List<ChunkInfo> found = new ArrayList<>();

        try (var paths = Files.walk(airportDir)) {
            paths.filter(Files::isRegularFile)
                    .filter(path -> path.getFileName().toString().endsWith(".ndjson.zst"))
                    .forEach(path -> {
                        ChunkInfo info = parseChunk(config, normalizedAirport, path);
                        if (info != null) found.add(info);
                    });
        } catch (Exception e) {
            return new JsonObject()
                    .put("airport", normalizedAirport)
                    .put("error", e.getMessage())
                    .put("chunks", chunks);
        }

        found.sort(Comparator.comparing(info -> info.start));

        for (ChunkInfo info : found) {
            chunks.add(new JsonObject()
                    .put("start", info.start.toString())
                    .put("end", info.end.toString())
                    .put("path", info.relativePath));
        }

        return new JsonObject()
                .put("airport", normalizedAirport)
                .put("chunks", chunks);
    }

    private static ChunkInfo parseChunk(PlaybackConfig config, String airport, Path path) {
        try {
            Path relative = config.dir.relativize(path);
            if (relative.getNameCount() != 5) return null;

            String relAirport = relative.getName(0).toString();
            if (!airport.equals(relAirport)) return null;

            int year = Integer.parseInt(relative.getName(1).toString());
            int month = Integer.parseInt(relative.getName(2).toString());
            int day = Integer.parseInt(relative.getName(3).toString());

            String filename = relative.getName(4).toString();
            if (!filename.matches("[0-9]{4}\\.ndjson\\.zst")) return null;

            int hour = Integer.parseInt(filename.substring(0, 2));
            int minute = Integer.parseInt(filename.substring(2, 4));

            Instant start = LocalDate.of(year, month, day)
                    .atTime(hour, minute)
                    .toInstant(ZoneOffset.UTC);
            Instant end = start.plusSeconds(config.chunkMinutes * 60L);

            return new ChunkInfo(start, end, relative.toString());
        } catch (Exception ignored) {
            return null;
        }
    }

    private static String normalizeAirport(String airport) {
        if (airport == null) return null;
        airport = airport.strip().toUpperCase();
        if (!airport.matches("[A-Z]{4}")) return null;
        return airport;
    }

    private record ChunkInfo(Instant start, Instant end, String relativePath) {}
}
