package asdex

import (
	"encoding/json"
	"fmt"
	stdmath "math"
	"strconv"
	"strings"
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	redsnet "github.com/juliusplatzer/reds/net"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

type SensorSource string

const (
	SourceUnknown SensorSource = ""
	SourceFUS     SensorSource = "FUS"
	SourceADS     SensorSource = "ADS"
	SourceMLT     SensorSource = "MLT"
)

type Target struct {
	ID string

	Callsign string
	Beacon   string
	Fix      string

	// TargetType is nil when unknown. Aircraft use ICAO type codes such as
	// "A320"; vehicles use "VEH".
	TargetType *string

	// CWT is the wake turbulence / CWT category.
	CWT string

	Lat float64
	Lon float64

	PosFeet redsmath.Vec2

	HeadingDeg    float32
	GroundSpeedKt float32

	AltitudeFt  int
	HasAltitude bool

	Source SensorSource

	Scratchpad1 string
	Scratchpad2 string

	ShowDB         bool
	ForceDataBlock bool

	Live bool

	PositionReportTime time.Time
	LastSmesFrameTime  time.Time

	CoastListID  string
	CoastUntil   time.Time
	SuspendUntil time.Time

	Suspended bool
	Coasting  bool
	Dropped   bool

	AlertsInhibited bool
}

func (t *Target) EffectiveShowDB() bool {
	if t == nil || !t.ShowDB || t.Suspended || t.Dropped {
		return false
	}
	return targetCanHaveDataBlock(t)
}

type TargetHistoryPoint struct {
	PosFeet redsmath.Vec2
}

type TargetStore struct {
	targets map[string]*Target
	order   []string

	history            map[string][]TargetHistoryPoint
	overrides          map[string]DatablockFieldOverride
	manualAssociations map[string]ManualAssociationOverride
	manualTags         map[string]ManualTagOverride
	terminatedTracks   map[string]bool
	hoverRevision      uint64
}

func NewTargetStore() TargetStore {
	return TargetStore{
		targets:            make(map[string]*Target),
		history:            make(map[string][]TargetHistoryPoint),
		overrides:          make(map[string]DatablockFieldOverride),
		manualAssociations: make(map[string]ManualAssociationOverride),
		manualTags:         make(map[string]ManualTagOverride),
		terminatedTracks:   make(map[string]bool),
	}
}

type DatablockFieldOverride struct {
	Callsign    string
	Beacon      string
	CWT         string
	TargetType  string
	Fix         string
	Scratchpad1 string
	Scratchpad2 string

	// Active means the override applies even when a value is empty.
	Active bool
}

type ManualAssociationOverride struct {
	Callsign    string
	Beacon      string
	CWT         string
	TargetType  string
	Fix         string
	Scratchpad1 string
	Scratchpad2 string

	Active bool
}

type ManualTagOverride struct {
	AircraftID string
	Active     bool
}

func (s *TargetStore) SetDatablockOverride(targetID string, override DatablockFieldOverride) {
	if s == nil || targetID == "" {
		return
	}
	if s.overrides == nil {
		s.overrides = make(map[string]DatablockFieldOverride)
	}

	override.Active = true
	s.overrides[targetID] = override

	if target := s.TargetByID(targetID); target != nil {
		applyDatablockOverride(target, override)
		s.applyManualAssociations(target)
		s.applyManualTags(target)
		s.applyTerminationOverride(target)
	}
}

func (s *TargetStore) applyDatablockOverrides(target *Target) {
	if s == nil || target == nil || s.overrides == nil {
		return
	}

	override, ok := s.overrides[target.ID]
	if !ok {
		return
	}
	applyDatablockOverride(target, override)
}

func applyDatablockOverride(target *Target, override DatablockFieldOverride) {
	if target == nil || !override.Active {
		return
	}

	target.Callsign = override.Callsign
	target.Beacon = override.Beacon
	target.CWT = override.CWT
	target.Fix = override.Fix
	target.Scratchpad1 = override.Scratchpad1
	target.Scratchpad2 = override.Scratchpad2

	targetType := strings.TrimSpace(override.TargetType)
	if targetType == "" {
		target.TargetType = nil
	} else {
		target.TargetType = stringPointer(targetType)
	}
}

func (s *TargetStore) applyManualAssociations(target *Target) {
	if s == nil || target == nil || s.manualAssociations == nil {
		return
	}

	override, ok := s.manualAssociations[target.ID]
	if !ok {
		return
	}
	applyManualAssociation(target, override)
}

func applyManualAssociation(target *Target, override ManualAssociationOverride) {
	if target == nil || !override.Active {
		return
	}

	target.Callsign = override.Callsign
	target.Beacon = override.Beacon
	target.CWT = override.CWT
	target.Fix = override.Fix
	target.Scratchpad1 = override.Scratchpad1
	target.Scratchpad2 = override.Scratchpad2

	targetType := strings.TrimSpace(override.TargetType)
	if targetType == "" {
		target.TargetType = nil
	} else {
		target.TargetType = stringPointer(targetType)
	}
	target.ForceDataBlock = true
}

func (s *TargetStore) applyManualTags(target *Target) {
	if s == nil || target == nil || s.manualTags == nil {
		return
	}

	override, ok := s.manualTags[target.ID]
	if !ok || !override.Active {
		return
	}

	target.Callsign = strings.ToUpper(strings.TrimSpace(override.AircraftID))
	target.ForceDataBlock = true
	target.ShowDB = true
}

func (s *TargetStore) applyTerminationOverride(target *Target) {
	if s == nil || target == nil || s.terminatedTracks == nil || !s.terminatedTracks[target.ID] {
		return
	}

	target.Callsign = ""
	target.Beacon = ""
	target.CWT = ""
	target.Fix = ""
	target.TargetType = nil
	target.Scratchpad1 = ""
	target.Scratchpad2 = ""

	target.ShowDB = false
	target.ForceDataBlock = false
	target.Suspended = false
	target.Coasting = false
	target.Dropped = false
	target.CoastListID = ""
	target.CoastUntil = time.Time{}
	target.SuspendUntil = time.Time{}
}

func (s *TargetStore) Upsert(t Target) {
	if s == nil || t.ID == "" {
		return
	}
	if s.targets == nil {
		s.targets = make(map[string]*Target)
	}
	if s.history == nil {
		s.history = make(map[string][]TargetHistoryPoint)
	}
	s.applyDatablockOverrides(&t)
	s.applyManualAssociations(&t)
	s.applyManualTags(&t)
	s.applyTerminationOverride(&t)

	if existing := s.targets[t.ID]; existing != nil {
		if existing.PosFeet != t.PosFeet {
			s.history[t.ID] = append(s.history[t.ID], TargetHistoryPoint{
				PosFeet: existing.PosFeet,
			})
			s.trimHistory(t.ID)
			s.hoverRevision++
		}
		*existing = t
		return
	}

	targetCopy := t
	s.targets[t.ID] = &targetCopy
	s.order = append(s.order, t.ID)
	s.hoverRevision++
}

func (s *TargetStore) Remove(id string) {
	if s == nil || s.targets == nil {
		return
	}
	if _, ok := s.targets[id]; !ok {
		return
	}

	delete(s.targets, id)
	delete(s.history, id)
	delete(s.overrides, id)
	delete(s.manualAssociations, id)
	delete(s.manualTags, id)
	delete(s.terminatedTracks, id)
	for i, orderedID := range s.order {
		if orderedID == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	s.hoverRevision++
}

func (s *TargetStore) RemoveLive(id string) {
	if s == nil || s.targets == nil {
		return
	}

	target := s.targets[id]
	if target == nil {
		return
	}
	if target.Suspended {
		target.Live = false
		s.hoverRevision++
		return
	}
	if target.Coasting || target.Dropped {
		return
	}

	s.Remove(id)
}

func (s *TargetStore) Clear() {
	if s == nil {
		return
	}
	if len(s.targets) > 0 {
		s.hoverRevision++
	}
	clear(s.targets)
	clear(s.history)
	clear(s.overrides)
	clear(s.manualAssociations)
	clear(s.manualTags)
	clear(s.terminatedTracks)
	s.order = s.order[:0]
}

func (s *TargetStore) All() []*Target {
	if s == nil || len(s.targets) == 0 {
		return nil
	}

	out := make([]*Target, 0, len(s.targets))
	for _, id := range s.order {
		if target := s.targets[id]; target != nil {
			out = append(out, target)
		}
	}
	return out
}

func (s *TargetStore) History() map[string][]TargetHistoryPoint {
	if s == nil {
		return nil
	}
	return s.history
}

func (s *TargetStore) HoverRevision() uint64 {
	if s == nil {
		return 0
	}
	return s.hoverRevision
}

func (s *TargetStore) TargetByID(id string) *Target {
	if s == nil || s.targets == nil || id == "" {
		return nil
	}
	return s.targets[id]
}

func (s *TargetStore) ToggleAlertsInhibited(targetID string) bool {
	if s == nil || targetID == "" {
		return false
	}

	target := s.TargetByID(targetID)
	if target == nil {
		return false
	}

	target.AlertsInhibited = !target.AlertsInhibited
	s.hoverRevision++
	return true
}

func (s *TargetStore) AlertsInhibited(targetID string) bool {
	target := s.TargetByID(targetID)
	return target != nil && target.AlertsInhibited
}

func (s *TargetStore) AnyAlertsInhibited() bool {
	if s == nil {
		return false
	}

	for _, target := range s.targets {
		if target != nil && target.AlertsInhibited {
			return true
		}
	}
	return false
}

func (s *TargetStore) AlertInhibitedIDs() map[string]bool {
	out := make(map[string]bool)
	if s == nil {
		return out
	}

	for _, target := range s.targets {
		if target != nil && target.AlertsInhibited {
			out[target.ID] = true
		}
	}
	return out
}

func (s *TargetStore) ClearAlertInhibits() {
	if s == nil {
		return
	}

	changed := false
	for _, target := range s.targets {
		if target != nil && target.AlertsInhibited {
			target.AlertsInhibited = false
			changed = true
		}
	}
	if changed {
		s.hoverRevision++
	}
}

func (s *TargetStore) SuspendedCount() int {
	if s == nil {
		return 0
	}

	count := 0
	for _, target := range s.targets {
		if target != nil && target.Suspended {
			count++
		}
	}
	return count
}

func (s *TargetStore) NextAvailableSuspendedTrackID() string {
	if s == nil {
		return ""
	}

	used := make(map[string]bool)
	for _, target := range s.targets {
		if target != nil && target.Suspended && target.CoastListID != "" {
			used[target.CoastListID] = true
		}
	}

	for ch := 'A'; ch <= 'Z'; ch++ {
		id := string(ch)
		if !used[id] {
			return id
		}
	}
	return ""
}

func (s *TargetStore) NextAvailableNumericCoastListID() string {
	if s == nil {
		return ""
	}

	used := make(map[int]bool)
	for _, target := range s.targets {
		if target == nil || (!target.Coasting && !target.Dropped) {
			continue
		}

		value, err := strconv.Atoi(strings.TrimSpace(target.CoastListID))
		if err == nil && value >= 1 && value <= 999 {
			used[value] = true
		}
	}

	for i := 1; i <= 999; i++ {
		if !used[i] {
			return fmt.Sprintf("%03d", i)
		}
	}
	return ""
}

func (s *TargetStore) SuspendedTargetByCoastListID(id string) *Target {
	id = strings.ToUpper(strings.TrimSpace(id))
	if s == nil || id == "" {
		return nil
	}

	for _, target := range s.targets {
		if target == nil || !target.Suspended {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(target.CoastListID), id) {
			return target
		}
	}
	return nil
}

func (s *TargetStore) CoastDropTargetByCoastListID(id string) *Target {
	id = strings.ToUpper(strings.TrimSpace(id))
	if s == nil || id == "" {
		return nil
	}

	for _, target := range s.targets {
		if target == nil || (!target.Coasting && !target.Dropped) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(target.CoastListID), id) {
			return target
		}
	}
	return nil
}

func (s *TargetStore) CoastDropTargetByLogicalKey(key string) *Target {
	return s.coastDropTargetByLogicalKeyExcept(key, "", time.Time{})
}

func (s *TargetStore) coastDropTargetByLogicalKeyExcept(
	key string,
	excludedID string,
	now time.Time,
) *Target {
	key = strings.ToUpper(strings.TrimSpace(key))
	if s == nil || key == "" {
		return nil
	}

	for _, id := range s.order {
		target := s.targets[id]
		if target == nil || target.ID == excludedID ||
			(!target.Coasting && !target.Dropped) {
			continue
		}
		if !now.IsZero() &&
			!target.CoastUntil.IsZero() &&
			!target.CoastUntil.After(now) {
			continue
		}
		if targetLogicalTrackKey(target) == key {
			return target
		}
	}
	return nil
}

func shouldReplaceCoastDrop(existing, candidate *Target) bool {
	if existing == nil {
		return true
	}
	if candidate == nil {
		return false
	}
	return candidate.CoastUntil.After(existing.CoastUntil)
}

func (s *TargetStore) removeDuplicateCoastDropTargets() {
	if s == nil {
		return
	}

	keepers := make(map[string]*Target)
	var remove []string
	for _, id := range s.order {
		target := s.targets[id]
		if target == nil || (!target.Coasting && !target.Dropped) {
			continue
		}

		key := targetLogicalTrackKey(target)
		if key == "" {
			continue
		}

		existing := keepers[key]
		if existing == nil {
			keepers[key] = target
			continue
		}
		if shouldReplaceCoastDrop(existing, target) {
			remove = append(remove, existing.ID)
			keepers[key] = target
		} else {
			remove = append(remove, target.ID)
		}
	}

	for _, id := range remove {
		s.Remove(id)
	}
}

func (s *TargetStore) TargetByCoastListID(id string) *Target {
	id = strings.ToUpper(strings.TrimSpace(id))
	if s == nil || id == "" {
		return nil
	}

	for _, target := range s.targets {
		if target == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(target.CoastListID), id) {
			return target
		}
	}
	return nil
}

