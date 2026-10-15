package ingest;

import messages.JmsMetadata;
import messages.SfdpsChannel;
import messages.SfdpsEvent;
import org.junit.jupiter.api.Test;
import org.w3c.dom.Document;

import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.List;

import static org.junit.jupiter.api.Assertions.*;

class SfdpsParserTest {

    @Test
    void parsesFlightTrackAndEramspecificState() throws Exception {
        String xml = """
                <mesg:MessageCollection xmlns:mesg="urn:test:msg" xmlns:nas="urn:test:nas" xmlns:fx="urn:test:flight" xmlns:fb="urn:test:base">
                  <mesg:message>
                    <mesg:messageDateTime>2026-10-05T16:00:01Z</mesg:messageDateTime>
                    <nas:flight centre="ZNY">
                      <fx:gufi>11111111-2222-3333-4444-555555555555</fx:gufi>
                      <fx:flightIdentification aircraftIdentification="AAL123" computerId="3AB" siteSpecificPlanId="77"/>
                      <nas:flightPlan identifier="KN12345678"/>
                      <nas:supplementalData><nas:additionalFlightInformation>
                        <nas:nameValue name="FDPS_GUFI" value="us.fdps.example.1"/>
                      </nas:additionalFlightInformation></nas:supplementalData>
                      <nas:assignedAltitude><nas:simple uom="FT">35000</nas:simple></nas:assignedAltitude>
                      <nas:controllingUnit unitIdentifier="ZNY" sectorIdentifier="42"/>
                      <fx:enRoute>
                        <nas:beaconCodeAssignment><nas:currentBeaconCode>1234</nas:currentBeaconCode></nas:beaconCodeAssignment>
                        <nas:position positionTime="2026-10-05T16:00:00Z" targetPositionTime="2026-10-05T15:59:59Z" coastIndicator="C" reportSource="RADAR">
                          <nas:position><fb:location><fb:pos>40.1000 -73.2000</fb:pos></fb:location></nas:position>
                          <fb:altitude uom="FT">34700</fb:altitude>
                          <nas:trackVelocity><nas:x uom="KT">420</nas:x><nas:y uom="KT">20</nas:y></nas:trackVelocity>
                          <nas:actualSpeed><nas:surveillance uom="KT">431</nas:surveillance></nas:actualSpeed>
                          <nas:heading uom="DEG">91</nas:heading>
                          <nas:targetPosition><fb:pos>40.0990 -73.2010</fb:pos></nas:targetPosition>
                          <nas:targetAltitude uom="FT">34600</nas:targetAltitude>
                        </nas:position>
                        <nas:boundaryCrossings><nas:handoff event="INITIATED">
                          <nas:transferringUnit unitIdentifier="ZNY" sectorIdentifier="42"/>
                          <nas:receivingUnit unitIdentifier="ZBW" sectorIdentifier="18"/>
                        </nas:handoff></nas:boundaryCrossings>
                      </fx:enRoute>
                    </nas:flight>
                    <nas:metadata messageType="TH_FIXM"/>
                  </mesg:message>
                </mesg:MessageCollection>
                """;

        Document doc = parse(xml);
        JmsMetadata jms = new JmsMetadata("SFDPS", "ZNY", "BATCH_TH_FIXM", null, "AAL123", null, null,
                false, null, null, true, null, null, 10L, "FlightFIXM");
        List<SfdpsEvent> events = SfdpsParser.parse(SfdpsChannel.FLIGHT_FIXM, doc, jms, Instant.now());

        assertEquals(1, events.size());
        var flight = events.getFirst().flight();
        assertEquals("TH", flight.messageType());
        assertEquals("ZNY", flight.facility());
        assertEquals("us.fdps.example.1", flight.sfdpsGufi());
        assertEquals("AAL123", flight.aircraftIdentification());
        assertEquals("3AB", flight.computerId());
        assertEquals("1234", flight.beaconCode());
        assertEquals(35000.0, flight.assignedAltitude().value());
        assertEquals(40.1, flight.track().lat());
        assertEquals(-73.2, flight.track().lon());
        assertEquals(34700.0, flight.track().reportedAltitude());
        assertEquals(431.0, flight.track().groundSpeed());
        assertEquals("42", flight.control().controllingSector());
        assertEquals("18", flight.handoff().receivingSector());
    }


