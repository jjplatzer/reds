package eram

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
	"strings"
	"sync"
	"time"

	"github.com/juliusplatzer/reds/util"
)

const (
	airportDatabaseResource = "resources/nav/us-airports.csv"
	awcMETAREndpoint        = "https://aviationweather.gov/api/data/metar"
	wxReportRefreshInterval = time.Minute
	wxReportHTTPTimeout     = 10 * time.Second
)

var (
	wxReportHTTPClient = &http.Client{Timeout: wxReportHTTPTimeout}
	airportDBOnce      sync.Once
	airportDBValue     airportDatabase
	airportDBErr       error
)

type airportDatabase struct {
	byIATA map[string]string
	byICAO map[string]string
	toIATA map[string]string
}

type wxMETAR struct {
	ICAO        string
	Raw         string
	Observation time.Time
	FetchedAt   time.Time
}

type wxMETARUpdate struct {
	ICAO  string
	METAR wxMETAR
	Err   error
}

type awcMETAR struct {
	ICAOID  string `json:"icaoId"`
	RawOb   string `json:"rawOb"`
	ObsTime int64  `json:"obsTime"`
}

func loadAirportDatabase() (airportDatabase, error) {
	airportDBOnce.Do(func() {
		airportDBValue, airportDBErr = parseAirportDatabase(util.LoadResourceBytes(airportDatabaseResource))
	})
	return airportDBValue, airportDBErr
}

func parseAirportDatabase(data []byte) (airportDatabase, error) {
	db := airportDatabase{
		byIATA: make(map[string]string),
		byICAO: make(map[string]string),
		toIATA: make(map[string]string),
	}

	r := csv.NewReader(bytes.NewReader(data))
	header, err := r.Read()
	if err != nil {
		return airportDatabase{}, fmt.Errorf("read airport CSV header: %w", err)
	}
	columns := make(map[string]int, len(header))
	for i, name := range header {
		columns[strings.TrimSpace(name)] = i
	}
	iataColumn, okIATA := columns["iata_code"]
	icaoColumn, okICAO := columns["icao_code"]
	if !okIATA || !okICAO {
		return airportDatabase{}, fmt.Errorf("airport CSV must contain iata_code and icao_code columns")
	}

	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return airportDatabase{}, fmt.Errorf("read airport CSV: %w", err)
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
			db.toIATA[icao] = iata
		}
	}
	return db, nil
}

func resolveWXAirport(code string) (icao, displayID string, ok bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	db, err := loadAirportDatabase()
	if err != nil {
		return "", "", false
	}

	switch len(code) {
	case 3:
		icao, ok = db.byIATA[code]
	case 4:
		icao, ok = db.byICAO[code]
	}
	if !ok {
		return "", "", false
	}
	displayID = db.toIATA[icao]
	if displayID == "" {
		displayID = icao
	}
	return icao, displayID, true
}

func (p *ERAMPane) executeMCACommand() bool {
	if p == nil {
		return false
	}
	text := strings.TrimSpace(string(p.mca.input))
	if text == "" {
		return false
	}
	tokens := strings.Fields(strings.ToUpper(text))
	if len(tokens) == 0 || tokens[0] != "WR" {
		return false
	}

	// CRC clears Preview and Feedback before processing a submitted message.
	p.mca.input = p.mca.input[:0]
	p.mca.cursor = 0
	p.setMCAFeedback(false)

	if len(tokens) < 2 {
		p.setMCAFeedback(true, "MESSAGE TOO SHORT")
		return true
	}
	if len(tokens) > 2 {
		p.setMCAFeedback(true, "MESSAGE TOO LONG")
		return true
	}
	airport := tokens[1]
	if len(airport) != 3 && len(airport) != 4 {
		p.setMCAFeedback(true, airport+" FORMAT")
		return true
	}

	icao, displayID, ok := resolveWXAirport(airport)
	if !ok {
		// CRC uses NOT ADAPTED when a weather-station request cannot resolve to
		// usable METAR data. Here the local airport adaptation is the authority.
		p.setMCAFeedback(true, "NOT ADAPTED")
		return true
	}
	p.toggleWXStation(icao, displayID)
	p.setMCAFeedback(false, "ACCEPT", "WEATHER STAT REQ")
	return true
}