func (s *TargetStore) SuspendTarget(targetID string, coastListID string, until time.Time) {
	target := s.TargetByID(targetID)
	if target == nil {
		return
	}

	target.Suspended = true
	target.Coasting = false
	target.Dropped = false
	target.CoastListID = strings.TrimSpace(coastListID)
	target.SuspendUntil = until
	target.CoastUntil = time.Time{}
	target.ShowDB = false
	s.hoverRevision++
}

func (s *TargetStore) AssociateCoastDropTrackWithUnknown(
	coastListID string,
	unknownTargetID string,
) bool {
	if s == nil {
		return false
	}

	source := s.CoastDropTargetByCoastListID(coastListID)
	dest := s.TargetByID(unknownTargetID)
	if source == nil || dest == nil {
		return false
	}
	if !isInitControlUnknownTarget(dest) {
		return false
	}

	targetType := ""
	if source.TargetType != nil {
		targetType = *source.TargetType
	}

	override := ManualAssociationOverride{
		Callsign:    source.Callsign,
		Beacon:      source.Beacon,
		CWT:         source.CWT,
		TargetType:  targetType,
		Fix:         source.Fix,
		Scratchpad1: source.Scratchpad1,
		Scratchpad2: source.Scratchpad2,
		Active:      true,
	}

	if s.manualAssociations == nil {
		s.manualAssociations = make(map[string]ManualAssociationOverride)
	}
	delete(s.terminatedTracks, dest.ID)
	delete(s.manualTags, dest.ID)
	s.manualAssociations[dest.ID] = override
	applyManualAssociation(dest, override)

	dest.ShowDB = true
	dest.Live = true
	dest.Coasting = false
	dest.Dropped = false
	dest.Suspended = false
	dest.CoastListID = ""
	dest.CoastUntil = time.Time{}
	dest.SuspendUntil = time.Time{}

	if source.ID != dest.ID {
		s.Remove(source.ID)
	} else {
		source.Coasting = false
		source.Dropped = false
		source.CoastListID = ""
		source.CoastUntil = time.Time{}
		source.ShowDB = true
	}

	s.hoverRevision++
	return true
}

