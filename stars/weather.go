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
	"path/filepath"
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
	byIATA map[string]string
	byICAO map[string]string
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

type starsSSAConfig struct {
	Areas            []starsSSAArea            `json:"areas"`
	ControlPositions []starsSSAControlPosition `json:"controlPositions"`
}

type starsSSAArea struct {
	ID          string   `json:"id"`
	SSAAirports []string `json:"ssaAirports"`
}

type starsSSAControlPosition struct {
	ID               string `json:"id"`
	AreaID           string `json:"areaId"`
	PhysicalFacility string `json:"physicalFacility"`
	Callsign         string `json:"callsign"`
}

// adaptedSystemAltimeterAirport resolves the best available system-altimeter
// station from the generated CRC position adaptation. The current REDS config
// does not yet carry CRC's explicit system-altimeter field, so prefer a real
// airport physical facility, then the airport prefix in the position callsign,
// and finally the area's adapted SSA-airport list.
func adaptedSystemAltimeterAirport(artcc, tracon, positionID string) (string, error) {
	artcc, err := normalizedSTARSResourceCode("ARTCC", artcc)
	if err != nil {
		return "", err
	}
	tracon, err = normalizedSTARSResourceCode("TRACON", tracon)
	if err != nil {
		return "", err
	}
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return "", fmt.Errorf("STARS: empty position ID")
	}

	path := filepath.ToSlash(filepath.Join("resources", "configs", "stars", artcc, tracon+".json"))
	if !util.ResourceExists(path) {
		return "", fmt.Errorf("STARS: facility config %s not found", path)
	}

	var cfg starsSSAConfig
	if err := json.Unmarshal(util.LoadResourceBytes(path), &cfg); err != nil {
		return "", fmt.Errorf("STARS: decode %s: %w", path, err)
	}

	var selected starsSSAControlPosition
	found := false
	for _, position := range cfg.ControlPositions {
		if position.ID == positionID {
			selected = position
			found = true
			break
		}
	}
	if !found || selected.AreaID == "" {
		return "", fmt.Errorf("STARS: position %q has no adapted area in %s", positionID, path)
	}

	if airport := adaptedAirportCode(selected.PhysicalFacility); airport != "" {
		return airport, nil
	}
	if prefix, _, ok := strings.Cut(strings.TrimSpace(selected.Callsign), "_"); ok {
		if airport := adaptedAirportCode(prefix); airport != "" {
			return airport, nil
		}
	}

	for _, area := range cfg.Areas {
		if area.ID != selected.AreaID {
			continue
		}
		for _, airport := range area.SSAAirports {
			if airport := adaptedAirportCode(airport); airport != "" {
				return airport, nil
			}
		}
		return "", nil
	}
	return "", fmt.Errorf("STARS: position %q references unknown area %q", positionID, selected.AreaID)
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

func normalizedSTARSResourceCode(kind, code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return "", fmt.Errorf("STARS: empty %s", kind)
	}
	if strings.ContainsAny(code, `/\\`) || code == "." || code == ".." {
		return "", fmt.Errorf("STARS: invalid %s %q", kind, code)
	}
	return code, nil
}

func loadSTARSAirportDatabase() (starsAirportDatabase, error) {
	starsAirportDBOnce.Do(func() {
		starsAirportDBValue, starsAirportDBErr = parseSTARSAirportDatabase(util.LoadResourceBytes(starsAirportDatabaseResource))
	})
	return starsAirportDBValue, starsAirportDBErr
}

func parseSTARSAirportDatabase(data []byte) (starsAirportDatabase, error) {
	db := starsAirportDatabase{
		byIATA: make(map[string]string),
		byICAO: make(map[string]string),
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
	if !okIATA || !okICAO {
		return starsAirportDatabase{}, fmt.Errorf("airport CSV must contain iata_code and icao_code columns")
	}

	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return starsAirportDatabase{}, fmt.Errorf("read airport CSV: %w", err)
		}
		if iataColumn >= len(record) || icaoColumn >= len(record) {
			continue
		}
		iata := strings.ToUpper(strings.TrimSpace(record[iataColumn]))
		icao := strings.ToUpper(strings.TrimSpace(record[icaoColumn]))
		if icao == "" {
			continue
		}
		db.byICAO[icao] = icao
		if iata != "" {
			db.byIATA[iata] = icao
		}
	}
	return db, nil
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

func (p *STARSPane) ssaFieldEText(now time.Time) string {
	text := now.UTC().Format("1504/05")
	if p == nil || !p.systemAltimeter.hasMETAR {
		return text
	}
	metar := p.systemAltimeter.metar
	if !metar.HasAltimeter {
		return text
	}
	return fmt.Sprintf("%s %02d.%02d", text, metar.Altimeter/100, metar.Altimeter%100)
}

func fetchSTARSAWCMETAR(ctx context.Context, icao string) (starsMETAR, error) {
	q := url.Values{}
	q.Set("ids", icao)
	q.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, starsAWCMETAREndpoint+"?"+q.Encode(), nil)
	if err != nil {
		return starsMETAR{}, err
	}
	req.Header.Set("User-Agent", "REDS STARS METAR")

	resp, err := starsMETARHTTPClient.Do(req)
	if err != nil {
		return starsMETAR{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return starsMETAR{}, fmt.Errorf("aviationweather METAR: HTTP %s", resp.Status)
	}

	var reports []starsAWCMETAR
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reports); err != nil {
		return starsMETAR{}, fmt.Errorf("decode aviationweather METAR: %w", err)
	}
	if len(reports) == 0 || strings.TrimSpace(reports[0].RawOb) == "" {
		return starsMETAR{}, fmt.Errorf("no METAR found for %s", icao)
	}

	report := reports[0]
	observation := time.Unix(report.ObsTime, 0).UTC()
	if report.ObsTime == 0 {
		observation = time.Time{}
	}
	altimeter, hasAltimeter := parseSTARSAltimeter(report.RawOb, report.Altim)
	return starsMETAR{
		ICAO:         strings.ToUpper(strings.TrimSpace(report.ICAOID)),
		Observation:  observation,
		Altimeter:    altimeter,
		HasAltimeter: hasAltimeter,
	}, nil
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
