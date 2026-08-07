package wx

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	redslog "github.com/juliusplatzer/reds/log"
)

type Domain string

const (
	DomainCONUS  Domain = "CONUS"
	DomainAlaska Domain = "ALASKA"
	DomainHawaii Domain = "HAWAII"
	DomainCarib  Domain = "CARIB"
	DomainGuam   Domain = "GUAM"
)

const (
	mrmsProductPath       = "MergedBaseReflectivityQC"
	mrmsLatestFile        = "MRMS_MergedBaseReflectivityQC.latest.grib2.gz"
	maxCompressedResponse = 32 * 1024 * 1024
	defaultHTTPTimeout    = 20 * time.Second
	pollInterval          = 60 * time.Second
)

func DomainForARTCC(artcc string) Domain {
	switch strings.ToUpper(strings.TrimSpace(artcc)) {
	case "ZAN":
		return DomainAlaska
	case "ZHN":
		return DomainHawaii
	case "ZSU":
		return DomainCarib
	case "ZUA":
		return DomainGuam
	default:
		return DomainCONUS
	}
}

func LatestURL(domain Domain) string {
	base := "https://mrms.ncep.noaa.gov/2D/"
	switch domain {
	case DomainAlaska, DomainHawaii, DomainCarib, DomainGuam:
		return base + string(domain) + "/" + mrmsProductPath + "/" + mrmsLatestFile
	default:
		return base + mrmsProductPath + "/" + mrmsLatestFile
	}
}

type Stream struct {
	updates <-chan *Grid

	cancel context.CancelFunc
	done   chan struct{}
}

func Start(
	parent context.Context,
	client *http.Client,
	domain Domain,
	bounds Bounds,
	logger *redslog.Logger,
) *Stream {
	if parent == nil {
		parent = context.Background()
	}
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}

	ctx, cancel := context.WithCancel(parent)
	updates := make(chan *Grid, 1)
	stream := &Stream{
		updates: updates,
		cancel:  cancel,
		done:    make(chan struct{}),
	}

	go stream.run(ctx, client, domain, bounds, updates, logger)
	return stream
}

func (s *Stream) Updates() <-chan *Grid {
	if s == nil {
		return nil
	}
	return s.updates
}

func (s *Stream) Close() {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
}