func (s *TargetStore) ManualTagUnknownTarget(targetID string, aircraftID string) bool {
	aircraftID = strings.ToUpper(strings.TrimSpace(aircraftID))
	if s == nil || targetID == "" || aircraftID == "" {
		return false
	}

	target := s.TargetByID(targetID)
	if target == nil {
		return false
	}
	if !targetIsManualTagCandidate(target) {
		return false
	}

	if s.manualTags == nil {
		s.manualTags = make(map[string]ManualTagOverride)
	}

	delete(s.terminatedTracks, target.ID)
	delete(s.manualAssociations, target.ID)

	s.manualTags[target.ID] = ManualTagOverride{
		AircraftID: aircraftID,
		Active:     true,
	}

	target.Callsign = aircraftID
	target.ForceDataBlock = true
	target.ShowDB = true

	target.Suspended = false
	target.Coasting = false
	target.Dropped = false
	target.CoastListID = ""
	target.CoastUntil = time.Time{}
	target.SuspendUntil = time.Time{}
	target.Live = true

	s.hoverRevision++
	return true
}

func (s *TargetStore) TerminateTrack(targetID string) {
	target := s.TargetByID(targetID)
	if target == nil {
		return
	}

	if target.Coasting || target.Dropped {
		s.Remove(targetID)
		return
	}

	if target.Suspended {
		if !target.Live {
			s.Remove(targetID)
			return
		}
		s.returnLiveTrackToUnknown(target)
		return
	}

	if target.Live && targetCanHaveDataBlock(target) {
		s.returnLiveTrackToUnknown(target)
		return
	}
}

func (s *TargetStore) returnLiveTrackToUnknown(target *Target) {
	if s == nil || target == nil {
		return
	}

	delete(s.overrides, target.ID)
	delete(s.manualAssociations, target.ID)
	delete(s.manualTags, target.ID)
	if s.terminatedTracks == nil {
		s.terminatedTracks = make(map[string]bool)
	}
	s.terminatedTracks[target.ID] = true
	s.applyTerminationOverride(target)

	target.Live = true
	s.hoverRevision++
}

func (s *TargetStore) UnsuspendTarget(targetID string) {
	target := s.TargetByID(targetID)
	if target == nil {
		return
	}
	if !target.Live {
		s.Remove(targetID)
		return
	}

	target.Suspended = false
	target.CoastListID = ""
	target.SuspendUntil = time.Time{}
	target.ShowDB = true
	s.hoverRevision++
}

func (s *TargetStore) TerminateCoastDropTrack(targetID string) {
	target := s.TargetByID(targetID)
	if target == nil {
		return
	}
	if target.Coasting || target.Dropped {
		s.Remove(targetID)
	}
}

func (s *TargetStore) ExpireSuspendedTracks(now time.Time) {
	if s == nil {
		return
	}

	var remove []string
	for _, target := range s.targets {
		if target == nil || !target.Suspended {
			continue
		}
		if target.SuspendUntil.IsZero() || target.SuspendUntil.After(now) {
			continue
		}

		if target.Live {
			target.Suspended = false
			target.CoastListID = ""
			target.SuspendUntil = time.Time{}
			target.ShowDB = true
			s.hoverRevision++
		} else {
			remove = append(remove, target.ID)
		}
	}

	for _, id := range remove {
		s.Remove(id)
	}
}

func (s *TargetStore) ExpireRawUnknownTargets(now time.Time, lifetime time.Duration) {
	if s == nil || lifetime <= 0 {
		return
	}

	var remove []string
	for _, target := range s.targets {
		if !targetIsRawUnknown(target) {
			continue
		}

		last := target.LastSmesFrameTime
		if last.IsZero() {
			last = target.PositionReportTime
		}
		if last.IsZero() || now.Sub(last) < lifetime {
			continue
		}

		remove = append(remove, target.ID)
	}

	for _, id := range remove {
		s.Remove(id)
	}
}

