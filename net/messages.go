package net

import (
	"encoding/json"
	"time"
)

type SmesStatus int

const (
	SmesStatusDisconnected SmesStatus = iota
	SmesStatusConnected
)

type SmesStatusEvent struct {
	Status SmesStatus
	Err    error
}

type SmesFrame struct {
	Type       string                     `json:"type,omitempty"`
	Key        string                     `json:"key,omitempty"`
	Airport    string                     `json:"airport,omitempty"`
	UpdatedAt  string                     `json:"updatedAt,omitempty"`
	IsFull     bool                       `json:"isFull,omitempty"`
	IsSnapshot bool                       `json:"isSnapshot,omitempty"`
	Removed    bool                       `json:"removed,omitempty"`
	Reason     string                     `json:"reason,omitempty"`
	Changed    map[string]json.RawMessage `json:"changed,omitempty"`
}

type PlaybackBootstrapResponse struct {
	Airport        string                                `json:"airport"`
	At             string                                `json:"at"`
	BaselineTime   string                                `json:"baselineTime"`
	TargetCount    int                                   `json:"targetCount"`
	AppliedRecords int                                   `json:"appliedRecords"`
	Targets        map[string]map[string]json.RawMessage `json:"targets"`
}

type SetAirportsMessage struct {
	Type     string   `json:"type"`
	Airports []string `json:"airports"`
}

type ActivityMessage struct {
	Type string `json:"type"`
}

const (
	TaisProtocolVersion = 1
	TaisFeedName        = "tais"
)

const (
	TaisCoastPhaseNone = iota
	TaisCoastPhase1
	TaisCoastPhase2
)

// TaisFrame is the protocol-v1 envelope emitted by the REDS TAIS service.
// Snapshot frames carry Targets, update frames carry Target, and remove frames
// carry Key. The client validates protocol/feed/revision before mutating state.
type TaisFrame struct {
	Type        string       `json:"type"`
	Protocol    int          `json:"protocol"`
	Feed        string       `json:"feed"`
	Facility    string       `json:"facility,omitempty"`
	Revision    uint64       `json:"revision"`
	GeneratedAt time.Time    `json:"generatedAt,omitempty"`
	Targets     []TaisTarget `json:"targets,omitempty"`
	Target      *TaisTarget  `json:"target,omitempty"`
	Key         string       `json:"key,omitempty"`
}

type TaisTarget struct {
	Key           string            `json:"key"`
	Facility      string            `json:"facility"`
	ReceivedAt    time.Time         `json:"receivedAt,omitempty"`
	RecordMeta    *TaisRecordMeta   `json:"recordMeta,omitempty"`
	CoastPhase    int               `json:"coastPhase,omitempty"`
	CoastPhase2At time.Time         `json:"coastPhase2At,omitempty"`
	Track         TaisTrack         `json:"track"`
	FlightPlan    *TaisFlightPlan   `json:"flightPlan,omitempty"`
	EnhancedData  *TaisEnhancedData `json:"enhancedData,omitempty"`
	History       []TaisHistory     `json:"history,omitempty"`
}

type TaisRecordMeta struct {
	SequenceNumber  int64     `json:"sequenceNumber,omitempty"`
	Source          string    `json:"source,omitempty"`
	Type            int       `json:"type,omitempty"`
	StarsTimestamp  int64     `json:"starsTimestamp,omitempty"`
	StarsSourceID   int       `json:"starsSourceId,omitempty"`
	StarsTimeSync   bool      `json:"starsTimeSync,omitempty"`
	SafaReceiptTime time.Time `json:"safaReceiptTime,omitempty"`
	SafaTimeSync    bool      `json:"safaTimeSync,omitempty"`
}

