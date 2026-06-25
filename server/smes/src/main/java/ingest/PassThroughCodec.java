package ingest;

import io.vertx.core.buffer.Buffer;
import io.vertx.core.eventbus.MessageCodec;

/**
 * Local-only EventBus codec that passes objects by reference.
 *
 * No serialization — only safe for single-JVM (non-clustered) deployment.
 * Throws if Vert.x ever attempts wire serialization (e.g. in a cluster).
 */
public final class PassThroughCodec<T> implements MessageCodec<T, T> {

    private final String name;

    public PassThroughCodec(String name) { this.name = name; }

    @Override public void   encodeToWire(Buffer buffer, T t)       { throw new UnsupportedOperationException("local only"); }
    @Override public T      decodeFromWire(int pos, Buffer buffer)  { throw new UnsupportedOperationException("local only"); }
    @Override public T      transform(T t)                          { return t; }
    @Override public String name()                                  { return name; }
    @Override public byte   systemCodecID()                         { return -1; }
}