func (s *Stream) run(
	ctx context.Context,
	client *http.Client,
	domain Domain,
	bounds Bounds,
	updates chan *Grid,
	logger *redslog.Logger,
) {
	defer close(s.done)

	var etag string
	var lastModified string
	var lastObserved time.Time

	for {
		grid, nextETag, nextModified, notModified, err := fetchLatest(ctx, client, domain, bounds, etag, lastModified, lastObserved)
		if err != nil {
			if logger != nil {
				logger.Warn("MRMS update failed", slog.Any("error", err))
			}
		} else {
			if nextETag != "" {
				etag = nextETag
			}
			if nextModified != "" {
				lastModified = nextModified
			}
			if !notModified && grid != nil && grid.ObservedAt.After(lastObserved) {
				lastObserved = grid.ObservedAt
				sendLatest(updates, grid)
			}
		}

		wait := pollInterval + time.Duration(time.Now().UnixNano()%int64(5*time.Second))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func fetchLatest(
	ctx context.Context,
	client *http.Client,
	domain Domain,
	bounds Bounds,
	etag string,
	lastModified string,
	lastObserved time.Time,
) (*Grid, string, string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LatestURL(domain), nil)
	if err != nil {
		return nil, "", "", false, err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", "", false, err
	}
	defer resp.Body.Close()

	nextETag := resp.Header.Get("ETag")
	nextModified := resp.Header.Get("Last-Modified")

	if resp.StatusCode == http.StatusNotModified {
		return nil, nextETag, nextModified, true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, nextETag, nextModified, false, fmt.Errorf("MRMS HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCompressedResponse+1))
	if err != nil {
		return nil, nextETag, nextModified, false, err
	}
	if len(body) > maxCompressedResponse {
		return nil, nextETag, nextModified, false, fmt.Errorf("MRMS response too large")
	}

	observedAt, err := PeekMRMSObservedAt(bytes.NewReader(body))
	if err != nil {
		return nil, nextETag, nextModified, false, err
	}
	if !lastObserved.IsZero() && !observedAt.After(lastObserved) {
		return nil, nextETag, nextModified, false, nil
	}

	grid, err := DecodeMRMS(bytes.NewReader(body), bounds)
	if err != nil {
		return nil, nextETag, nextModified, false, err
	}
	return grid, nextETag, nextModified, false, nil
}

func sendLatest(updates chan *Grid, grid *Grid) {
	select {
	case updates <- grid:
	default:
		select {
		case <-updates:
		default:
		}
		select {
		case updates <- grid:
		default:
		}
	}
}

func PeekMRMSObservedAt(r io.Reader) (time.Time, error) {
	data, err := gunzipAll(r)
	if err != nil {
		return time.Time{}, err
	}
	message, err := parseGRIB(data, false)
	if err != nil {
		return time.Time{}, err
	}
	return message.observedAt, nil
}

func DecodeMRMS(r io.Reader, cropBounds Bounds) (*Grid, error) {
	data, err := gunzipAll(r)
	if err != nil {
		return nil, err
	}

	message, err := parseGRIB(data, true)
	if err != nil {
		return nil, err
	}
	if message.pngData == nil {
		return nil, fmt.Errorf("MRMS GRIB missing PNG section")
	}

	img, err := png.Decode(bytes.NewReader(message.pngData))
	if err != nil {
		return nil, fmt.Errorf("MRMS PNG decode: %w", err)
	}

	metadata := message.metadata
	crop := CropIndices(metadata, cropBounds)
	if crop.Empty() {
		return &Grid{
			ObservedAt: message.observedAt,
			Bounds:     boundsForCrop(metadata, crop),
			DLat:       metadata.DLat,
			DLon:       metadata.DLon,
		}, nil
	}

	levels := make([]Level, crop.NX()*crop.NY())
	for y := 0; y < crop.NY(); y++ {
		normalizedY := crop.Y0 + y
		sourceY := normalizedY
		if !message.sourceNorthToSouth {
			sourceY = metadata.NY - 1 - normalizedY
		}

		for x := 0; x < crop.NX(); x++ {
			normalizedX := crop.X0 + x
			sourceX := normalizedX
			if !message.sourceWestToEast {
				sourceX = metadata.NX - 1 - normalizedX
			}

			raw, missing := pngSample(img, sourceX, sourceY, message.bitWidth)
			dbz := scaledValue(raw, message.reference, message.binaryScale, message.decimalScale)
			levels[x+y*crop.NX()] = LevelForDBZ(dbz, missing)
		}
	}

	return &Grid{
		ObservedAt: message.observedAt,
		Bounds:     boundsForCrop(metadata, crop),
		NX:         crop.NX(),
		NY:         crop.NY(),
		DLat:       metadata.DLat,
		DLon:       metadata.DLon,
		Levels:     levels,
	}, nil
}

type gribMessage struct {
	observedAt time.Time
	metadata   GridMetadata

	sourceWestToEast   bool
	sourceNorthToSouth bool

	reference    float32
	binaryScale  int
	decimalScale int
	bitWidth     int

	pngData []byte
}

func parseGRIB(data []byte, includePNG bool) (gribMessage, error) {
	if len(data) < 20 {
		return gribMessage{}, fmt.Errorf("MRMS GRIB too short")
	}
	if string(data[:4]) != "GRIB" {
		return gribMessage{}, fmt.Errorf("MRMS GRIB missing magic")
	}
	if data[7] != 2 {
		return gribMessage{}, fmt.Errorf("MRMS GRIB edition %d is unsupported", data[7])
	}
	totalLength := binary.BigEndian.Uint64(data[8:16])
	if totalLength > uint64(len(data)) {
		return gribMessage{}, fmt.Errorf("MRMS GRIB declared length %d exceeds body %d", totalLength, len(data))
	}

	var out gribMessage
	offset := 16
	for offset+5 <= len(data) {
		if offset+4 <= len(data) && string(data[offset:offset+4]) == "7777" {
			break
		}

		sectionLength := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if sectionLength < 5 || offset+sectionLength > len(data) {
			return gribMessage{}, fmt.Errorf("MRMS GRIB invalid section at %d", offset)
		}

		section := data[offset : offset+sectionLength]
		switch section[4] {
		case 1:
			observedAt, err := parseSection1Time(section)
			if err != nil {
				return gribMessage{}, err
			}
			out.observedAt = observedAt
		case 3:
			metadata, westToEast, northToSouth, err := parseSection3Grid(section)
			if err != nil {
				return gribMessage{}, err
			}
			out.metadata = metadata
			out.sourceWestToEast = westToEast
			out.sourceNorthToSouth = northToSouth
		case 4:
			if err := validateSection4Product(section); err != nil {
				return gribMessage{}, err
			}
		case 5:
			reference, binaryScale, decimalScale, bitWidth, err := parseSection5Representation(section)
			if err != nil {
				return gribMessage{}, err
			}
			out.reference = reference
			out.binaryScale = binaryScale
			out.decimalScale = decimalScale
			out.bitWidth = bitWidth
		case 6:
			if err := validateSection6Bitmap(section); err != nil {
				return gribMessage{}, err
			}
		case 7:
			if includePNG {
				out.pngData = append([]byte(nil), section[5:]...)
			}
		}

		offset += sectionLength
	}

	if out.observedAt.IsZero() {
		return gribMessage{}, fmt.Errorf("MRMS GRIB missing reference time")
	}
	if out.metadata.NX <= 0 || out.metadata.NY <= 0 {
		return gribMessage{}, fmt.Errorf("MRMS GRIB missing grid metadata")
	}
	if out.bitWidth <= 0 {
		return gribMessage{}, fmt.Errorf("MRMS GRIB missing data representation")
	}
	return out, nil
}

func parseSection1Time(section []byte) (time.Time, error) {
	if len(section) < 21 {
		return time.Time{}, fmt.Errorf("MRMS GRIB section 1 too short")
	}

	year := int(binary.BigEndian.Uint16(section[12:14]))
	month := time.Month(section[14])
	day := int(section[15])
	hour := int(section[16])
	minute := int(section[17])
	second := int(section[18])

	return time.Date(year, month, day, hour, minute, second, 0, time.UTC), nil
}

func parseSection3Grid(section []byte) (GridMetadata, bool, bool, error) {
	if len(section) < 72 {
		return GridMetadata{}, false, false, fmt.Errorf("MRMS GRIB section 3 too short")
	}

	template := binary.BigEndian.Uint16(section[12:14])
	if template != 0 {
		return GridMetadata{}, false, false, fmt.Errorf("MRMS GRIB grid template %d is unsupported", template)
	}

	nx := int(binary.BigEndian.Uint32(section[30:34]))
	ny := int(binary.BigEndian.Uint32(section[34:38]))
	if nx <= 0 || ny <= 0 {
		return GridMetadata{}, false, false, fmt.Errorf("MRMS GRIB invalid grid size %dx%d", nx, ny)
	}

	lat1 := scaledMicroDegrees(section[46:50])
	lon1 := normalizeLon(scaledMicroDegrees(section[50:54]))
	lat2 := scaledMicroDegrees(section[55:59])
	lon2 := normalizeLon(scaledMicroDegrees(section[59:63]))
	dlon := float64(binary.BigEndian.Uint32(section[63:67])) / 1e6
	dlat := float64(binary.BigEndian.Uint32(section[67:71])) / 1e6
	if dlat <= 0 || dlon <= 0 {
		dlat = math.Abs(lat2-lat1) / math.Max(1, float64(ny-1))
		dlon = math.Abs(lon2-lon1) / math.Max(1, float64(nx-1))
	}
	if dlat <= 0 || dlon <= 0 {
		return GridMetadata{}, false, false, fmt.Errorf("MRMS GRIB invalid grid increments")
	}

	scanning := section[71]
	if scanning&0x20 != 0 {
		return GridMetadata{}, false, false, fmt.Errorf("MRMS GRIB j-adjacent scanning is unsupported")
	}

	westToEast := lon1 <= lon2
	northToSouth := lat1 >= lat2
	west := math.Min(lon1, lon2) - dlon/2
	east := math.Max(lon1, lon2) + dlon/2
	south := math.Min(lat1, lat2) - dlat/2
	north := math.Max(lat1, lat2) + dlat/2

	return GridMetadata{
		NX: nx,
		NY: ny,
		Bounds: Bounds{
			North: north,
			South: south,
			West:  normalizeLon(west),
			East:  normalizeLon(east),
		},
		DLat: dlat,
		DLon: dlon,
	}, westToEast, northToSouth, nil
}

func validateSection4Product(section []byte) error {
	if len(section) < 9 {
		return fmt.Errorf("MRMS GRIB section 4 too short")
	}
	return nil
}

func parseSection5Representation(section []byte) (float32, int, int, int, error) {
	if len(section) < 21 {
		return 0, 0, 0, 0, fmt.Errorf("MRMS GRIB section 5 too short")
	}

	template := binary.BigEndian.Uint16(section[9:11])
	if template != 41 {
		return 0, 0, 0, 0, fmt.Errorf("MRMS GRIB data template 5.%d is unsupported", template)
	}

	referenceBits := binary.BigEndian.Uint32(section[11:15])
	reference := math.Float32frombits(referenceBits)
	binaryScale := int(int16(binary.BigEndian.Uint16(section[15:17])))
	decimalScale := int(int16(binary.BigEndian.Uint16(section[17:19])))
	bitWidth := int(section[19])
	if bitWidth <= 0 || bitWidth > 16 {
		return 0, 0, 0, 0, fmt.Errorf("MRMS GRIB bit width %d is unsupported", bitWidth)
	}

	return reference, binaryScale, decimalScale, bitWidth, nil
}

func validateSection6Bitmap(section []byte) error {
	if len(section) < 6 {
		return fmt.Errorf("MRMS GRIB section 6 too short")
	}
	if section[5] != 255 {
		return fmt.Errorf("MRMS GRIB bitmap indicator %d is unsupported", section[5])
	}
	return nil
}

func scaledMicroDegrees(data []byte) float64 {
	return float64(int32(binary.BigEndian.Uint32(data))) / 1e6
}

func scaledValue(raw uint16, reference float32, binaryScale, decimalScale int) float32 {
	value := float64(reference) + math.Ldexp(float64(raw), binaryScale)
	if decimalScale != 0 {
		value *= math.Pow10(-decimalScale)
	}
	return float32(value)
}

func pngSample(img image.Image, x, y, bitWidth int) (uint16, bool) {
	if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
		return 0, true
	}

	b := img.Bounds()
	px := b.Min.X + x
	py := b.Min.Y + y

	switch typed := img.(type) {
	case *image.Gray:
		return uint16(typed.GrayAt(px, py).Y), false
	case *image.Gray16:
		return typed.Gray16At(px, py).Y, false
	}

	if bitWidth <= 8 {
		gray := color.GrayModel.Convert(img.At(px, py)).(color.Gray)
		return uint16(gray.Y), false
	}
	gray := color.Gray16Model.Convert(img.At(px, py)).(color.Gray16)
	return gray.Y, false
}

func boundsForCrop(metadata GridMetadata, crop Crop) Bounds {
	if crop.Empty() {
		return Bounds{}
	}
	return Bounds{
		North: metadata.Bounds.North - float64(crop.Y0)*metadata.DLat,
		South: metadata.Bounds.North - float64(crop.Y1)*metadata.DLat,
		West:  normalizeLon(metadata.Bounds.West + float64(crop.X0)*metadata.DLon),
		East:  normalizeLon(metadata.Bounds.West + float64(crop.X1)*metadata.DLon),
	}
}

func gunzipAll(r io.Reader) ([]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("MRMS gzip: %w", err)
	}
	defer gz.Close()

	data, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("MRMS gzip read: %w", err)
	}
	return data, nil
}