func (s *TargetStore) UpdateCoastDropTracks(
	now time.Time,
	coastDelay time.Duration,
	lifetime time.Duration,
	isDestination func(*Target) bool,
) {
	if s == nil {
		return
	}

	s.removeDuplicateCoastDropTargets()

	var remove []string
	for _, target := range s.targets {
		if target == nil {
			continue
		}
		if target.Suspended {
			continue
		}

		if target.Coasting || target.Dropped {
			if !target.CoastUntil.IsZero() && !target.CoastUntil.After(now) {
				remove = append(remove, target.ID)
			}
			continue
		}
		if !target.Live {
			continue
		}
		if !targetCanHaveDataBlock(target) {
			continue
		}

		last := target.PositionReportTime
		if last.IsZero() {
			last = target.LastSmesFrameTime
		}
		if last.IsZero() || now.Sub(last) < coastDelay {
			continue
		}

		key := targetLogicalTrackKey(target)
		if key != "" {
			existing := s.coastDropTargetByLogicalKeyExcept(key, target.ID, now)
			if existing != nil {
				remove = append(remove, target.ID)
				continue
			}
		}

		coastID := s.NextAvailableNumericCoastListID()
		if coastID == "" {
			continue
		}

		target.Live = false
		target.CoastListID = coastID
		target.CoastUntil = now.Add(lifetime)
		target.Suspended = false

		if isDestination != nil && isDestination(target) {
			target.Dropped = true
			target.Coasting = false
			target.ShowDB = false
		} else {
			target.Coasting = true
			target.Dropped = false
			target.ShowDB = true
		}
		s.hoverRevision++
	}

	for _, id := range remove {
		s.Remove(id)
	}
}

const maxTargetHoverRangeFeet = float32(150)

func (s *TargetStore) NearestTargetID(posFeet redsmath.Vec2) string {
	if s == nil || len(s.targets) == 0 {
		return ""
	}

	maxDistance2 := maxTargetHoverRangeFeet * maxTargetHoverRangeFeet
	bestDistance2 := maxDistance2
	bestID := ""

	for _, id := range s.order {
		target := s.targets[id]
		if target == nil || target.Dropped {
			continue
		}

		delta := target.PosFeet.Sub(posFeet)
		distance2 := delta.X*delta.X + delta.Y*delta.Y
		if distance2 <= bestDistance2 {
			bestDistance2 = distance2
			bestID = target.ID
		}
	}

	return bestID
}

func (s *TargetStore) trimHistory(id string) {
	points := s.history[id]
	if len(points) > len(targetHistoryValues) {
		s.history[id] = points[len(points)-len(targetHistoryValues):]
	}
}

type TargetDelta struct {
	Target Target
	Delete bool
}

func (s *TargetStore) ApplyDelta(delta TargetDelta) {
	if delta.Delete {
		s.Remove(delta.Target.ID)
		return
	}
	s.Upsert(delta.Target)
}

func (s *TargetStore) ApplySmesFrame(frame redsnet.SmesFrame, vm *VideoMap) {
	if s == nil || frame.Key == "" {
		return
	}
	if frame.Removed {
		s.RemoveLive(frame.Key)
		return
	}

	now := time.Now().UTC()
	target := Target{
		ID:                frame.Key,
		ShowDB:            true,
		LastSmesFrameTime: now,
	}
	if existing := s.targets[frame.Key]; existing != nil {
		target = *existing
	}

	applySmesChanged(&target, frame.Changed, vm)
	target.Live = true
	target.LastSmesFrameTime = now
	if !target.Suspended && (target.Coasting || target.Dropped) {
		target.Coasting = false
		target.Dropped = false
		target.CoastListID = ""
		target.CoastUntil = time.Time{}
		target.ShowDB = true
	}
	s.Upsert(target)

	liveTarget := s.TargetByID(target.ID)
	key := targetLogicalTrackKey(liveTarget)
	for key != "" {
		old := s.coastDropTargetByLogicalKeyExcept(key, target.ID, time.Time{})
		if old == nil {
			break
		}
		s.Remove(old.ID)
	}
}

func applySmesChanged(target *Target, changed map[string]json.RawMessage, vm *VideoMap) {
	if target == nil {
		return
	}

	if value, present, clear := changedString(changed, "callsign"); present {
		target.Callsign = clearedString(value, clear)
	}
	if value, present, clear := changedString(changed, "squawk"); present {
		target.Beacon = clearedString(value, clear)
	}
	if value, present, clear := changedString(changed, "exitFix"); present {
		target.Fix = clearedString(value, clear)
	}
	if value, present, clear := changedString(changed, "wake"); present {
		target.CWT = clearedString(value, clear)
	}
	if value, present, clear := changedString(changed, "scratchpad1"); present {
		target.Scratchpad1 = normalizeScratchpad(clearedString(value, clear))
	}
	if value, present, clear := changedString(changed, "scratchpad2"); present {
		target.Scratchpad2 = normalizeScratchpad(clearedString(value, clear))
	}
	if value, present, clear := changedTime(changed, "positionReportTime"); present {
		if clear {
			target.PositionReportTime = time.Time{}
		} else {
			target.PositionReportTime = value
		}
	}

	if value, present, clear := changedString(changed, "acType"); present {
		if clear || value == "" {
			target.TargetType = nil
		} else {
			target.TargetType = stringPointer(value)
		}
	}
	if value, present, clear := changedString(changed, "tgtType"); present {
		switch {
		case clear:
			target.TargetType = nil
		case strings.EqualFold(value, "vehicle"), strings.EqualFold(value, "VEH"):
			target.TargetType = stringPointer("VEH")
		case strings.EqualFold(value, "unknown") && target.TargetType == nil:
			target.TargetType = nil
		}
	}

	positionChanged := false
	if value, present, clear := changedFloat64(changed, "lat"); present {
		positionChanged = true
		if clear {
			target.Lat = 0
		} else {
			target.Lat = value
		}
	}
	if value, present, clear := changedFloat64(changed, "lon"); present {
		positionChanged = true
		if clear {
			target.Lon = 0
		} else {
			target.Lon = value
		}
	}
	if positionChanged {
		target.PosFeet = redsmath.Vec2{}
		if vm != nil && target.Lat != 0 && target.Lon != 0 {
			target.PosFeet = vm.LonLatToFeet(target.Lon, target.Lat)
		}
	}

	if value, present, clear := changedFloat64(changed, "altitude"); present {
		if clear {
			target.AltitudeFt = 0
			target.HasAltitude = false
		} else {
			target.AltitudeFt = int(stdmath.Round(value))
			target.HasAltitude = true
		}
	}
	if value, present, clear := changedFloat64(changed, "speed"); present {
		if clear {
			target.GroundSpeedKt = 0
		} else {
			target.GroundSpeedKt = float32(value)
		}
	}
	if value, present, clear := changedFloat64(changed, "heading"); present {
		if clear {
			target.HeadingDeg = 0
		} else {
			target.HeadingDeg = float32(value)
		}
	}
}

func changedString(changed map[string]json.RawMessage, key string) (string, bool, bool) {
	raw, present := changed[key]
	if !present {
		return "", false, false
	}
	if isJSONNull(raw) {
		return "", true, true
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false, false
	}
	return value, true, false
}

