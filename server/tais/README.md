# REDS TAIS consumer

This service is the airborne STARS/TAIS ingest boundary for REDS. It currently does two things only:

1. consumes STDDS TAIS `TATrackAndFlightPlan` SimpleXML from the SCDS Solace queue;
2. normalizes the records and keeps current state plus a short raw position history per `(facility, trackNum)`.

It intentionally does **not** perform ADS-B fusion. The WebSocket layer exposes normalized TAIS state without coupling surveillance logic to JMS/XML.

## Why SimpleXML

The FAA *FIXM Mediated STDDS Data Overview v2* identifies TAIS `TATrackAndFlightPlan` as the source message for `TAIS_TRACK_AND_FLIGHT_PLAN` and maps fields such as `mrtTime`, `acAddress`, `vx/vy`, `lat/lon`, `reportedAltitude`, flight-plan fields, `/TATrackAndFlightPlan/src`, and `trackNum` into FIXM. The live v4 SCDS feed used by REDS is the source SimpleXML form, with a root like:

```xml
<ns2:TATrackAndFlightPlan
    xmlns:ns2="urn:us:gov:dot:faa:atm:terminal:entities:v4-0:tais:terminalautomationinformation">
    <src>N90</src>
    <record>...</record>
</ns2:TATrackAndFlightPlan>
```

The parser is namespace-aware but keys on local element names so a namespace-prefix change does not break ingest.

## Track identity and history

The canonical TAIS key is `(facility, trackNum)`, rendered as e.g. `N90:T3507`. `trackNum` must not be treated as nationally unique.

STARS HISTORY/H_RATE is a display-side function. HISTORY supports up to 10 dots. H_RATE is a minimum elapsed time; STARS waits until the next radar position after the threshold before adding the next history dot. The public Vice STARS implementation documents this behavior and references STARS manual page 4-94. It also uses a 4.5 s default H_RATE, which produces a 5 s effective history-dot cadence in 1 Hz FUSED mode.

For that reason this server **does not downsample to 5 seconds**. It retains every distinct TAIS `mrtTime` in a bounded raw ring. The future STARS client/fusion layer can then create the exact requested 0..10-dot trail without information already having been thrown away by ingest.

Reference: https://pharr.org/vice/#stars-track-ids-history

## Local configuration

The service reads real environment variables first and then the nearest `.env` file (searching upward from the working directory). The shared SCDS credentials stay in the repository-root `.env`, which is gitignored.

Required values:

```env
TAIS_JMS_URL=tcps://...
TAIS_VPN=STDDS
SCDS_USERNAME=...
SCDS_PASSWORD=...
TAIS_QUEUE=...

# Defensive local allow-list. Keep the SWIFT subscription itself server-side
# filtered as well; a local filter cannot recover the low-latency cadence.
TAIS_FACILITIES=N90
```

Optional values:

```env
TAIS_PRINT_STATS=true
TAIS_STATS_INTERVAL_MS=30000
TAIS_HISTORY_RAW_CAPACITY=64
TAIS_MAX_BYTES=10485760
TAIS_RECONNECT_INITIAL_MS=5000
TAIS_RECONNECT_MAX_MS=60000
```

Build/test and run from the repository root:

```bash
mvn -f server/tais/pom.xml test
mvn -f server/tais/pom.xml package
java -jar server/tais/target/reds-tais-1.0-SNAPSHOT.jar
```