type TaisTrack struct {
	TrackNum           string    `json:"trackNum"`
	MRTTime            time.Time `json:"mrtTime,omitempty"`
	Status             string    `json:"status,omitempty"`
	ACAddress          string    `json:"acAddress,omitempty"`
	XPos               int       `json:"xPos,omitempty"`
	YPos               int       `json:"yPos,omitempty"`
	Lat                float64   `json:"lat,omitempty"`
	Lon                float64   `json:"lon,omitempty"`
	VerticalRate       int       `json:"verticalRate,omitempty"`
	VX                 int       `json:"vx,omitempty"`
	VY                 int       `json:"vy,omitempty"`
	VerticalRateRaw    int       `json:"verticalRateRaw,omitempty"`
	VXRaw              int       `json:"vxRaw,omitempty"`
	VYRaw              int       `json:"vyRaw,omitempty"`
	Frozen             bool      `json:"frozen,omitempty"`
	NewTrack           bool      `json:"newTrack,omitempty"`
	Pseudo             bool      `json:"pseudo,omitempty"`
	ADSB               bool      `json:"adsb,omitempty"`
	ReportedBeaconCode string    `json:"reportedBeaconCode,omitempty"`
	ReportedAltitude   int       `json:"reportedAltitude,omitempty"`

	// Aircraft-transmitted ADS-B Flight ID from a verified source, when available.
	// Not populated by the current TAIS-only upstream.
	ADSBFlightID string `json:"adsbFlightId,omitempty"`

	// FAA ICAO-to-N-number association (not an ADS-B-transmitted Flight ID).
	Registration string `json:"registration,omitempty"`
}

type TaisFlightPlan struct {
	SFPN               int    `json:"sfpn,omitempty"`
	OCR                string `json:"ocr,omitempty"`
	RNAV               int    `json:"rnav,omitempty"`
	ScratchPad1        string `json:"scratchPad1,omitempty"`
	ScratchPad2        string `json:"scratchPad2,omitempty"`
	CPS                string `json:"cps,omitempty"`
	Runway             string `json:"runway,omitempty"`
	AssignedBeaconCode string `json:"assignedBeaconCode,omitempty"`
	RequestedAltitude  int    `json:"requestedAltitude,omitempty"`
	AssignedAltitude   int    `json:"assignedAltitude,omitempty"`
	Category           string `json:"category,omitempty"`
	DBI                string `json:"dbi,omitempty"`
	ACID               string `json:"acid,omitempty"`
	ACType             string `json:"acType,omitempty"`
	EntryFix           string `json:"entryFix,omitempty"`
	ExitFix            string `json:"exitFix,omitempty"`
	Airport            string `json:"airport,omitempty"`
	FlightRules        string `json:"flightRules,omitempty"`
	RawFlightRules     string `json:"rawFlightRules,omitempty"`
	Type               string `json:"type,omitempty"`
	PTDTime            string `json:"ptdTime,omitempty"`
	Status             string `json:"status,omitempty"`
	Deleted            bool   `json:"deleted,omitempty"`
	Suspended          bool   `json:"suspended,omitempty"`
	LLD                string `json:"lld,omitempty"`
	ECID               string `json:"ecid,omitempty"`
	EquipmentSuffix    string `json:"eqptSuffix,omitempty"`
}

type TaisEnhancedData struct {
	ERAMGUFI           string `json:"eramGufi,omitempty"`
	SFDPSGUFI          string `json:"sfdpsGufi,omitempty"`
	DepartureAirport   string `json:"departureAirport,omitempty"`
	DestinationAirport string `json:"destinationAirport,omitempty"`
}

type TaisHistory struct {
	Time             time.Time `json:"time"`
	Lat              float64   `json:"lat"`
	Lon              float64   `json:"lon"`
	ReportedAltitude int       `json:"reportedAltitude,omitempty"`
	VX               int       `json:"vx,omitempty"`
	VY               int       `json:"vy,omitempty"`
	VerticalRate     int       `json:"verticalRate,omitempty"`
	ADSB             bool      `json:"adsb,omitempty"`
}

// TaisSnapshot is a detached, point-in-time client view. Targets and their
// history slices are copied before return, so callers cannot mutate client
// state while the websocket goroutine applies live updates.
type TaisSnapshot struct {
	Ready       bool
	Revision    uint64
	GeneratedAt time.Time
	Targets     []TaisTarget
}