func changedFloat64(changed map[string]json.RawMessage, key string) (float64, bool, bool) {
	raw, present := changed[key]
	if !present {
		return 0, false, false
	}
	if isJSONNull(raw) {
		return 0, true, true
	}

	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false, false
	}
	return value, true, false
}

func changedTime(changed map[string]json.RawMessage, key string) (time.Time, bool, bool) {
	raw, present := changed[key]
	if !present {
		return time.Time{}, false, false
	}
	if isJSONNull(raw) {
		return time.Time{}, true, true
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return time.Time{}, false, false
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, true, true
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC(), true, false
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), true, false
	}
	return time.Time{}, false, false
}

func isJSONNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}

func clearedString(value string, clear bool) string {
	if clear {
		return ""
	}
	return value
}

func normalizeScratchpad(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "none") {
		return ""
	}
	return value
}

func stringPointer(value string) *string {
	return &value
}

type TargetDrawOptions struct {
	VectorSeconds int

	ShowHistory   bool
	HistoryLength int

	Brightness int

	ScopeRotationDeg int

	VectorVisible func(*Target) bool

	HighlightedTargetID string

	AlertTargetIDs map[string]bool
	AlertFlashOn   bool

	AlertInhibitedTargetIDs map[string]bool
}

func DrawTargets(
	targets []*Target,
	history map[string][]TargetHistoryPoint,
	cb *renderer.CmdBuffer,
	opts TargetDrawOptions,
) {
	if cb == nil {
		return
	}
	if opts.Brightness == 0 {
		opts.Brightness = brightnessDefault
	}

	addHistoryDots(targets, history, cb, opts)
	addAlertInhibitSquares(targets, cb, opts)
	addAlertOctagons(targets, cb, opts)
	addHighlightRings(targets, cb, opts.Brightness, opts.HighlightedTargetID, opts.AlertTargetIDs)
	addTargetSymbols(targets, cb, opts)
	addSuspendedTargetIcons(targets, cb, opts.Brightness, opts.ScopeRotationDeg, opts.HighlightedTargetID)
	addTargetVectors(targets, cb, opts)
}

type targetClass int

const (
	targetClassUnknown targetClass = iota
	targetClassVehicle
	targetClassAircraft
	targetClassHeavyAircraft
)

func classifyTarget(t *Target) targetClass {
	if t == nil || t.TargetType == nil || strings.TrimSpace(*t.TargetType) == "" {
		return targetClassUnknown
	}

	if strings.EqualFold(strings.TrimSpace(*t.TargetType), "VEH") {
		callsign := strings.TrimSpace(t.Callsign)
		if callsign != "" && !strings.EqualFold(callsign, "UNKN") {
			return targetClassVehicle
		}
		return targetClassUnknown
	}

	if isHeavyCWT(t.CWT) {
		return targetClassHeavyAircraft
	}
	return targetClassAircraft
}

func targetUsesUnknownPolygon(class targetClass) bool {
	return class == targetClassUnknown || class == targetClassVehicle
}

func targetHasDatablock(class targetClass) bool {
	return class == targetClassVehicle ||
		class == targetClassAircraft ||
		class == targetClassHeavyAircraft
}

func targetCanHaveDataBlock(target *Target) bool {
	if target == nil {
		return false
	}
	return target.ForceDataBlock || targetHasDatablock(classifyTarget(target))
}

func targetLogicalTrackKey(target *Target) string {
	if target == nil || !targetCanHaveDataBlock(target) {
		return ""
	}

	if callsign := strings.ToUpper(strings.TrimSpace(target.Callsign)); callsign != "" {
		return "ACID:" + callsign
	}
	if beacon := strings.ToUpper(strings.TrimSpace(target.Beacon)); beacon != "" {
		return "BCN:" + beacon
	}
	return ""
}

func targetIsRawUnknown(target *Target) bool {
	if target == nil {
		return false
	}
	if !target.Live || target.Suspended || target.Coasting || target.Dropped {
		return false
	}
	if target.ForceDataBlock {
		return false
	}
	return classifyTarget(target) == targetClassUnknown && !targetCanHaveDataBlock(target)
}

func isInitControlUnknownTarget(target *Target) bool {
	if target == nil {
		return false
	}
	if !target.Live || target.Suspended || target.Coasting || target.Dropped {
		return false
	}
	return classifyTarget(target) == targetClassUnknown && !targetCanHaveDataBlock(target)
}

func targetIsManualTagCandidate(target *Target) bool {
	if target == nil {
		return false
	}
	if !target.Live || target.Suspended || target.Coasting || target.Dropped {
		return false
	}
	return classifyTarget(target) == targetClassUnknown && !targetCanHaveDataBlock(target)
}

func isHeavyCWT(cwt string) bool {
	switch cwt {
	case "A", "B", "C", "D", "E":
		return true
	default:
		return false
	}
}

type targetRGBRole int

const (
	targetRGBNormal targetRGBRole = iota
	targetRGBHeavy
	targetRGBUnknown
	targetRGBVehicle
	targetRGBVector
	targetRGBHighlight
	targetRGBSuspendedOuter
	targetRGBSuspendedInner
	targetRGBSuspendedSelectedInner
	targetRGBAlert
)

// Gildea, K. M. (2018), Development of a Standard Palette for Color Coding ATC Displays,
// FAA Office of Aerospace Medicine Technical Report DOT/FAA/AM-18/18
var targetVehiclePink = renderer.RGB8(232, 76, 253)

func targetRGB(role targetRGBRole, brightness int) renderer.RGB {
	floor := 0

	var base renderer.RGB
	switch role {
	case targetRGBNormal:
		base = renderer.RGB8(248, 248, 248)
	case targetRGBHeavy:
		base = renderer.RGB8(248, 128, 0)
	case targetRGBUnknown:
		base = renderer.RGB8(0, 255, 255)
	case targetRGBVehicle:
		base = targetVehiclePink
	case targetRGBVector:
		base = renderer.RGB8(140, 140, 140)
		floor = 20
	case targetRGBHighlight:
		base = renderer.RGB8(255, 255, 255)
	case targetRGBSuspendedOuter:
		base = renderer.RGB8(0, 255, 255)
		floor = 20
	case targetRGBSuspendedInner:
		base = renderer.RGB8(128, 128, 128)
		floor = 20
	case targetRGBSuspendedSelectedInner:
		base = renderer.RGB8(255, 255, 255)
		floor = 20
	case targetRGBAlert:
		base = renderer.RGB8(255, 0, 0)
		floor = 20
	default:
		base = renderer.RGB8(248, 248, 248)
	}
	return applyBrightness(base, brightness, floor)
}

func targetClassRGB(class targetClass, brightness int) renderer.RGB {
	switch class {
	case targetClassAircraft:
		return targetRGB(targetRGBNormal, brightness)
	case targetClassHeavyAircraft:
		return targetRGB(targetRGBHeavy, brightness)
	case targetClassVehicle:
		return targetRGB(targetRGBVehicle, brightness)
	default:
		return targetRGB(targetRGBUnknown, brightness)
	}
}

