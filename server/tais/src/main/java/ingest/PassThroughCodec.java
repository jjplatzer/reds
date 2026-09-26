package ingest;

import io.vertx.core.buffer.Buffer;
import io.vertx.core.eventbus.MessageCodec;

/** Local-only EventBus codec: no serialization, no clustered Vert.x use. */
public final class PassThroughCodec<T> implements MessageCodec<T, T> {

    private final String name;

    public PassThroughCodec(String name) {
        this.name = name;
    }

    @Override public void encodeToWire(Buffer buffer, T value) { throw new UnsupportedOperationException("local only"); }
    @Override public T decodeFromWire(int pos, Buffer buffer)   { throw new UnsupportedOperationException("local only"); }
    @Override public T transform(T value)                       { return value; }
    @Override public String name()                              { return name; }
    @Override public byte systemCodecID()                       { return -1; }
}