type TaisClientStatus struct {
	Connected   bool
	Ready       bool
	Revision    uint64
	TargetCount int
	LastError   error
}

const (
	SfdpsProtocolVersion = 1
	SfdpsFeedName        = "sfdps"
)

// SfdpsFrame mirrors server/sfdps protocol-v1. ERAM does not consume it yet;
// defining the wire model here keeps the future client/fusion work out of the
// XML/JMS server implementation.
type SfdpsFrame struct {
	Type           string          `json:"type"`
	Protocol       int             `json:"protocol"`
	Feed           string          `json:"feed"`
	Facility       string          `json:"facility,omitempty"`
	Revision       uint64          `json:"revision"`
	GeneratedAt    time.Time       `json:"generatedAt,omitempty"`
	Channel        string          `json:"channel,omitempty"`
	MessageType    string          `json:"messageType,omitempty"`
	Flights        []SfdpsFlight   `json:"flights,omitempty"`
	Airspace       []SfdpsAirspace `json:"airspace,omitempty"`
	General        []SfdpsGeneral  `json:"general,omitempty"`
	Status         []SfdpsStatus   `json:"status,omitempty"`
	Flight         *SfdpsFlight    `json:"flight,omitempty"`
	AirspaceEntity *SfdpsAirspace  `json:"airspaceEntity,omitempty"`
	GeneralMessage *SfdpsGeneral   `json:"generalMessage,omitempty"`
	StatusMessage  *SfdpsStatus    `json:"statusMessage,omitempty"`
	Key            string          `json:"key,omitempty"`
}

type SfdpsFlight struct {
	Key                            string            `json:"key"`
	Facility                       string            `json:"facility"`
	Identity                       string            `json:"identity"`
	Tracked                        bool              `json:"tracked"`
	LastMessageType                string            `json:"lastMessageType,omitempty"`
	ReceivedAt                     time.Time         `json:"receivedAt,omitempty"`
	MessageTime                    time.Time         `json:"messageTime,omitempty"`
	JMS                            *SfdpsJMSMetadata `json:"jms,omitempty"`
	SFDPSGUFI                      string            `json:"sfdpsGufi,omitempty"`
	UUIDGUFI                       string            `json:"uuidGufi,omitempty"`
	FlightPlanID                   string            `json:"flightPlanId,omitempty"`
	FlightState                    string            `json:"flightState,omitempty"`
	AircraftIdentification         string            `json:"aircraftIdentification,omitempty"`
	PreviousAircraftIdentification string            `json:"previousAircraftIdentification,omitempty"`
	ComputerID                     string            `json:"computerId,omitempty"`
	SiteSpecificPlanID             string            `json:"siteSpecificPlanId,omitempty"`
	AircraftType                   string            `json:"aircraftType,omitempty"`
	DeparturePoint                 string            `json:"departurePoint,omitempty"`
	ArrivalPoint                   string            `json:"arrivalPoint,omitempty"`
	BeaconCode                     string            `json:"beaconCode,omitempty"`
	AssignedAltitude               *SfdpsAltitude    `json:"assignedAltitude,omitempty"`
	InterimAltitude                *SfdpsAltitude    `json:"interimAltitude,omitempty"`
	Track                          *SfdpsTrack       `json:"track,omitempty"`
	Control                        *SfdpsControl     `json:"control,omitempty"`
	FourthLine                     *SfdpsFourthLine  `json:"fourthLine,omitempty"`
	PointOut                       *SfdpsPointOut    `json:"pointOut,omitempty"`
	Handoff                        *SfdpsHandoff     `json:"handoff,omitempty"`
	Hold                           *SfdpsHold        `json:"hold,omitempty"`
	Route                          []SfdpsRoutePoint `json:"route,omitempty"`
	NameValues                     map[string]string `json:"nameValues,omitempty"`
}