var targetHistoryValues = [...]uint8{219, 187, 161, 138, 118, 101, 87}

func historyRGB(age int, brightness int) renderer.RGB {
	if age < 0 {
		age = 0
	}
	if age >= len(targetHistoryValues) {
		age = len(targetHistoryValues) - 1
	}

	value := targetHistoryValues[age]
	return applyBrightness(renderer.RGB8(value, value, value), brightness, 20)
}

const feetPerDegree = float32(364560.0)

var aircraftPolygon = scaledPolygon([]redsmath.Vec2{
	{X: -0.000142545, Y: 0.0},
	{X: -0.0001607125, Y: 2.09625e-05},
	{X: -0.0001607125, Y: 6.9875e-05},
	{X: -0.000142545, Y: 6.9875e-05},
	{X: -0.0001118, Y: 2.795e-05},
	{X: -5.59e-05, Y: 2.795e-05},
	{X: -8.385e-05, Y: 0.000151293},
	{X: -5.59e-05, Y: 0.000151293},
	{X: 1.3975e-05, Y: 2.795e-05},
	{X: 0.000120185, Y: 2.38175e-05},
	{X: 0.000137155, Y: 1.53625e-05},
	{X: 0.0001439425, Y: 8.385e-06},
	{X: 0.0001439425, Y: -8.385e-06},
	{X: 0.000137155, Y: -1.53625e-05},
	{X: 0.000120185, Y: -2.38175e-05},
	{X: 1.3975e-05, Y: -2.795e-05},
	{X: -5.59e-05, Y: -0.000151293},
	{X: -8.385e-05, Y: -0.000151293},
	{X: -5.59e-05, Y: -2.795e-05},
	{X: -0.0001118, Y: -2.795e-05},
	{X: -0.000142545, Y: -6.9875e-05},
	{X: -0.0001607125, Y: -6.9875e-05},
	{X: -0.0001607125, Y: -2.09625e-05},
})

var unknownPolygon = scaledPolygon([]redsmath.Vec2{
	{X: -2.5e-05, Y: 7.5e-05},
	{X: -0.000125, Y: 0.0},
	{X: -2.5e-05, Y: -7.5e-05},
	{X: 0.000175, Y: 0.0},
})

type polygonMesh struct {
	Vertices []redsmath.Vec2
	Indices  []uint32
}

var aircraftMesh = tessellatedTargetPolygon(aircraftPolygon)

var unknownMesh = polygonMesh{
	Vertices: unknownPolygon,
	Indices:  triangleFanIndices(len(unknownPolygon)),
}

const highlightRingRadiusFeet = 0.012 * redsmath.FeetPerNM

var highlightRingPolygon = regularRingPolygon(20, highlightRingRadiusFeet)

const alertOctagonRadiusFeet = 0.012 * redsmath.FeetPerNM

var alertOctagonPolygon = regularRingPolygon(20, alertOctagonRadiusFeet)

const alertInhibitSquareRadiusFeet = float32(3.0 / 160.0 * redsmath.FeetPerNM)

var alertInhibitSquarePolygon = regularRingPolygon(4, alertInhibitSquareRadiusFeet)

const (
	suspendedOuterHalfSizeFeet = float32(65)
	suspendedInnerHalfSizeFeet = float32(55)
	suspendedLabelFontSize     = 2
)

func regularRingPolygon(sides int, radiusFeet float32) []redsmath.Vec2 {
	if sides < 3 || radiusFeet <= 0 {
		return nil
	}

	points := make([]redsmath.Vec2, 0, sides+1)
	for i := 0; i <= sides; i++ {
		radians := float64(i) / float64(sides) * 2 * stdmath.Pi
		points = append(points, redsmath.Vec2{
			X: radiusFeet * float32(stdmath.Cos(radians)),
			Y: radiusFeet * float32(stdmath.Sin(radians)),
		})
	}
	return points
}

func scaledPolygon(points []redsmath.Vec2) []redsmath.Vec2 {
	out := make([]redsmath.Vec2, len(points))
	for i, point := range points {
		out[i] = point.Mul(feetPerDegree)
	}
	return out
}

func tessellatedTargetPolygon(polygon []redsmath.Vec2) polygonMesh {
	vertices, indices := renderer.TessellateRings([][]redsmath.Vec2{polygon})
	if len(vertices) == 0 || len(indices) == 0 {
		return polygonMesh{
			Vertices: polygon,
			Indices:  triangleFanIndices(len(polygon)),
		}
	}

	out := make([]redsmath.Vec2, len(vertices))
	for i, vertex := range vertices {
		out[i] = redsmath.Vec2{X: vertex.X, Y: vertex.Y}
	}
	return polygonMesh{Vertices: out, Indices: indices}
}

func triangleFanIndices(vertexCount int) []uint32 {
	if vertexCount < 3 {
		return nil
	}
	indices := make([]uint32, 0, (vertexCount-2)*3)
	for i := 1; i+1 < vertexCount; i++ {
		indices = append(indices, 0, uint32(i), uint32(i+1))
	}
	return indices
}

func addHighlightRings(
	targets []*Target,
	cb *renderer.CmdBuffer,
	brightness int,
	highlightedID string,
	alertTargetIDs map[string]bool,
) {
	if highlightedID == "" || alertTargetIDs[highlightedID] {
		return
	}

	builder := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(builder)

	for _, target := range targets {
		if target == nil || target.ID != highlightedID || target.Suspended || target.Dropped {
			continue
		}

		scale := float32(1)
		if classifyTarget(target) == targetClassHeavyAircraft {
			scale = 1.5
		}

		points := make([]renderer.PointVertex, 0, len(highlightRingPolygon))
		for _, point := range highlightRingPolygon {
			position := target.PosFeet.Add(point.Mul(scale))
			points = append(points, renderer.PointVertex{X: position.X, Y: position.Y})
		}
		builder.AddLineStrip(points)
		break
	}

	cb.SetRGB(targetRGB(targetRGBHighlight, brightness))
	cb.LineWidth(1)
	builder.GenerateCommands(cb)
}

func addAlertOctagons(
	targets []*Target,
	cb *renderer.CmdBuffer,
	opts TargetDrawOptions,
) {
	if cb == nil || len(opts.AlertTargetIDs) == 0 {
		return
	}

	builder := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(builder)

	// CRC draws the octagon with rotateWithWorld=false and a 22.5 degree
	// rotation, so counter the scope rotation in world-matrix rendering.
	rotation := float32(22.5) - float32(opts.ScopeRotationDeg)
	for _, target := range targets {
		if target == nil || target.Suspended || target.Dropped || !opts.AlertTargetIDs[target.ID] {
			continue
		}

		scale := float32(1)
		if classifyTarget(target) == targetClassHeavyAircraft {
			scale = 1.5
		}

		points := make([]renderer.PointVertex, 0, len(alertOctagonPolygon))
		for _, point := range alertOctagonPolygon {
			position := rotateScaleTranslate(point, target.PosFeet, rotation, scale)
			points = append(points, renderer.PointVertex{X: position.X, Y: position.Y})
		}
		builder.AddLineStrip(points)
	}

	cb.SetRGB(targetRGB(targetRGBAlert, opts.Brightness))
	cb.LineWidth(2)
	builder.GenerateCommands(cb)
}

