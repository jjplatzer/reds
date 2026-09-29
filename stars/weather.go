package stars

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juliusplatzer/reds/util"
)

const (
	starsAirportDatabaseResource = "resources/nav/us-airports.csv"
	starsAWCMETAREndpoint        = "https://aviationweather.gov/api/data/metar"
	starsMETARRefreshInterval    = time.Minute
	starsMETARHTTPTimeout        = 10 * time.Second
)

var (
	starsMETARHTTPClient = &http.Client{Timeout: starsMETARHTTPTimeout}
	starsAirportDBOnce   sync.Once
	starsAirportDBValue  starsAirportDatabase
	starsAirportDBErr    error
)

type starsAirportDatabase struct {
	byIATA        map[string]string
	byICAO        map[string]string
	displayByICAO map[string]string
	positionByID  map[string]configPoint
}

type starsMETAR struct {
	ICAO         string
	Observation  time.Time
	Altimeter    int // hundredths inHg, e.g. 2992
	HasAltimeter bool
}

type starsMETARUpdate struct {
	METAR starsMETAR
	Err   error
}

type starsMETARBatchUpdate struct {
	METARs map[string]starsMETAR
	Err    error
}

type ssaAirportWeatherStation struct {
	display string
	icao    string
}

type ssaAirportWeatherState struct {
	stations    []ssaAirportWeatherStation
	metars      map[string]starsMETAR
	updates     chan starsMETARBatchUpdate
	fetching    bool
	lastAttempt time.Time
}

type starsAWCMETAR struct {
	ICAOID  string   `json:"icaoId"`
	RawOb   string   `json:"rawOb"`
	ObsTime int64    `json:"obsTime"`
	Altim   *float64 `json:"altim"`
}

type systemAltimeterState struct {
	airport     string
	icao        string
	metar       starsMETAR
	hasMETAR    bool
	updates     chan starsMETARUpdate
	fetching    bool
	lastAttempt time.Time
}

func adaptedAirportCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return ""
	}
	if db, err := loadSTARSAirportDatabase(); err == nil {
		if len(code) == 3 {
			if _, ok := db.byIATA[code]; ok {
				return code
			}
		}
		if len(code) == 4 {
			if _, ok := db.byICAO[code]; ok {
				return code
			}
		}
	}
	return ""
}

func loadSTARSAirportDatabase() (starsAirportDatabase, error) {
	starsAirportDBOnce.Do(func() {
		starsAirportDBValue, starsAirportDBErr = parseSTARSAirportDatabase(util.LoadResourceBytes(starsAirportDatabaseResource))
	})
	return starsAirportDBValue, starsAirportDBErr
}

func parseSTARSAirportDatabase(data []byte) (starsAirportDatabase, error) {
	db := starsAirportDatabase{
		byIATA:        make(map[string]string),
		byICAO:        make(map[string]string),
		displayByICAO: make(map[string]string),
		positionByID:  make(map[string]configPoint),
	}

	r := csv.NewReader(bytes.NewReader(data))
	header, err := r.Read()
	if err != nil {
		return starsAirportDatabase{}, fmt.Errorf("read airport CSV header: %w", err)
	}
	columns := make(map[string]int, len(header))
	for i, name := range header {
		columns[strings.TrimSpace(name)] = i
	}
	iataColumn, okIATA := columns["iata_code"]
	icaoColumn, okICAO := columns["icao_code"]
	latColumn, okLat := columns["latitude_deg"]
	lonColumn, okLon := columns["longitude_deg"]
	if !okIATA || !okICAO || !okLat || !okLon {
		return starsAirportDatabase{}, fmt.Errorf("airport CSV must contain iata_code, icao_code, latitude_deg and longitude_deg columns")
	}

	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return starsAirportDatabase{}, fmt.Errorf("read airport CSV: %w", err)
		}
		if iataColumn >= len(record) || icaoColumn >= len(record) || latColumn >= len(record) || lonColumn >= len(record) {
			continue
		}
		iata := strings.ToUpper(strings.TrimSpace(record[iataColumn]))
		icao := strings.ToUpper(strings.TrimSpace(record[icaoColumn]))
		if icao == "" {
			continue
		}
		db.byICAO[icao] = icao
		lat, latErr := strconv.ParseFloat(strings.TrimSpace(record[latColumn]), 64)
		lon, lonErr := strconv.ParseFloat(strings.TrimSpace(record[lonColumn]), 64)
		if latErr == nil && lonErr == nil {
			point := configPoint{Lat: lat, Lon: lon}
			db.positionByID[icao] = point
			if iata != "" {
				db.positionByID[iata] = point
			}
		}
		if iata != "" {
			db.byIATA[iata] = icao
			db.displayByICAO[icao] = iata
		}
	}
	return db, nil
}

func starsAirportDisplayID(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 4 {
		return code
	}
	if db, err := loadSTARSAirportDatabase(); err == nil {
		if display, ok := db.displayByICAO[code]; ok {
			return display
		}
	}
	return code[1:]
}

