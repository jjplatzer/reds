package ingest;

import org.junit.jupiter.api.Test;

import javax.xml.parsers.DocumentBuilderFactory;
import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.time.Instant;

import static org.junit.jupiter.api.Assertions.*;

final class TaisParserTest {

    @Test
    void parsesLiveV4SimpleXmlShape() throws Exception {
        String xml = """
                <?xml version="1.0" encoding="UTF-8"?>
                <ns2:TATrackAndFlightPlan xmlns:ns2="urn:us:gov:dot:faa:atm:terminal:entities:v4-0:tais:terminalautomationinformation">
                  <src>N90</src>
                  <record>
                    <recSeqNum>5477</recSeqNum>
                    <recSrc>A</recSrc>
                    <recType>210</recType>
                    <recSTARSTimestamp>46947770</recSTARSTimestamp>
                    <recSTARSSrcID>0</recSTARSSrcID>
                    <recSTARSTimeSync>true</recSTARSTimeSync>
                    <recSAFAReceiptTime>2026-09-23T13:02:27.787Z</recSAFAReceiptTime>
                    <recSAFATimeSync>true</recSAFATimeSync>
                    <track>
                      <trackNum>3507</trackNum>
                      <mrtTime>2026-09-23T13:02:27.229Z</mrtTime>
                      <status>active</status>
                      <acAddress>AD51AC</acAddress>
                      <lat>42.68069</lat>
                      <lon>-71.16011</lon>
                      <vVert>37</vVert>
                      <vx>56</vx>
                      <vy>-114</vy>
                      <new>0</new>
                      <adsb>1</adsb>
                      <reportedBeaconCode>0317</reportedBeaconCode>
                      <reportedAltitude>5600</reportedAltitude>
                    </track>
                    <flightPlan>
                      <sfpn>208</sfpn>
                      <acid>N95747</acid>
                      <acType>C182</acType>
                      <scratchPad1>JFA</scratchPad1>
                      <assignedBeaconCode>0317</assignedBeaconCode>
                      <status>active</status>
                      <delete>0</delete>
                      <suspended>0</suspended>
                    </flightPlan>
                    <enhancedData>
                      <eramGufi>KB452274VB</eramGufi>
                      <sfdpsGufi>us.fdps.example</sfdpsGufi>
                      <departureAirport>KLWM</departureAirport>
                      <destinationAirport>KLWM</destinationAirport>
                    </enhancedData>
                  </record>
                </ns2:TATrackAndFlightPlan>
                """;

        DocumentBuilderFactory factory = DocumentBuilderFactory.newInstance();
        factory.setNamespaceAware(true);
        var doc = factory.newDocumentBuilder().parse(
                new ByteArrayInputStream(xml.getBytes(StandardCharsets.UTF_8))
        );

        Instant receivedAt = Instant.parse("2026-09-23T13:02:28Z");
        TaisBatch batch = TaisParser.parse(doc, receivedAt);

        assertEquals("N90", batch.facility());
        assertEquals(1, batch.observations().size());

        TaisObservation obs = batch.observations().getFirst();
        assertEquals("N90:T3507", obs.targetKey());
        assertEquals(receivedAt, obs.receivedAt());
        assertEquals(5477L, obs.recordMeta().sequenceNumber());
        assertEquals(Instant.parse("2026-09-23T13:02:27.787Z"), obs.recordMeta().safaReceiptTime());
        assertEquals(Instant.parse("2026-09-23T13:02:27.229Z"), obs.track().mrtTime());
        assertEquals("ad51ac", obs.track().acAddress());
        assertEquals("0317", obs.track().reportedBeaconCode());
        assertTrue(obs.track().adsb());
        assertEquals("N95747", obs.flightPlan().acid());
        assertEquals("0317", obs.flightPlan().assignedBeaconCode());
        assertEquals("KLWM", obs.enhancedData().destinationAirport());
    }

    @Test
    void acceptsTrackWithoutFlightPlan() throws Exception {
        String xml = """
                <TATrackAndFlightPlan>
                  <src>N90</src>
                  <record>
                    <track>
                      <trackNum>125</trackNum>
                      <mrtTime>2026-09-23T13:02:25.458Z</mrtTime>
                      <lat>45.61086</lat>
                      <lon>-73.27244</lon>
                    </track>
                  </record>
                </TATrackAndFlightPlan>
                """;

        DocumentBuilderFactory factory = DocumentBuilderFactory.newInstance();
        factory.setNamespaceAware(true);
        var doc = factory.newDocumentBuilder().parse(
                new ByteArrayInputStream(xml.getBytes(StandardCharsets.UTF_8))
        );

        TaisObservation obs = TaisParser.parse(doc, Instant.EPOCH).observations().getFirst();
        assertEquals("N90:T125", obs.targetKey());
        assertNull(obs.flightPlan());
        assertNull(obs.enhancedData());
    }
}
