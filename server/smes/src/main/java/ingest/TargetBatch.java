package ingest;

import java.util.List;

/**
 * Batch of surface targets parsed from a single JMS message.
 *
 * One dispatch per SMES message (which may carry 50–200 position reports in an
 * AT batch) instead of one dispatch per report. Travels via PassThroughCodec on
 * the local EventBus — no serialization overhead.
 */
public record TargetBatch(List<SurfaceTarget> targets) {}
