package stars

// CRC-compatible CRDA geometry and target qualification. This file intentionally
// has no dependency on live TAIS snapshots, renderer state, or controller input:
// a subsequent integration patch owns conversion and display preferences.
//
// Reference: CRC Stars.Crda/CrdaManager.cs, GeoPoint.cs, GeoCalc.cs, NavCalc.cs.

import (
	"fmt"
	"math"
	"strings"
)

const (
	crdaMaxGroundSpeedKnots = 350
	crdaEarthRadiusNM       = 3437.670013352 // CRC GeoCalc.GreatCircleDistance
)

type crdaMode uint8

const (
	crdaDisabled crdaMode = iota
	crdaStagger
	crdaTie
)

// Runtime state is deliberately separate from immutable CRC adaptation.
// The caller supplies one element per RPC in the unchanged JSON order.
type crdaPairState struct {
	Mode          crdaMode
	MasterEnabled bool
	SlaveEnabled  bool
}

// crdaTrack is a normalized snapshot of the fields required by CRC.
// CourseTrue is TRUE degrees; MagVar follows CRC convention (magnetic = true
// + MagVar). AdjustedAltitudeHundreds is in units of 100 ft, like STARS
// displayed Mode C (e.g. 20 => 2,000 ft). Do not pass raw TAIS feet here.
// Has* flags distinguish a legitimate zero value from a missing report.
type crdaTrack struct {
	Position                         configPoint
	CourseTrue                       float64
	GroundSpeedKnots                 float64
	AdjustedAltitudeHundreds         int
	Scratchpad1, Scratchpad2         string
	Associated, CoastPhase2          bool
	HasCourse, HasSpeed, HasAltitude bool
	ForceQualification               bool // bypass heading/altitude/scratchpads, NOT region/speed
	ForceGhost                       bool // allow the source runway even when its ghosting is off
}

type crdaGhost struct {
	Position     configPoint
	RPCID        string
	RPCIndex     int
	AirportID    string
	SourceRunway string
	MasterSource bool
	Symbol       string
	Mode         crdaMode
}

type crdaRegion struct {
	Points [4]configPoint // near-left, far-left, far-right, near-right
}

type crdaPair struct {
	adaptation rpcConfig
	regions    [2]crdaRegion
}

type crdaEngine struct {
	pairs  []crdaPair
	magVar float64
}

// newCRDAEngine precomputes the two qualification trapezoids for each RPC.
// No per-target polygon construction is necessary during radar updates.
func newCRDAEngine(rpcs []rpcConfig, magneticVariation float64) (*crdaEngine, error) {
	if !crdaFinite(magneticVariation) {
		return nil, fmt.Errorf("CRDA magnetic variation is not finite")
	}
	e := &crdaEngine{pairs: make([]crdaPair, 0, len(rpcs)), magVar: magneticVariation}
	for i, rpc := range rpcs {
		master, err := crdaBuildRegion(rpc.MasterRunway, magneticVariation)
		if err != nil {
			return nil, fmt.Errorf("CRDA RPC %d master %q: %w", i+1, rpc.MasterRunway.RunwayID, err)
		}
		slave, err := crdaBuildRegion(rpc.SlaveRunway, magneticVariation)
		if err != nil {
			return nil, fmt.Errorf("CRDA RPC %d slave %q: %w", i+1, rpc.SlaveRunway.RunwayID, err)
		}
		e.pairs = append(e.pairs, crdaPair{adaptation: rpc, regions: [2]crdaRegion{master, slave}})
	}
	return e, nil
}

func crdaFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func crdaValidPoint(p configPoint) bool {
	return crdaFinite(p.Lat) && crdaFinite(p.Lon) && p.Lat >= -90 && p.Lat <= 90 && p.Lon >= -180 && p.Lon <= 180
}

func crdaNormalizeHeading(h float64) float64 {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	return h
}

func crdaHeadingDelta(a, b float64) float64 {
	d := math.Abs(crdaNormalizeHeading(a) - crdaNormalizeHeading(b))
	if d > 180 {
		d = 360 - d
	}
	return d
}

// CRC GeoCalc.LongitudeScalingFactor: approximately cos(latitude), scaled by
// 59.9986602615/60. Retain CRC's planar degree model rather than mixing it
// with a different map projection inside ghost translation.
func crdaLongitudeScale(lat float64) float64 {
	return (59.998660261511354 / 60.0) * math.Cos(lat*math.Pi/180)
}

// crdaVector matches GeoPoint.AddVector: distances in angular degrees,
// bearing MAGNETIC, longitude corrected by the supplied reference scale.
func crdaVector(origin configPoint, distanceDeg, bearingMag, scale, magVar float64) configPoint {
	a := (bearingMag - magVar) * math.Pi / 180
	return configPoint{
		Lat: origin.Lat + distanceDeg*math.Cos(a),
		Lon: origin.Lon + distanceDeg*math.Sin(a)/scale,
	}
}

