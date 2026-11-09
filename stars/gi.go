package stars

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	starsATISAPIBaseURL      = "https://atis.info/api"
	starsATISRefreshInterval = time.Minute
	starsATISMissingAfter    = 10 * time.Minute
	starsGITextLength        = 32 // TI 6191.409 Rev. 30, Table 2-15, field F (p. 2-75).
)

var starsATISHTTPClient = &http.Client{Timeout: 10 * time.Second}

type giTextAirport struct {
	display string
	icao    string
}

type giATISReading struct {
	code       string
	approaches map[string]string // normalized runway -> three-letter approach type
	readAt     time.Time
}

type giATISUpdate struct {
	icao    string
	reading giATISReading
	err     error
	done    bool
}

type giTextState struct {
	airports    [10]giTextAirport
	atis        map[string]giATISReading
	ctx         context.Context
	cancel      context.CancelFunc
	updates     chan giATISUpdate
	lastAttempt time.Time
	fetching    bool
	departure   bool
}

// MAIN uses the position's primary airport; auxiliary slots preserve its area's
// SSA-airport order. These assignments stay stable when an API has no report.
func (p *STARSPane) initializeGIText() {
	ctx, cancel := context.WithCancel(context.Background())
	state := giTextState{ctx: ctx, cancel: cancel, atis: make(map[string]giATISReading),
		updates: make(chan giATISUpdate, 11)}
	name := strings.ToUpper(p.config.ControlPosition.Name)
	state.departure = strings.HasSuffix(strings.ToUpper(p.config.ControlPosition.Callsign), "_DEP") ||
		(strings.Contains(name, "DEPARTURE") && !strings.Contains(name, "APPROACH"))
	seen := make(map[string]bool)
	next := 0
	add := func(airport string) {
		if next == len(state.airports) || strings.TrimSpace(airport) == "" {
			return
		}
		display := starsAirportDisplayID(airport)
		icao := resolveSTARSAltimeterAirport(airport)
		if seen[icao] {
			return
		}
		seen[icao] = true
		// Small FAA-only airports can still have APTC data, but the ATIS API
		// requires an ICAO identifier rather than a guessed K-prefixed code.
		if !giICAOIdentifier(icao) {
			icao = ""
		}
		state.airports[next] = giTextAirport{display: display, icao: icao}
		next++
	}
	add(p.config.systemAltimeterAirport())
	for _, airport := range p.config.Area.SSAAirports {
		add(airport)
	}
	p.giText = state
}