func resolveSTARSAltimeterAirport(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if db, err := loadSTARSAirportDatabase(); err == nil {
		if len(code) == 3 {
			if icao, ok := db.byIATA[code]; ok {
				return icao
			}
		}
		if len(code) == 4 {
			if icao, ok := db.byICAO[code]; ok {
				return icao
			}
		}
	}
	return code
}

func (p *STARSPane) initializeSystemAltimeter(airport string) {
	if p == nil {
		return
	}
	airport = strings.ToUpper(strings.TrimSpace(airport))
	p.systemAltimeter = systemAltimeterState{
		airport: airport,
		icao:    resolveSTARSAltimeterAirport(airport),
		updates: make(chan starsMETARUpdate, 1),
	}
}

// initializeSSAAirportWeather selects the adapted airports used by field N of
// the System Status Area. TI 6191.409 Table 2-15 permits up to six airports
// when a single pressure unit is displayed. Preserve CRC's adaptation order,
// de-duplicate by resolved ICAO station, and keep the three-character display
// identifier separate from the station identifier used by AviationWeather.
func (p *STARSPane) initializeSSAAirportWeather(airports []string) {
	if p == nil {
		return
	}

	state := ssaAirportWeatherState{
		metars:  make(map[string]starsMETAR),
		updates: make(chan starsMETARBatchUpdate, 1),
	}
	seen := make(map[string]struct{})
	for _, airport := range airports {
		if len(state.stations) == 6 {
			break
		}
		display := starsAirportDisplayID(airport)
		icao := strings.ToUpper(strings.TrimSpace(resolveSTARSAltimeterAirport(airport)))
		if display == "" || icao == "" {
			continue
		}
		if _, ok := seen[icao]; ok {
			continue
		}
		seen[icao] = struct{}{}
		state.stations = append(state.stations, ssaAirportWeatherStation{
			display: display,
			icao:    icao,
		})
	}
	p.ssaAirportWeather = state
}

func (p *STARSPane) refreshSystemAltimeter() {
	if p == nil || p.systemAltimeter.icao == "" || p.systemAltimeter.updates == nil || p.systemAltimeter.fetching {
		return
	}
	now := time.Now()
	if !p.systemAltimeter.lastAttempt.IsZero() && now.Sub(p.systemAltimeter.lastAttempt) < starsMETARRefreshInterval {
		return
	}

	p.systemAltimeter.lastAttempt = now
	p.systemAltimeter.fetching = true
	icao := p.systemAltimeter.icao
	updates := p.systemAltimeter.updates
	go func() {
		metar, err := fetchSTARSAWCMETAR(context.Background(), icao)
		select {
		case updates <- starsMETARUpdate{METAR: metar, Err: err}:
		default:
		}
	}()
}

func (p *STARSPane) consumeSystemAltimeterUpdates() {
	if p == nil || p.systemAltimeter.updates == nil {
		return
	}
	for {
		select {
		case update := <-p.systemAltimeter.updates:
			p.systemAltimeter.fetching = false
			if update.Err != nil {
				if p.logger != nil {
					p.logger.Warn(
						"STARS system altimeter METAR request failed",
						slog.String("airport", p.systemAltimeter.airport),
						slog.String("icao", p.systemAltimeter.icao),
						slog.Any("error", update.Err),
					)
				}
				continue
			}
			p.systemAltimeter.metar = update.METAR
			p.systemAltimeter.hasMETAR = true
		default:
			return
		}
	}
}

func (p *STARSPane) refreshSSAAirportWeather() {
	if p == nil || len(p.ssaAirportWeather.stations) == 0 || p.ssaAirportWeather.updates == nil || p.ssaAirportWeather.fetching {
		return
	}
	now := time.Now()
	if !p.ssaAirportWeather.lastAttempt.IsZero() && now.Sub(p.ssaAirportWeather.lastAttempt) < starsMETARRefreshInterval {
		return
	}

	p.ssaAirportWeather.lastAttempt = now
	p.ssaAirportWeather.fetching = true
	icaos := make([]string, 0, len(p.ssaAirportWeather.stations))
	for _, station := range p.ssaAirportWeather.stations {
		icaos = append(icaos, station.icao)
	}
	updates := p.ssaAirportWeather.updates
	go func() {
		metars, err := fetchSTARSAWCMETARs(context.Background(), icaos)
		select {
		case updates <- starsMETARBatchUpdate{METARs: metars, Err: err}:
		default:
		}
	}()
}

func (p *STARSPane) consumeSSAAirportWeatherUpdates() {
	if p == nil || p.ssaAirportWeather.updates == nil {
		return
	}
	for {
		select {
		case update := <-p.ssaAirportWeather.updates:
			p.ssaAirportWeather.fetching = false
			if update.Err != nil {
				if p.logger != nil {
					p.logger.Warn(
						"STARS SSA airport METAR request failed",
						slog.Any("error", update.Err),
					)
				}
				continue
			}
			// A successful batch replaces the prior snapshot. This avoids
			// continuing to label an airport "A" if the current response no
			// longer supplies an altimeter for it.
			p.ssaAirportWeather.metars = update.METARs
		default:
			return
		}
	}
}