    @Test
    void toleratesNewerAircraftPositionShape() throws Exception {
        String xml = """
                <MessageCollection><message><flight centre="ZNY">
                  <flightIdentification aircraftIdentification="JBU1" computerId="501"/>
                  <supplementalData><additionalFlightInformation><nameValue name="fdpsGufi" value="us.fdps.jbu1"/></additionalFlightInformation></supplementalData>
                  <arrival><aerodrome locationIndicator="KJFK"/></arrival>
                  <enRoute><aircraftPosition>
                    <position><position><pos>40.5000 -73.6000</pos></position></position>
                    <positionTime>2026-10-05T16:00:00Z</positionTime>
                    <reportedLevel><altitude uom="FT">12000</altitude></reportedLevel>
                    <trackVelocity><x uom="KM_H">500</x><y uom="KM_H">20</y></trackVelocity>
                  </aircraftPosition></enRoute>
                </flight><metadata messageType="TH_FIXM"/></message></MessageCollection>
                """;
        var event = SfdpsParser.parse(SfdpsChannel.FLIGHT_FIXM, parse(xml),
                new JmsMetadata(null, "ZNY", "TH_FIXM", null, "JBU1", null, "KJFK", null, null, null, true, null, null, 8L, "FlightFIXM"),
                Instant.now()).getFirst();
        assertEquals("us.fdps.jbu1", event.flight().sfdpsGufi());
        assertEquals("KJFK", event.flight().arrivalPoint());
        assertEquals(40.5, event.flight().track().lat());
        assertEquals(-73.6, event.flight().track().lon());
        assertEquals(12000.0, event.flight().track().reportedAltitude());
    }

    @Test
    void parsesFourthLineFixm() throws Exception {
        String xml = """
                <MessageCollection><message><flight centre="ZDC">
                  <flightIdentification aircraftIdentification="UAL9" computerId="441"/>
                  <supplementalData><additionalFlightInformation><nameValue name="FDPS_GUFI" value="us.fdps.9"/></additionalFlightInformation></supplementalData>
                  <enRoute><cleared clearanceHeading="H270" clearanceSpeed="M82" clearanceText="DCTJ42"/></enRoute>
                </flight><metadata messageType="HF_FIXM"/></message></MessageCollection>
                """;
        var events = SfdpsParser.parse(SfdpsChannel.FLIGHT_FIXM, parse(xml),
                new JmsMetadata(null, "ZDC", "HF_FIXM", null, null, null, null, null, null, null, true, null, null, 1L, "FlightFIXM"),
                Instant.now());
        assertEquals("H270", events.getFirst().flight().fourthLine().heading());
        assertEquals("M82", events.getFirst().flight().fourthLine().speed());
        assertEquals("DCTJ42", events.getFirst().flight().fourthLine().text());
    }

    @Test
    void parsesConvertedRouteAndHoldUsingManualFixmPaths() throws Exception {
        String xml = """
                <MessageCollection><message><flight centre="ZNY">
                  <flightIdentification aircraftIdentification="DAL10" computerId="102" siteSpecificPlanId="55"/>
                  <supplementalData><additionalFlightInformation><nameValue name="FDPS_GUFI" value="us.fdps.dal10"/></additionalFlightInformation></supplementalData>
                  <agreed><route>
                    <holdFix><designator>CAMRN</designator></holdFix>
                    <expandedRoute>
                      <routePoint estimatedTime="2026-10-05T16:12:00Z"><point><designator>CAMRN</designator></point></routePoint>
                      <routePoint estimatedTime="2026-10-05T16:20:00Z"><point><designatedPoint designator="ROBER"/></point></routePoint>
                    </expandedRoute>
                  </route></agreed>
                  <enRoute><expectedFurtherClearanceTime time="2026-10-05T16:30:00Z"/></enRoute>
                </flight><metadata messageType="HX_FIXM"/></message></MessageCollection>
                """;
        var events = SfdpsParser.parse(SfdpsChannel.FLIGHT_FIXM, parse(xml),
                new JmsMetadata(null, "ZNY", "HX_FIXM", null, null, null, null, null, null, null, true, null, null, 5L, "FlightFIXM"),
                Instant.now());
        var flight = events.getFirst().flight();
        assertEquals(2, flight.route().size());
        assertEquals("CAMRN", flight.route().getFirst().designator());
        assertEquals("ROBER", flight.route().get(1).designator());
        assertEquals("CAMRN", flight.hold().fix());
        assertEquals("2026-10-05T16:30:00Z", flight.hold().expectFurtherClearanceTime());
    }