func addAlertInhibitSquares(
	targets []*Target,
	cb *renderer.CmdBuffer,
	opts TargetDrawOptions,
) {
	if cb == nil || len(opts.AlertInhibitedTargetIDs) == 0 {
		return
	}

	builders := map[targetClass]*renderer.LinesBuilder{
		targetClassUnknown:       renderer.GetLinesBuilder(),
		targetClassVehicle:       renderer.GetLinesBuilder(),
		targetClassAircraft:      renderer.GetLinesBuilder(),
		targetClassHeavyAircraft: renderer.GetLinesBuilder(),
	}
	defer func() {
		for _, builder := range builders {
			renderer.ReturnLinesBuilder(builder)
		}
	}()

	rotation := float32(45) - float32(opts.ScopeRotationDeg)
	for _, target := range targets {
		if target == nil || target.Suspended || target.Dropped ||
			!opts.AlertInhibitedTargetIDs[target.ID] {
			continue
		}

		class := classifyTarget(target)
		builder := builders[class]

		points := make([]renderer.PointVertex, 0, len(alertInhibitSquarePolygon))
		for _, point := range alertInhibitSquarePolygon {
			position := rotateScaleTranslate(point, target.PosFeet, rotation, 1)
			points = append(points, renderer.PointVertex{X: position.X, Y: position.Y})
		}
		builder.AddLineStrip(points)
	}

	cb.LineWidth(2)
	for _, class := range []targetClass{
		targetClassAircraft,
		targetClassHeavyAircraft,
		targetClassUnknown,
		targetClassVehicle,
	} {
		cb.SetRGB(targetClassRGB(class, opts.Brightness))
		builders[class].GenerateCommands(cb)
	}
}

func addTargetSymbols(targets []*Target, cb *renderer.CmdBuffer, opts TargetDrawOptions) {
	builders := map[targetClass]*renderer.TrianglesBuilder{
		targetClassUnknown:       renderer.GetTrianglesBuilder(),
		targetClassVehicle:       renderer.GetTrianglesBuilder(),
		targetClassAircraft:      renderer.GetTrianglesBuilder(),
		targetClassHeavyAircraft: renderer.GetTrianglesBuilder(),
	}
	alertBuilder := renderer.GetTrianglesBuilder()
	defer func() {
		for _, builder := range builders {
			renderer.ReturnTrianglesBuilder(builder)
		}
		renderer.ReturnTrianglesBuilder(alertBuilder)
	}()

	for _, target := range targets {
		if target == nil || target.Suspended || target.Dropped {
			continue
		}

		class := classifyTarget(target)
		mesh := unknownMesh
		scale := float32(1)
		if !targetUsesUnknownPolygon(class) {
			mesh = aircraftMesh
		}
		if class == targetClassHeavyAircraft {
			scale = 1.5
		}

		builder := builders[class]
		if opts.AlertFlashOn && opts.AlertTargetIDs[target.ID] {
			builder = alertBuilder
		}

		addTransformedIndexed(
			builder,
			mesh.Vertices,
			mesh.Indices,
			target.PosFeet,
			90-target.HeadingDeg,
			scale,
		)
	}

	for _, class := range []targetClass{
		targetClassAircraft,
		targetClassHeavyAircraft,
		targetClassUnknown,
		targetClassVehicle,
	} {
		cb.SetRGB(targetClassRGB(class, opts.Brightness))
		builders[class].GenerateCommands(cb, renderer.DrawSolid, 0)
	}

	cb.SetRGB(targetRGB(targetRGBAlert, opts.Brightness))
	alertBuilder.GenerateCommands(cb, renderer.DrawSolid, 0)
}

func addSuspendedTargetIcons(
	targets []*Target,
	cb *renderer.CmdBuffer,
	brightness int,
	scopeRotationDeg int,
	highlightedID string,
) {
	outerBuilder := renderer.GetTrianglesBuilder()
	innerBuilder := renderer.GetTrianglesBuilder()
	selectedInnerBuilder := renderer.GetTrianglesBuilder()
	defer renderer.ReturnTrianglesBuilder(outerBuilder)
	defer renderer.ReturnTrianglesBuilder(innerBuilder)
	defer renderer.ReturnTrianglesBuilder(selectedInnerBuilder)

	inverseRotation := -float32(scopeRotationDeg)
	for _, target := range targets {
		if target == nil || !target.Suspended || target.Dropped {
			continue
		}

		outer := screenAlignedSquare(target.PosFeet, suspendedOuterHalfSizeFeet, inverseRotation)
		inner := screenAlignedSquare(target.PosFeet, suspendedInnerHalfSizeFeet, inverseRotation)

		outerBuilder.AddQuad(
			pointVertex(outer[0]),
			pointVertex(outer[1]),
			pointVertex(outer[2]),
			pointVertex(outer[3]),
		)

		fillBuilder := innerBuilder
		if target.ID == highlightedID {
			fillBuilder = selectedInnerBuilder
		}
		fillBuilder.AddQuad(
			pointVertex(inner[0]),
			pointVertex(inner[1]),
			pointVertex(inner[2]),
			pointVertex(inner[3]),
		)
	}

	cb.SetRGB(targetRGB(targetRGBSuspendedOuter, brightness))
	outerBuilder.GenerateCommands(cb, renderer.DrawSolid, 0)

	cb.SetRGB(targetRGB(targetRGBSuspendedInner, brightness))
	innerBuilder.GenerateCommands(cb, renderer.DrawSolid, 0)

	cb.SetRGB(targetRGB(targetRGBSuspendedSelectedInner, brightness))
	selectedInnerBuilder.GenerateCommands(cb, renderer.DrawSolid, 0)
}

func screenAlignedSquare(
	center redsmath.Vec2,
	halfSizeFeet float32,
	inverseRotationDeg float32,
) []redsmath.Vec2 {
	corners := []redsmath.Vec2{
		{X: -halfSizeFeet, Y: -halfSizeFeet},
		{X: halfSizeFeet, Y: -halfSizeFeet},
		{X: halfSizeFeet, Y: halfSizeFeet},
		{X: -halfSizeFeet, Y: halfSizeFeet},
	}

	out := make([]redsmath.Vec2, 0, len(corners))
	for _, corner := range corners {
		out = append(out, rotateScaleTranslate(corner, center, inverseRotationDeg, 1))
	}
	return out
}