func formatSSAAirportWeatherLines(stations []ssaAirportWeatherStation, metars map[string]starsMETAR) []string {
	entries := make([]string, 0, min(len(stations), 6))
	for _, station := range stations[:min(len(stations), 6)] {
		metar, ok := metars[station.icao]
		if !ok || !metar.HasAltimeter {
			continue
		}
		entries = append(entries, fmt.Sprintf(
			"%s %02d.%02dA",
			station.display,
			metar.Altimeter/100,
			metar.Altimeter%100,
		))
	}

	lines := make([]string, 0, 2)
	for len(entries) > 0 {
		n := min(3, len(entries))
		lines = append(lines, strings.Join(entries[:n], " "))
		entries = entries[n:]
	}
	return lines
}

func (p *STARSPane) ssaAirportWeatherLines() []string {
	if p == nil {
		return nil
	}
	return formatSSAAirportWeatherLines(p.ssaAirportWeather.stations, p.ssaAirportWeather.metars)
}

func (p *STARSPane) ssaFieldEText(now time.Time, showTime, showAltimeter bool) string {
	var parts []string
	if showTime {
		parts = append(parts, now.UTC().Format("1504/05"))
	}
	if showAltimeter && p != nil && p.systemAltimeter.hasMETAR {
		metar := p.systemAltimeter.metar
		if metar.HasAltimeter {
			parts = append(parts, fmt.Sprintf("%02d.%02d", metar.Altimeter/100, metar.Altimeter%100))
		}
	}
	return strings.Join(parts, " ")
}

func fetchSTARSAWCMETAR(ctx context.Context, icao string) (starsMETAR, error) {
	icao = strings.ToUpper(strings.TrimSpace(icao))
	metars, err := fetchSTARSAWCMETARs(ctx, []string{icao})
	if err != nil {
		return starsMETAR{}, err
	}
	metar, ok := metars[icao]
	if !ok {
		return starsMETAR{}, fmt.Errorf("no METAR found for %s", icao)
	}
	return metar, nil
}

func fetchSTARSAWCMETARs(ctx context.Context, icaos []string) (map[string]starsMETAR, error) {
	ids := make([]string, 0, len(icaos))
	seen := make(map[string]struct{}, len(icaos))
	for _, icao := range icaos {
		icao = strings.ToUpper(strings.TrimSpace(icao))
		if icao == "" {
			continue
		}
		if _, ok := seen[icao]; ok {
			continue
		}
		seen[icao] = struct{}{}
		ids = append(ids, icao)
	}
	if len(ids) == 0 {
		return map[string]starsMETAR{}, nil
	}

	q := url.Values{}
	q.Set("ids", strings.Join(ids, ","))
	q.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, starsAWCMETAREndpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "REDS STARS METAR")

	resp, err := starsMETARHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("aviationweather METAR: HTTP %s", resp.Status)
	}

	var reports []starsAWCMETAR
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reports); err != nil {
		return nil, fmt.Errorf("decode aviationweather METAR: %w", err)
	}
	if len(reports) == 0 {
		return nil, fmt.Errorf("no METAR found for %s", strings.Join(ids, ","))
	}

	metars := make(map[string]starsMETAR, len(reports))
	for _, report := range reports {
		if strings.TrimSpace(report.RawOb) == "" {
			continue
		}
		icao := strings.ToUpper(strings.TrimSpace(report.ICAOID))
		if icao == "" {
			continue
		}
		observation := time.Unix(report.ObsTime, 0).UTC()
		if report.ObsTime == 0 {
			observation = time.Time{}
		}
		altimeter, hasAltimeter := parseSTARSAltimeter(report.RawOb, report.Altim)
		metars[icao] = starsMETAR{
			ICAO:         icao,
			Observation:  observation,
			Altimeter:    altimeter,
			HasAltimeter: hasAltimeter,
		}
	}
	if len(metars) == 0 {
		return nil, fmt.Errorf("no usable METAR found for %s", strings.Join(ids, ","))
	}
	return metars, nil
}

func parseSTARSAltimeter(raw string, altimHPA *float64) (int, bool) {
	for _, field := range strings.Fields(strings.ToUpper(raw)) {
		if len(field) != 5 || field[0] != 'A' {
			continue
		}
		value := 0
		valid := true
		for _, r := range field[1:] {
			if r < '0' || r > '9' {
				valid = false
				break
			}
			value = value*10 + int(r-'0')
		}
		if valid {
			return value, true
		}
	}
	if altimHPA != nil && *altimHPA > 0 {
		return int(*altimHPA*2.95299830714 + 0.5), true
	}
	return 0, false
}
