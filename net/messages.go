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
	Type      string                     `json:"type,omitempty"`
	Key       string                     `json:"key,omitempty"`
	Airport   string                     `json:"airport,omitempty"`
	UpdatedAt string                     `json:"updatedAt,omitempty"`
	IsFull    bool                       `json:"isFull,omitempty"`
	Removed   bool                       `json:"removed,omitempty"`
	Reason    string                     `json:"reason,omitempty"`
	Changed   map[string]json.RawMessage `json:"changed,omitempty"`
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

// TaisFrame is the protocol-v1 envelope emitted by the REDS TAIS service.
// Snapshot frames carry Targets, update frames carry Target, and remove frames
// carry Key. The client validates protocol/feed/revision before mutating state.
type TaisFrame struct {
	Type        string       `json:"type"`
	Protocol    int          `json:"protocol"`
	Feed        string       `json:"feed"`
	Revision    uint64       `json:"revision"`
	GeneratedAt time.Time    `json:"generatedAt,omitempty"`
	Targets     []TaisTarget `json:"targets,omitempty"`
	Target      *TaisTarget  `json:"target,omitempty"`
	Key         string       `json:"key,omitempty"`
}

type TaisTarget struct {
	Key          string            `json:"key"`
	Facility     string            `json:"facility"`
	ReceivedAt   time.Time         `json:"receivedAt,omitempty"`
	RecordMeta   *TaisRecordMeta   `json:"recordMeta,omitempty"`
	Track        TaisTrack         `json:"track"`
	FlightPlan   *TaisFlightPlan   `json:"flightPlan,omitempty"`
	EnhancedData *TaisEnhancedData `json:"enhancedData,omitempty"`
	History      []TaisHistory     `json:"history,omitempty"`
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