    @Test
    void parsesAirspaceRouteAndSector() throws Exception {
        String routeXml = """
                <BasicMessage><RouteSegment><timeSlice><RouteSegmentTimeSlice>
                  <name>SD001</name><availability><RouteAvailability><status>OPEN</status></RouteAvailability></availability>
                </RouteSegmentTimeSlice></timeSlice></RouteSegment></BasicMessage>
                """;
        var route = SfdpsParser.parse(SfdpsChannel.AIRSPACE_AIXM, parse(routeXml),
                new JmsMetadata(null, "ZNY", "HR_AIXM", null, null, null, null, null, null, null, null, null, null, 1L, "AirspaceAIXM"), Instant.now());
        assertEquals("SD001", route.getFirst().airspace().identifier());
        assertEquals("ON", route.getFirst().airspace().status());

        String sectorXml = """
                <BasicMessage><Airspace><timeSlice><AirspaceTimeSlice><type>SECTOR</type><designator>42</designator>
                  <extension><SectorAssignmentStatusExtension><FAVNumber>2405</FAVNumber><FAVNumber>2406</FAVNumber></SectorAssignmentStatusExtension></extension>
                </AirspaceTimeSlice></timeSlice></Airspace></BasicMessage>
                """;
        var sector = SfdpsParser.parse(SfdpsChannel.AIRSPACE_AIXM, parse(sectorXml),
                new JmsMetadata(null, "ZNY", "SH_AIXM", null, null, null, null, null, null, null, null, null, null, 2L, "AirspaceAIXM"), Instant.now());
        assertEquals("42", sector.getFirst().airspace().identifier());
        assertEquals(List.of("2405", "2406"), sector.getFirst().airspace().favNumbers());

        String saaXml = """
                <BasicMessage><Airspace><timeSlice><AirspaceTimeSlice><type>R</type><designator>SHPT38AL</designator>
                  <activation><AirspaceActivation><status>AVBL_FOR_ACTIVATION</status>
                    <levels><AirspaceLayer><lowerLimit uom="FT">5000</lowerLimit><upperLimit uom="FT">30000</upperLimit></AirspaceLayer></levels>
                  </AirspaceActivation></activation>
                  <extension><SAAScheduleExtension><ID>0016</ID><type>S</type><activationDateTime>302355</activationDateTime><deactivationDateTime>310355</deactivationDateTime></SAAScheduleExtension></extension>
                </AirspaceTimeSlice></timeSlice></Airspace></BasicMessage>
                """;
        var saa = SfdpsParser.parse(SfdpsChannel.AIRSPACE_AIXM, parse(saaXml),
                new JmsMetadata(null, "ZNY", "SU_AIXM", null, null, null, null, null, null, null, null, null, null, 3L, "AirspaceAIXM"), Instant.now());
        var entity = saa.getFirst().airspace();
        assertEquals("SCHED", entity.status());
        assertEquals("302355", entity.activationTimeRaw());
        assertNull(entity.activationTime());
        assertEquals(5000.0, entity.lowerAltitude());
        assertEquals("FT", entity.lowerAltitudeUom());
    }

    @Test
    void preservesGeneralAndStatusSimpleXml() throws Exception {
        String generalXml = """
                <FDPSMsg><header><center>ZNY</center></header><GH>
                  <sourceId_00e>1600010001</sourceId_00e>
                  <remarks_11c remarktype="intrafacility">TCAS UNITED LIVERY</remarks_11c>
                </GH></FDPSMsg>
                """;
        var general = SfdpsParser.parse(SfdpsChannel.GENERAL, parse(generalXml),
                new JmsMetadata(null, "ZNY", "GH", null, null, null, null, null, null, null, null, null, null, 3L, "ERGMP"), Instant.now());
        assertEquals("TCAS UNITED LIVERY", general.getFirst().general().remarks());
        assertEquals("intrafacility", general.getFirst().general().remarkType());

        String statusXml = """
                <Status><Center>ALL</Center><Classification>Public</Classification><Time>2026-10-05T16:00:00Z</Time>
                  <StatusType>ARTCC Status</StatusType><Source>SFDPS-Monitor</Source>
                  <ARTCC center="ZNY" state="up"/><ARTCC center="ZDC" state="down"/>
                  <Details>heartbeat</Details>
                </Status>
                """;
        var status = SfdpsParser.parse(SfdpsChannel.STATUS, parse(statusXml),
                new JmsMetadata(null, null, "STATUS", null, null, null, null, null, null, null, null, null, null, 4L, "STATUS"), Instant.now());
        assertEquals("ARTCC Status", status.getFirst().status().statusType());
        assertEquals("up", status.getFirst().status().artccStates().get("ZNY"));
    }

    private static Document parse(String xml) throws Exception {
        return XmlSupport.newDocumentBuilder().parse(new ByteArrayInputStream(xml.getBytes(StandardCharsets.UTF_8)));
    }
}