type SfdpsJMSMetadata struct {
	SourceSystem       string    `json:"sourceSystem,omitempty"`
	SourceFacility     string    `json:"sourceFacility,omitempty"`
	MessageType        string    `json:"messageType,omitempty"`
	FlightOperator     string    `json:"flightOperator,omitempty"`
	FlightID           string    `json:"flightId,omitempty"`
	Origin             string    `json:"origin,omitempty"`
	Destination        string    `json:"destination,omitempty"`
	OneMinuteFrequency *bool     `json:"oneMinuteFrequency,omitempty"`
	Sensitive          string    `json:"sensitive,omitempty"`
	Restricted         string    `json:"restricted,omitempty"`
	Authoritative      *bool     `json:"authoritative,omitempty"`
	FDPSReceivedAt     time.Time `json:"fdpsReceivedAt,omitempty"`
	FDPSSentAt         time.Time `json:"fdpsSentAt,omitempty"`
	SequenceNumber     *int64    `json:"sequenceNumber,omitempty"`
	DataType           string    `json:"dataType,omitempty"`
}

type SfdpsAltitude struct {
	Kind  string   `json:"kind,omitempty"`
	Value *float64 `json:"value,omitempty"`
	Lower *float64 `json:"lower,omitempty"`
	Upper *float64 `json:"upper,omitempty"`
	Raw   string   `json:"raw,omitempty"`
	UOM   string   `json:"uom,omitempty"`
}

type SfdpsTrack struct {
	PositionTime          time.Time `json:"positionTime,omitempty"`
	Lat                   *float64  `json:"lat,omitempty"`
	Lon                   *float64  `json:"lon,omitempty"`
	ReportedAltitude      *float64  `json:"reportedAltitude,omitempty"`
	ReportedAltitudeUOM   string    `json:"reportedAltitudeUom,omitempty"`
	TargetLat             *float64  `json:"targetLat,omitempty"`
	TargetLon             *float64  `json:"targetLon,omitempty"`
	TargetAltitude        *float64  `json:"targetAltitude,omitempty"`
	TargetAltitudeUOM     string    `json:"targetAltitudeUom,omitempty"`
	TargetAltitudeInvalid *bool     `json:"targetAltitudeInvalid,omitempty"`
	TargetPositionTime    time.Time `json:"targetPositionTime,omitempty"`
	VelocityX             *float64  `json:"velocityX,omitempty"`
	VelocityY             *float64  `json:"velocityY,omitempty"`
	VelocityUOM           string    `json:"velocityUom,omitempty"`
	GroundSpeed           *float64  `json:"groundSpeed,omitempty"`
	GroundSpeedUOM        string    `json:"groundSpeedUom,omitempty"`
	Heading               *float64  `json:"heading,omitempty"`
	HeadingUOM            string    `json:"headingUom,omitempty"`
	CoastIndicator        string    `json:"coastIndicator,omitempty"`
	ReportSource          string    `json:"reportSource,omitempty"`
}

type SfdpsControl struct {
	ControllingFacility string `json:"controllingFacility,omitempty"`
	ControllingSector   string `json:"controllingSector,omitempty"`
	ReceivingFacility   string `json:"receivingFacility,omitempty"`
	ReceivingSector     string `json:"receivingSector,omitempty"`
	AcceptingFacility   string `json:"acceptingFacility,omitempty"`
	AcceptingSector     string `json:"acceptingSector,omitempty"`
}

type SfdpsFourthLine struct {
	Heading string `json:"heading,omitempty"`
	Speed   string `json:"speed,omitempty"`
	Text    string `json:"text,omitempty"`
}

type SfdpsPointOut struct {
	OriginatingFacility string `json:"originatingFacility,omitempty"`
	OriginatingSector   string `json:"originatingSector,omitempty"`
	ReceivingFacility   string `json:"receivingFacility,omitempty"`
	ReceivingSector     string `json:"receivingSector,omitempty"`
}

