package ingest;

import java.util.List;

/** All TAIS track/flight-plan records carried by one SWIM JMS message. */
public record TaisBatch(String facility, List<TaisObservation> observations) {

    public static final String ADDRESS = "faa.tais.observation";

    public TaisBatch {
        observations = List.copyOf(observations);
    }
}