func crdaBearing(origin, point configPoint, scale, magVar float64) float64 {
	dx := (point.Lon - origin.Lon) * scale
	dy := point.Lat - origin.Lat
	return crdaNormalizeHeading(math.Atan2(dx, dy)*180/math.Pi + magVar)
}

func crdaDistanceDegrees(a, b configPoint, scale float64) float64 {
	return math.Hypot((b.Lon-a.Lon)*scale, b.Lat-a.Lat)
}

func crdaGreatCircleNM(a, b configPoint) float64 {
	lat1, lat2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dlat, dlon := lat2-lat1, (b.Lon-a.Lon)*math.Pi/180
	h := math.Pow(math.Sin(dlat/2), 2) + math.Cos(lat1)*math.Cos(lat2)*math.Pow(math.Sin(dlon/2), 2)
	h = math.Max(0, math.Min(1, h))
	return 2 * crdaEarthRadiusNM * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))
}

// crdaBuildRegion implements CRC CalculateQualificationRegion. Reference
// heading is the INBOUND course; the region extends outward on its reciprocal.
func crdaBuildRegion(r rpcRunwayConfig, magVar float64) (crdaRegion, error) {
	if !crdaValidPoint(r.TargetReferencePoint) || !crdaValidPoint(r.ImageReferencePoint) {
		return crdaRegion{}, fmt.Errorf("invalid target/image reference point")
	}
	for _, pair := range []struct {
		name  string
		value float64
	}{
		{"nearSideDistance", r.NearSideDistance}, {"regionLength", r.RegionLength},
		{"nearSideHalfWidth", r.NearSideHalfWidth}, {"farSideHalfWidth", r.FarSideHalfWidth},
		{"targetHeading", r.TargetReferenceLineHeading}, {"imageHeading", r.ImageReferenceLineHeading},
		{"tieOffset", r.TieModeOffset}, {"descentDistance", r.DescentPointDistance},
	} {
		if !crdaFinite(pair.value) {
			return crdaRegion{}, fmt.Errorf("%s is not finite", pair.name)
		}
	}
	if r.NearSideDistance < 0 || r.RegionLength <= 0 || r.NearSideHalfWidth < 0 || r.FarSideHalfWidth < 0 ||
		r.DescentPointDistance < 0 || r.HeadingTolerance < 0 || r.HeadingTolerance > 180 || r.TieModeOffset < 0 {
		return crdaRegion{}, fmt.Errorf("invalid CRDA runway dimensions or heading tolerance")
	}
	scale := crdaLongitudeScale(r.TargetReferencePoint.Lat)
	if scale < 1e-9 {
		return crdaRegion{}, fmt.Errorf("reference point too near pole")
	}
	outbound := crdaNormalizeHeading(r.TargetReferenceLineHeading + 180)
	near := crdaVector(r.TargetReferencePoint, r.NearSideDistance/60, outbound, scale, magVar)
	far := crdaVector(near, r.RegionLength/60, outbound, scale, magVar)
	return crdaRegion{Points: [4]configPoint{
		crdaVector(near, r.NearSideHalfWidth/60, outbound-90, scale, magVar),
		crdaVector(far, r.FarSideHalfWidth/60, outbound-90, scale, magVar),
		crdaVector(far, r.FarSideHalfWidth/60, outbound+90, scale, magVar),
		crdaVector(near, r.NearSideHalfWidth/60, outbound+90, scale, magVar),
	}}, nil
}

// Match CRC GeoRegion.Contains (ray crossing, half-open vertex convention).
func (r crdaRegion) contains(p configPoint) bool {
	if !crdaValidPoint(p) {
		return false
	}
	inside := false
	j := len(r.Points) - 1
	for i := 0; i < len(r.Points); i++ {
		a, b := r.Points[i], r.Points[j]
		if ((a.Lat <= p.Lat && p.Lat < b.Lat) || (b.Lat <= p.Lat && p.Lat < a.Lat)) &&
			p.Lon < (b.Lon-a.Lon)*(p.Lat-a.Lat)/(b.Lat-a.Lat)+a.Lon {
			inside = !inside
		}
		j = i
	}
	return inside
}

