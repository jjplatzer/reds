package net

import "encoding/json"

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