func giICAOIdentifier(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, c := range value {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// Rendering only consumes completed requests. A small worker limit avoids
// sending ten simultaneous requests when opening a multi-airport position.
func (p *STARSPane) refreshGIATIS(now time.Time) {
	state := &p.giText
	if state.updates == nil || state.fetching || state.ctx.Err() != nil ||
		(!state.lastAttempt.IsZero() && now.Sub(state.lastAttempt) < starsATISRefreshInterval) {
		return
	}
	state.fetching, state.lastAttempt = true, now
	airports, ctx, updates, departure := state.airports, state.ctx, state.updates, state.departure
	go func() {
		var workers sync.WaitGroup
		limit := make(chan struct{}, 3)
		for _, airport := range airports {
			if airport.icao == "" {
				continue
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				select {
				case limit <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-limit }()
				reading, err := fetchGIATIS(ctx, airport.icao, departure)
				reading.readAt = time.Now()
				update := giATISUpdate{icao: airport.icao, reading: reading, err: err}
				select {
				case updates <- update:
				case <-ctx.Done():
				}
			}()
		}
		workers.Wait()
		select {
		case updates <- giATISUpdate{done: true}:
		case <-ctx.Done():
		}
	}()
}

func (p *STARSPane) consumeGIATISUpdates() {
	for {
		select {
		case update := <-p.giText.updates:
			if update.done {
				p.giText.fetching = false
			} else if update.err == nil {
				p.giText.atis[update.icao] = update.reading
			} else if p.logger != nil && p.giText.ctx.Err() == nil {
				p.logger.Warn("Unable to refresh GI ATIS", slog.String("airport", update.icao), slog.Any("error", update.err))
			}
		default:
			return
		}
	}
}

type giATISMessage struct {
	Airport   string    `json:"airport"`
	Type      string    `json:"type"`
	Code      string    `json:"code"`
	Datis     string    `json:"datis"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func fetchGIATIS(ctx context.Context, icao string, departure bool) (giATISReading, error) {
	base := strings.TrimSpace(os.Getenv("REDS_ATIS_API_BASE_URL"))
	if base == "" {
		base = starsATISAPIBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/"+url.PathEscape(icao), nil)
	if err != nil {
		return giATISReading{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := starsATISHTTPClient.Do(req)
	if err != nil {
		return giATISReading{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent {
		return giATISReading{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return giATISReading{}, fmt.Errorf("ATIS endpoint returned HTTP %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return giATISReading{}, err
	}
	if len(payload) > 1<<20 {
		return giATISReading{}, fmt.Errorf("ATIS response exceeds 1 MiB")
	}
	return parseGIATIS(payload, icao, departure)
}

// The API provides the code directly. Prefer a combined bulletin; otherwise
// use the appropriate arrival/departure bulletin, then the other as a fallback.
func parseGIATIS(payload []byte, icao string, departure bool) (giATISReading, error) {
	var messages []giATISMessage
	if err := json.Unmarshal(payload, &messages); err != nil {
		return giATISReading{}, err
	}
	bestRank := 10
	bestApproachRank := 10
	var reading giATISReading
	var latest time.Time
	var latestApproach time.Time
	var approaches map[string]string
	for _, message := range messages {
		candidate := strings.ToUpper(strings.TrimSpace(message.Code))
		if !strings.EqualFold(message.Airport, icao) || len(candidate) != 1 || candidate[0] < 'A' || candidate[0] > 'Z' {
			continue
		}
		rank := 3
		switch strings.ToLower(strings.TrimSpace(message.Type)) {
		case "combined":
			rank = 0
		case "arrival", "arr", "a":
			rank = 1
			if departure {
				rank = 2
			}
		case "departure", "dep", "d":
			rank = 2
			if departure {
				rank = 1
			}
		}
		if rank < bestRank || (rank == bestRank && message.UpdatedAt.After(latest)) {
			bestRank, latest = rank, message.UpdatedAt
			reading.code = candidate
		}
		// A departure bulletin can supply the selected letter while the
		// arrival bulletin supplies otherwise missing landing approach types.
		approachRank := rank
		if departure && (rank == 1 || rank == 2) {
			approachRank = 3 - rank
		}
		if hints := giATISApproaches(message.Datis); len(hints) != 0 &&
			(approachRank < bestApproachRank || (approachRank == bestApproachRank && message.UpdatedAt.After(latestApproach))) {
			bestApproachRank, latestApproach, approaches = approachRank, message.UpdatedAt, hints
		}
	}
	reading.approaches = approaches
	return reading, nil
}

func (p *STARSPane) giTextLines(now time.Time) (lines [10]string) {
	for i, airport := range p.giText.airports {
		if airport.display == "" {
			continue
		}
		var atis giATISReading
		if reading, ok := p.giText.atis[airport.icao]; ok && now.Sub(reading.readAt) < starsATISMissingAfter {
			atis = reading
		}
		configuration, ok := p.aptc.Configuration(airport.display)
		if !ok && airport.icao != "" {
			configuration, _ = p.aptc.Configuration(airport.icao)
		}
		lines[i] = formatGITextLine(airport.display, atis.code, configuration.ArrivalRunways, configuration.DepartureRunways, i == 0, atis.approaches)
	}
	return
}

func formatGITextLine(airport, code, arrivals, departures string, main bool, approaches map[string]string) string {
	var fields []string
	if arrivals = formatGIArrivals(arrivals, approaches); arrivals != "" {
		fields = append(fields, arrivals)
	}
	if departures = formatGIRunways(giRunways(normalizeGIConfiguration(departures))); departures != "" {
		fields = append(fields, departures)
	}
	if code == "" && len(fields) == 0 {
		return ""
	}
	text := code
	if !main {
		text = airport + " :" + code
	}
	if len(fields) != 0 {
		if text != "" {
			text += " "
		}
		text += strings.Join(fields, " ")
	}
	if runes := []rune(text); len(runes) > starsGITextLength {
		text = string(runes[:starsGITextLength-1]) + "+"
	}
	return text
}

var (
	giApproachNames = map[string]string{
		"ILS": "ILS", "VAP": "VAP", "VIS": "VAP", "VISUAL": "VAP",
		"RNV": "RNV", "RNAV": "RNV", "RNP": "RNP", "GPS": "GPS", "GLS": "GLS",
		"LOC": "LOC", "LOCALIZER": "LOC", "LDA": "LDA", "VOR": "VOR", "DME": "DME",
		"NDB": "NDB", "SDF": "SDF", "TAC": "TAC", "TACAN": "TAC", "PAR": "PAR", "ASR": "ASR",
	}
	giApproachPattern     = regexp.MustCompile(`\b(?:ILS|VAP|VIS|VISUAL|RNV|RNAV|RNP|GPS|GLS|LOC|LOCALIZER|LDA|VOR|DME|NDB|SDF|TAC|TACAN|PAR|ASR)\b`)
	giAttachedRunway      = regexp.MustCompile(`([A-Z])([0-9])`)
	giRNAVSensor          = regexp.MustCompile(`\bRNV\s*\((?:GPS|RNP)\)`)
	giRunwayPattern       = regexp.MustCompile(`\b([0-9]{1,2})([LCR]?)\b|\b([LCR])\b`)
	giDepartureAnnotation = regexp.MustCompile(`\b(?:D:|DEP(?:T|G|ARTING|ARTURES?)?\b)`)
	giRunwayQualifier     = regexp.MustCompile(`\b(?:OFFSET|INTRAIL)\b`)
	giATISNotams          = regexp.MustCompile(`\bNOTAMS?\b`)
	giATISOperational     = regexp.MustCompile(`\b(?:APPROACH(?:ES)?|APCHS?|APP|IN USE|LANDING|LND)\b`)
	giATISUnavailable     = regexp.MustCompile(`\b(?:OTS|OUT OF SERVICE|OUT OF SVC|UNAVAILABLE|NOT AVAILABLE|NOT AVBL|NOT IN USE|DO NOT USE|NOT AUTHORIZED|CLOSED|CLSD)\b`)
)

func normalizeGIConfiguration(text string) string {
	text = giAttachedRunway.ReplaceAllString(strings.ToUpper(text), "$1 $2")
	text = giApproachPattern.ReplaceAllStringFunc(text, func(name string) string { return giApproachNames[name] })
	text = giRNAVSensor.ReplaceAllString(text, "RNV ")
	return strings.Join(strings.Fields(text), " ")
}

type giRunway struct {
	number string
	suffix string
}

func (r giRunway) key() string { return strings.TrimLeft(r.number, "0") + r.suffix }

// Also expand already compacted lists such as 22L/R. A lone L/R/C is only a
// suffix continuation when separated from the preceding runway by a slash.
func giRunways(text string) []giRunway {
	var runways []giRunway
	lastNumber, previousEnd := "", 0
	for _, match := range giRunwayPattern.FindAllStringSubmatchIndex(text, -1) {
		if match[2] >= 0 {
			number := text[match[2]:match[3]]
			n, _ := strconv.Atoi(number)
			if n < 1 || n > 36 {
				lastNumber = ""
				continue
			}
			runways = append(runways, giRunway{number, text[match[4]:match[5]]})
			lastNumber = number
		} else if lastNumber != "" && strings.TrimSpace(text[previousEnd:match[0]]) == "/" {
			runways = append(runways, giRunway{lastNumber, text[match[6]:match[7]]})
		}
		previousEnd = match[1]
	}
	return runways
}

func formatGIRunways(runways []giRunway) string {
	type runwayGroup struct{ number, suffixes string }
	var groups []runwayGroup
	indices := make(map[string]int)
	for _, runway := range runways {
		key := strings.TrimLeft(runway.number, "0")
		if runway.suffix == "" {
			key += "-unsuffixed"
		}
		index, ok := indices[key]
		if !ok {
			index = len(groups)
			indices[key] = index
			groups = append(groups, runwayGroup{number: runway.number})
		}
		if !strings.Contains(groups[index].suffixes, runway.suffix) {
			groups[index].suffixes += runway.suffix
		}
	}
	var fields []string
	for _, group := range groups {
		var suffixes []string
		for _, suffix := range []string{"L", "C", "R"} {
			if strings.Contains(group.suffixes, suffix) {
				suffixes = append(suffixes, suffix)
			}
		}
		fields = append(fields, group.number+strings.Join(suffixes, "/"))
	}
	return strings.Join(fields, "/")
}

type giApproachGroup struct {
	approach string
	runways  []giRunway
}

func giApproachGroups(text string) []giApproachGroup {
	if match := giDepartureAnnotation.FindStringIndex(text); match != nil {
		text = text[:match[0]]
	}
	if match := giRunwayQualifier.FindStringIndex(text); match != nil {
		text = text[:match[0]]
	}
	matches := giApproachPattern.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return []giApproachGroup{{runways: giRunways(text)}}
	}
	groups := []giApproachGroup{{runways: giRunways(text[:matches[0][0]])}}
	pending := ""
	for i, match := range matches {
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		approach := text[match[0]:match[1]]
		if pending != "" {
			approach = pending + "/" + approach
		}
		runways := giRunways(text[match[1]:end])
		if len(runways) == 0 && i+1 < len(matches) {
			pending = approach
			continue
		}
		groups = append(groups, giApproachGroup{approach, runways})
		pending = ""
	}
	return groups
}

func formatGIArrivals(text string, inferred map[string]string) string {
	var groups []giApproachGroup
	indices := make(map[string]int)
	for _, group := range giApproachGroups(normalizeGIConfiguration(text)) {
		for _, runway := range group.runways {
			approach := group.approach
			if approach == "" {
				approach = inferred[runway.key()]
				if approach == "" {
					approach = inferred["*"]
				}
			}
			index, ok := indices[approach]
			if !ok {
				index = len(groups)
				indices[approach] = index
				groups = append(groups, giApproachGroup{approach: approach})
			}
			groups[index].runways = append(groups[index].runways, runway)
		}
	}
	var fields []string
	for _, group := range groups {
		text := formatGIRunways(group.runways)
		if group.approach != "" {
			text = group.approach + " " + text
		}
		fields = append(fields, text)
	}
	return strings.Join(fields, " ")
}

// Infer only operational approach statements, before NOTAM text. Outage and
// closure clauses do not establish which approach is in use. Explicit APTC
// approach labels take precedence over these runway-specific ATIS hints.
func giATISApproaches(text string) map[string]string {
	text = normalizeGIConfiguration(text)
	if match := giATISNotams.FindStringIndex(text); match != nil {
		text = text[:match[0]]
	}
	approaches := make(map[string]string)
	ambiguousDefault := false
	for _, clause := range strings.FieldsFunc(text, func(c rune) bool { return c == '.' || c == ';' }) {
		if !giATISOperational.MatchString(clause) || giATISUnavailable.MatchString(clause) {
			continue
		}
		for _, group := range giApproachGroups(clause) {
			if group.approach == "" {
				continue
			}
			if len(group.runways) == 0 {
				if strings.Contains(clause, "IN USE") && !strings.Contains(group.approach, "/") && !ambiguousDefault {
					if previous := approaches["*"]; previous != "" && previous != group.approach {
						delete(approaches, "*")
						ambiguousDefault = true
					} else {
						approaches["*"] = group.approach
					}
				}
				continue
			}
			for _, runway := range group.runways {
				approaches[runway.key()] = group.approach
			}
		}
	}
	return approaches
}