// CRC CrdaAltitudeAndHeadingQualify. Altitude adaptation and track altitude
// are in 100-ft increments, not raw feet. The outer limits are an early reject;
// closer to the reference point CRC also uses an interpolated descent path.
func crdaAltitudeAndHeadingQualify(track crdaTrack, runway rpcRunwayConfig, magVar float64) bool {
	if crdaHeadingDelta(track.CourseTrue+magVar, runway.TargetReferenceLineHeading) > float64(runway.HeadingTolerance) {
		return false
	}
	h := float64(track.AdjustedAltitudeHundreds)
	if h > float64(runway.DescentPointAltitude+runway.AbovePathTolerance) ||
		h < float64(runway.TargetReferencePointAltitude-runway.BelowPathTolerance) {
		return false
	}
	if len(runway.ScratchpadPatterns) > 0 && !crdaMatchPatterns(track.Scratchpad1, runway.ScratchpadPatterns) {
		return false
	}
	if len(runway.SecondaryScratchpadPatterns) > 0 && !crdaMatchPatterns(track.Scratchpad2, runway.SecondaryScratchpadPatterns) {
		return false
	}
	dist := crdaGreatCircleNM(runway.TargetReferencePoint, track.Position)
	if dist >= runway.DescentPointDistance {
		return h >= float64(runway.DescentPointAltitude-runway.BelowPathTolerance)
	}
	// dist < descent distance implies that descent distance is positive.
	ref := float64(runway.TargetReferencePointAltitude) +
		float64(runway.DescentPointAltitude-runway.TargetReferencePointAltitude)*dist/runway.DescentPointDistance
	return h <= ref+float64(runway.AbovePathTolerance) && h >= ref-float64(runway.BelowPathTolerance)
}

func crdaMatchPatterns(value string, patterns []string) bool {
	if value == "" {
		return false
	}
	for _, pattern := range patterns {
		if strings.Contains(strings.ToUpper(value), strings.ToUpper(pattern)) {
			return true
		}
	}
	return false
}

// crdaImagePosition matches CRC's radial rotation. Unlike a nearest-point
// path projection, it preserves the parent's bearing offset from the OUTBOUND
// target course. Tie offset is applied to the radial distance in NM.
func crdaImagePosition(r rpcRunwayConfig, parent configPoint, mode crdaMode, magVar float64) configPoint {
	scale := crdaLongitudeScale(r.TargetReferencePoint.Lat)
	d := crdaDistanceDegrees(r.TargetReferencePoint, parent, scale)
	if mode == crdaTie {
		d += r.TieModeOffset / 60
	}
	sourceOutbound := crdaNormalizeHeading(r.TargetReferenceLineHeading + 180)
	imageOutbound := crdaNormalizeHeading(r.ImageReferenceLineHeading + 180)
	bearing := crdaNormalizeHeading(imageOutbound + crdaBearing(r.TargetReferencePoint, parent, scale, magVar) - sourceOutbound)
	return crdaVector(r.ImageReferencePoint, d, bearing, scale, magVar)
}

// ghostForTrack returns CRC's ONE best qualifying ghost, or false. Within one
// RPC, a qualifying master wins over its slave (CRC's else-if). Across RPCs,
// smallest magnetic course mismatch wins, ties follow original RPC order.
// This is independent of rendering, UI activation, and live track updates.
func (e *crdaEngine) ghostForTrack(track crdaTrack, states []crdaPairState, forceAllQualification bool) (crdaGhost, bool) {
	if e == nil || len(states) != len(e.pairs) || !track.Associated || track.CoastPhase2 ||
		!track.HasAltitude || !track.HasCourse || !track.HasSpeed || !crdaValidPoint(track.Position) ||
		!crdaFinite(track.CourseTrue) || !crdaFinite(track.GroundSpeedKnots) ||
		track.GroundSpeedKnots < 0 || track.GroundSpeedKnots > crdaMaxGroundSpeedKnots {
		return crdaGhost{}, false
	}
	bestDelta := math.Inf(1)
	var best crdaGhost
	found := false
	for i := range e.pairs {
		pair := &e.pairs[i]
		state := states[i]
		if state.Mode != crdaStagger && state.Mode != crdaTie {
			continue
		}
		for side := 0; side < 2; side++ {
			runway := pair.adaptation.MasterRunway
			enabled := state.MasterEnabled
			if side == 1 {
				runway, enabled = pair.adaptation.SlaveRunway, state.SlaveEnabled
			}
			if !(enabled || track.ForceGhost) || !pair.regions[side].contains(track.Position) {
				continue
			}
			if !(track.ForceQualification || forceAllQualification) && !crdaAltitudeAndHeadingQualify(track, runway, e.magVar) {
				continue
			}
			delta := crdaHeadingDelta(track.CourseTrue+e.magVar, runway.TargetReferenceLineHeading)
			if !found || delta < bestDelta {
				bestDelta, found = delta, true
				symbol := pair.adaptation.PositionSymbolStagger
				if state.Mode == crdaTie {
					symbol = pair.adaptation.PositionSymbolTie
				}
				best = crdaGhost{
					Position: crdaImagePosition(runway, track.Position, state.Mode, e.magVar),
					RPCID:    pair.adaptation.ID, RPCIndex: pair.adaptation.Index,
					AirportID: pair.adaptation.AirportID, SourceRunway: runway.RunwayID,
					MasterSource: side == 0, Mode: state.Mode, Symbol: symbol,
				}
			}
			// CRC's else-if skips slave after master qualifies within an RPC.
			break
		}
	}
	return best, found
}