func (p *ERAMPane) toggleWXStation(icao, displayID string) {
	if p == nil {
		return
	}
	for i, station := range p.wxReport.stations {
		if station.ICAO != icao {
			continue
		}
		p.wxReport.stations = append(p.wxReport.stations[:i], p.wxReport.stations[i+1:]...)
		delete(p.wxReport.fetching, icao)
		delete(p.wxReport.lastAttempt, icao)
		p.clampWXTopLine()
		return
	}

	p.wxReport.stations = append([]wxReportStation{{ICAO: icao, DisplayID: displayID}}, p.wxReport.stations...)
	p.wxReport.prefs.visible = true
	p.wxReport.topLine = 0
	p.requestWXMETAR(icao)
}

func (p *ERAMPane) requestWXMETAR(icao string) {
	if p == nil || icao == "" || p.wxReport.updates == nil || p.wxReport.fetching[icao] {
		return
	}
	now := time.Now()
	if last := p.wxReport.lastAttempt[icao]; !last.IsZero() && now.Sub(last) < wxReportRefreshInterval {
		return
	}
	p.wxReport.lastAttempt[icao] = now
	p.wxReport.fetching[icao] = true
	updates := p.wxReport.updates
	go func() {
		metar, err := fetchAWCMETAR(context.Background(), icao)
		update := wxMETARUpdate{ICAO: icao, METAR: metar, Err: err}
		select {
		case updates <- update:
		default:
		}
	}()
}

func fetchAWCMETAR(ctx context.Context, icao string) (wxMETAR, error) {
	q := url.Values{}
	q.Set("ids", icao)
	q.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, awcMETAREndpoint+"?"+q.Encode(), nil)
	if err != nil {
		return wxMETAR{}, err
	}
	req.Header.Set("User-Agent", "REDS WX Report")

	resp, err := wxReportHTTPClient.Do(req)
	if err != nil {
		return wxMETAR{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return wxMETAR{}, fmt.Errorf("aviationweather METAR: HTTP %s", resp.Status)
	}

	var reports []awcMETAR
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reports); err != nil {
		return wxMETAR{}, fmt.Errorf("decode aviationweather METAR: %w", err)
	}
	if len(reports) == 0 || strings.TrimSpace(reports[0].RawOb) == "" {
		return wxMETAR{}, fmt.Errorf("no METAR found for %s", icao)
	}
	report := reports[0]
	obs := time.Unix(report.ObsTime, 0).UTC()
	if report.ObsTime == 0 {
		obs = time.Time{}
	}
	return wxMETAR{
		ICAO:        strings.ToUpper(strings.TrimSpace(report.ICAOID)),
		Raw:         strings.TrimSpace(report.RawOb),
		Observation: obs,
		FetchedAt:   time.Now(),
	}, nil
}

func (p *ERAMPane) consumeWXMETARUpdates() {
	if p == nil || p.wxReport.updates == nil {
		return
	}
	for {
		select {
		case update := <-p.wxReport.updates:
			delete(p.wxReport.fetching, update.ICAO)
			if update.Err != nil {
				if p.logger != nil {
					p.logger.Warn("ERAM WX REPORT METAR request failed", slog.String("icao", update.ICAO), slog.Any("error", update.Err))
				}
				continue
			}
			p.wxReport.metars[update.ICAO] = update.METAR
		default:
			return
		}
	}
}

func (p *ERAMPane) refreshWXMETARs() {
	if p == nil {
		return
	}
	now := time.Now()
	for _, station := range p.wxReport.stations {
		if last := p.wxReport.lastAttempt[station.ICAO]; !last.IsZero() && now.Sub(last) < wxReportRefreshInterval {
			continue
		}
		p.requestWXMETAR(station.ICAO)
	}
}