type SfdpsHandoff struct {
	Event                string `json:"event,omitempty"`
	TransferringFacility string `json:"transferringFacility,omitempty"`
	TransferringSector   string `json:"transferringSector,omitempty"`
	ReceivingFacility    string `json:"receivingFacility,omitempty"`
	ReceivingSector      string `json:"receivingSector,omitempty"`
	AcceptingFacility    string `json:"acceptingFacility,omitempty"`
	AcceptingSector      string `json:"acceptingSector,omitempty"`
}

type SfdpsHold struct {
	Fix                        string `json:"fix,omitempty"`
	ExpectFurtherClearanceTime string `json:"expectFurtherClearanceTime,omitempty"`
	Airborne                   *bool  `json:"airborne,omitempty"`
}

type SfdpsRoutePoint struct {
	Designator    string    `json:"designator,omitempty"`
	EstimatedTime time.Time `json:"estimatedTime,omitempty"`
}

type SfdpsAirspace struct {
	MessageType         string            `json:"messageType,omitempty"`
	Facility            string            `json:"facility,omitempty"`
	ReceivedAt          time.Time         `json:"receivedAt,omitempty"`
	JMS                 *SfdpsJMSMetadata `json:"jms,omitempty"`
	Kind                string            `json:"kind,omitempty"`
	Identifier          string            `json:"identifier,omitempty"`
	Status              string            `json:"status,omitempty"`
	FAVNumbers          []string          `json:"favNumbers,omitempty"`
	ScheduleID          string            `json:"scheduleId,omitempty"`
	ScheduleType        string            `json:"scheduleType,omitempty"`
	ActivationTimeRaw   string            `json:"activationTimeRaw,omitempty"`
	DeactivationTimeRaw string            `json:"deactivationTimeRaw,omitempty"`
	ActivationTime      time.Time         `json:"activationTime,omitempty"`
	DeactivationTime    time.Time         `json:"deactivationTime,omitempty"`
	LowerAltitude       *float64          `json:"lowerAltitude,omitempty"`
	LowerAltitudeUOM    string            `json:"lowerAltitudeUom,omitempty"`
	UpperAltitude       *float64          `json:"upperAltitude,omitempty"`
	UpperAltitudeUOM    string            `json:"upperAltitudeUom,omitempty"`
	Fields              map[string]string `json:"fields,omitempty"`
}

type SfdpsGeneral struct {
	MessageType        string            `json:"messageType,omitempty"`
	Facility           string            `json:"facility,omitempty"`
	ReceivedAt         time.Time         `json:"receivedAt,omitempty"`
	MessageTime        time.Time         `json:"messageTime,omitempty"`
	JMS                *SfdpsJMSMetadata `json:"jms,omitempty"`
	FlightID           string            `json:"flightId,omitempty"`
	ComputerID         string            `json:"computerId,omitempty"`
	SiteSpecificPlanID string            `json:"siteSpecificPlanId,omitempty"`
	Remarks            string            `json:"remarks,omitempty"`
	RemarkType         string            `json:"remarkType,omitempty"`
	InterimAltitude    *float64          `json:"interimAltitude,omitempty"`
	HoldFix            string            `json:"holdFix,omitempty"`
	HoldTime           string            `json:"holdTime,omitempty"`
	FAV                string            `json:"fav,omitempty"`
	Fields             map[string]string `json:"fields,omitempty"`
}

type SfdpsStatus struct {
	Center         string            `json:"center,omitempty"`
	Classification string            `json:"classification,omitempty"`
	ReceivedAt     time.Time         `json:"receivedAt,omitempty"`
	Time           time.Time         `json:"time,omitempty"`
	JMS            *SfdpsJMSMetadata `json:"jms,omitempty"`
	StatusType     string            `json:"statusType,omitempty"`
	Source         string            `json:"source,omitempty"`
	ARTCCStates    map[string]string `json:"artccStates,omitempty"`
	SoftwareStates map[string]string `json:"softwareStates,omitempty"`
	Process        string            `json:"process,omitempty"`
	Details        string            `json:"details,omitempty"`
	Fields         map[string]string `json:"fields,omitempty"`
}
