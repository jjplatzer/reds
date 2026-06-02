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
	Changed   map[string]json.RawMessage `json:"changed,omitempty"`
}

type SetAirportsMessage struct {
	Type     string   `json:"type"`
	Airports []string `json:"airports"`
}