func pointVertex(point redsmath.Vec2) renderer.PointVertex {
	return renderer.PointVertex{X: point.X, Y: point.Y}
}

func DrawSuspendedTargetLabels(
	targets []*Target,
	cb *renderer.CmdBuffer,
	transforms radar.ScopeTransformations,
	font *renderer.BitmapFont,
	textureID renderer.TextureID,
) {
	if cb == nil || font == nil || textureID == 0 {
		return
	}

	td := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(td)

	td.SetFont(font)
	lineHeight := font.LineHeight(suspendedLabelFontSize)
	style := renderer.TextStyle{
		Size:  suspendedLabelFontSize,
		Color: renderer.RGB8(0, 0, 0).ToRGBA(),
	}

	for _, target := range targets {
		if target == nil || !target.Suspended || target.Dropped || strings.TrimSpace(target.CoastListID) == "" {
			continue
		}

		label := truncateRunes(strings.TrimSpace(target.CoastListID), 1)
		width, _ := font.MeasureText(label, suspendedLabelFontSize)
		center := transforms.WindowFromWorldP(target.PosFeet)
		topLeft := redsmath.Vec2{
			X: center.X - float32(width)/2,
			Y: center.Y - float32(lineHeight)/2,
		}
		td.AddText(label, topLeft, style)
	}

	td.GenerateCommands(cb, textureID)
}

func addTransformedIndexed(
	builder *renderer.TrianglesBuilder,
	points []redsmath.Vec2,
	indices []uint32,
	pos redsmath.Vec2,
	rotationDeg float32,
	scale float32,
) {
	if builder == nil {
		return
	}

	transformed := make([]renderer.PointVertex, 0, len(points))
	for _, point := range points {
		q := rotateScaleTranslate(point, pos, rotationDeg, scale)
		transformed = append(transformed, renderer.PointVertex{X: q.X, Y: q.Y})
	}
	builder.AddIndexed(transformed, indices)
}

func addTranslatedIndexed(
	builder *renderer.TrianglesBuilder,
	points []redsmath.Vec2,
	indices []uint32,
	pos redsmath.Vec2,
) {
	addTransformedIndexed(builder, points, indices, pos, 0, 1)
}

func rotateScaleTranslate(
	point redsmath.Vec2,
	pos redsmath.Vec2,
	rotationDeg float32,
	scale float32,
) redsmath.Vec2 {
	radians := float64(rotationDeg) * stdmath.Pi / 180
	cos := float32(stdmath.Cos(radians))
	sin := float32(stdmath.Sin(radians))

	x := point.X * scale
	y := point.Y * scale

	return redsmath.Vec2{
		X: pos.X + x*cos - y*sin,
		Y: pos.Y + x*sin + y*cos,
	}
}

const (
	minTargetVectorSeconds = 1
	maxTargetVectorSeconds = 20
)

func ClampedTargetVectorSeconds(seconds int) int {
	if seconds < minTargetVectorSeconds {
		return minTargetVectorSeconds
	}
	if seconds > maxTargetVectorSeconds {
		return maxTargetVectorSeconds
	}
	return seconds
}

func vectorEndFeet(
	start redsmath.Vec2,
	groundSpeedKt float32,
	trackDeg float32,
	vectorSeconds int,
) redsmath.Vec2 {
	distanceNM := groundSpeedKt * float32(vectorSeconds) / 3600
	distanceFeet := distanceNM * redsmath.FeetPerNM
	radians := float64(trackDeg) * stdmath.Pi / 180

	return redsmath.Vec2{
		X: start.X + distanceFeet*float32(stdmath.Sin(radians)),
		Y: start.Y + distanceFeet*float32(stdmath.Cos(radians)),
	}
}

func addTargetVectors(
	targets []*Target,
	cb *renderer.CmdBuffer,
	opts TargetDrawOptions,
) {
	builder := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(builder)

	vectorSeconds := ClampedTargetVectorSeconds(opts.VectorSeconds)
	for _, target := range targets {
		if target == nil || target.Suspended || target.Coasting || target.Dropped || target.GroundSpeedKt <= 0 {
			continue
		}
		if opts.VectorVisible != nil && !opts.VectorVisible(target) {
			continue
		}

		end := vectorEndFeet(
			target.PosFeet,
			target.GroundSpeedKt,
			target.HeadingDeg,
			vectorSeconds,
		)
		builder.AddLine(
			renderer.PointVertex{X: target.PosFeet.X, Y: target.PosFeet.Y},
			renderer.PointVertex{X: end.X, Y: end.Y},
		)
	}

	cb.SetRGB(targetRGB(targetRGBVector, opts.Brightness))
	cb.LineWidth(1)
	builder.GenerateCommands(cb)
}

const historyDotRadiusFeet = 0.003 * redsmath.FeetPerNM

func addHistoryDots(
	targets []*Target,
	history map[string][]TargetHistoryPoint,
	cb *renderer.CmdBuffer,
	opts TargetDrawOptions,
) {
	if !opts.ShowHistory {
		return
	}

	maxHistory := opts.HistoryLength
	if maxHistory < 1 {
		maxHistory = 1
	}
	if maxHistory > len(targetHistoryValues) {
		maxHistory = len(targetHistoryValues)
	}

	dotPolygon := circlePolygon(historyDotRadiusFeet, 12)
	dotIndices := circleFanIndices(12)

	for colorIndex := 0; colorIndex < maxHistory; colorIndex++ {
		builder := renderer.GetTrianglesBuilder()

		for _, target := range targets {
			if target == nil || target.Dropped {
				continue
			}

			points := history[target.ID]
			count := len(points)
			if count > maxHistory {
				count = maxHistory
			}

			start := len(points) - count
			for i := 0; i < count; i++ {
				ageFromNewest := count - 1 - i
				if ageFromNewest == colorIndex {
					addTranslatedIndexed(builder, dotPolygon, dotIndices, points[start+i].PosFeet)
				}
			}
		}

		cb.SetRGB(historyRGB(colorIndex, opts.Brightness))
		builder.GenerateCommands(cb, renderer.DrawSolid, 0)
		renderer.ReturnTrianglesBuilder(builder)
	}
}

func circlePolygon(radius float32, segments int) []redsmath.Vec2 {
	if radius <= 0 || segments < 3 {
		return nil
	}

	points := make([]redsmath.Vec2, 1, segments+1)
	for i := 0; i < segments; i++ {
		radians := float64(i) / float64(segments) * 2 * stdmath.Pi
		points = append(points, redsmath.Vec2{
			X: radius * float32(stdmath.Cos(radians)),
			Y: radius * float32(stdmath.Sin(radians)),
		})
	}
	return points
}

func circleFanIndices(segments int) []uint32 {
	if segments < 3 {
		return nil
	}

	indices := make([]uint32, 0, segments*3)
	for i := 0; i < segments; i++ {
		indices = append(indices, 0, uint32(i+1), uint32((i+1)%segments+1))
	}
	return indices
}
