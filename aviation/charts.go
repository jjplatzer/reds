// Package aviation handles FAA chart imagery and embedded georeferencing.
package aviation

import (
	"context"
	"fmt"
	"image"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SalvioniDigitalSolutions/gopdf"
)

const chartMaxBytes = 20 << 20
const chartDPI = 180.0

var chartCache = struct {
	sync.Mutex
	items map[string]*IAPChart
}{items: make(map[string]*IAPChart)}

// IAPChart contains a raster of the entire first PDF page, positioned in
// geographic coordinates. White ink alpha is modulated at draw time.
type IAPChart struct {
	Image    *image.RGBA
	Vertices []ChartVertex
	Indices  []uint32
}

type ChartVertex struct {
	Latitude, Longitude float32
	U, V                float32
}

type chartGeo struct {
	pageBox    [4]float64
	viewport   [4]float64
	lpts       [8]float64
	gpts       [8]float64
	projection lambert
}

// FetchIAPChart uses the same cycle as the STARS chart search window.
// It is intended to run in a background goroutine, never on the GL thread.
func FetchIAPChart(ctx context.Context, chartID, cycle string) (*IAPChart, error) {
	id := strings.ToUpper(strings.TrimSpace(chartID))
	if len(id) == 0 || len(id) > 32 {
		return nil, fmt.Errorf("invalid chart ID")
	}
	for _, c := range id {
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return nil, fmt.Errorf("invalid chart ID %q", id)
		}
	}
	for _, c := range cycle {
		if c < '0' || c > '9' {
			return nil, fmt.Errorf("invalid d-TPP cycle")
		}
	}
	if len(cycle) != 4 {
		return nil, fmt.Errorf("invalid d-TPP cycle")
	}

	key := cycle + ":" + id
	chartCache.Lock()
	if hit := chartCache.items[key]; hit != nil {
		chartCache.Unlock()
		return hit, nil
	}
	chartCache.Unlock()

	url := "https://aeronav.faa.gov/d-tpp/" + cycle + "/" + id + ".PDF"
	client := &http.Client{Timeout: 25 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("FAA returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > chartMaxBytes {
		return nil, fmt.Errorf("chart PDF too large")
	}
	data, err := ioReadBounded(resp.Body, chartMaxBytes)
	if err != nil {
		return nil, err
	}
	reader, err := gopdf.NewReader(data)
	if err != nil {
		return nil, fmt.Errorf("parse FAA PDF: %w", err)
	}
	if reader.NumPages() == 0 {
		return nil, fmt.Errorf("FAA PDF has no pages")
	}

	geo, err := readChartGeo(reader)
	if err != nil {
		return nil, err
	}
	img, _, err := reader.RenderPageDetail(0, gopdf.RenderOpts{
		DPI: chartDPI, IncludeText: true, IncludeVector: true,
		IncludeRasterImages: true, IncludeAnnotations: true,
		SubstituteFont: gopdf.SystemFonts(),
	})
	if err != nil {
		return nil, fmt.Errorf("rasterize FAA PDF: %w", err)
	}
	converted := makeChartOverlay(img)
	vertices, indices, err := chartMesh(geo, 16, 24)
	if err != nil {
		return nil, err
	}
	result := &IAPChart{Image: converted, Vertices: vertices, Indices: indices}
	chartCache.Lock()
	if len(chartCache.items) >= 3 {
		// Small bounded cache: enabling/disabling the same chart is instant.
		for k := range chartCache.items {
			delete(chartCache.items, k)
			break
		}
	}
	chartCache.items[key] = result
	chartCache.Unlock()
	return result, nil
}

func ioReadBounded(r io.Reader, maximum int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("chart PDF exceeds %d bytes", maximum)
	}
	return data, nil
}

func makeChartOverlay(src image.Image) *image.RGBA {
	bounds := src.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			r, g, b, _ := src.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			// Drop pale backgrounds and preserve dark antialiased ink.
			// The source is rasterized on white, so white becomes fully clear.
			lum := (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 65535
			ink := math.Max(0, math.Min(1, (0.95-lum)/0.9))
			alpha := uint8(math.Round(255 * ink))
			i := out.PixOffset(x, y)
			// OpenGL uploads the raw RGBA bytes, which are straight alpha.
			out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = 255, 255, 255, alpha
		}
	}
	return out
}

func chartDict(r *gopdf.Reader, obj any) (gopdf.Dict, bool) {
	d, ok := r.Resolve(obj).(gopdf.Dict)
	return d, ok
}

func chartArray(r *gopdf.Reader, obj any, count int) ([]float64, bool) {
	array, ok := r.Resolve(obj).(gopdf.Array)
	if !ok || len(array) < count {
		return nil, false
	}
	values := make([]float64, count)
	for i := 0; i < count; i++ {
		switch v := r.Resolve(array[i]).(type) {
		case float64:
			values[i] = v
		case int64:
			values[i] = float64(v)
		default:
			return nil, false
		}
	}
	return values, true
}

func readChartGeo(r *gopdf.Reader) (chartGeo, error) {
	var result chartGeo
	page := r.PageDict(0)
	if page == nil {
		return result, fmt.Errorf("no first PDF page")
	}
	media, ok := chartArray(r, page["MediaBox"], 4)
	if !ok {
		return result, fmt.Errorf("chart MediaBox unavailable")
	}
	copy(result.pageBox[:], media)
	vpArray, ok := r.Resolve(page["VP"]).(gopdf.Array)
	if !ok {
		return result, fmt.Errorf("FAA PDF has no geospatial /VP")
	}
	for _, vpObj := range vpArray {
		vp, ok := chartDict(r, vpObj)
		if !ok {
			continue
		}
		m, ok := chartDict(r, vp["Measure"])
		if !ok {
			continue
		}
		subtype, ok := r.Resolve(m["Subtype"]).(gopdf.Name)
		if !ok || string(subtype) != "GEO" {
			continue
		}
		bbox, ok1 := chartArray(r, vp["BBox"], 4)
		lpts, ok2 := chartArray(r, m["LPTS"], 8)
		gpts, ok3 := chartArray(r, m["GPTS"], 8)
		gcs, ok4 := chartDict(r, m["GCS"])
		if !(ok1 && ok2 && ok3 && ok4) {
			continue
		}
		wkt, ok := r.Resolve(gcs["WKT"]).(gopdf.String)
		if !ok {
			continue
		}
		proj, err := parseLambert(string(wkt))
		if err != nil {
			return result, err
		}
		copy(result.viewport[:], bbox)
		copy(result.lpts[:], lpts)
		copy(result.gpts[:], gpts)
		result.projection = proj
		return result, nil
	}
	return result, fmt.Errorf("FAA PDF lacks a supported /GEO viewport with Lambert WKT")
}

type lambert struct {
	a, e, n, F, rho0, lon0 float64
}

func wktNumber(wkt, key string) (float64, error) {
	re := regexp.MustCompile(`(?i)PARAMETER\s*\[\s*"` + regexp.QuoteMeta(key) + `"\s*,\s*([-+0-9.eE]+)`)
	m := re.FindStringSubmatch(wkt)
	if len(m) < 2 {
		return 0, fmt.Errorf("LCC WKT missing %s", key)
	}
	return strconv.ParseFloat(m[1], 64)
}

func parseLambert(wkt string) (lambert, error) {
	if !strings.Contains(strings.ToLower(wkt), "lambert_conformal_conic") {
		return lambert{}, fmt.Errorf("unsupported FAA chart projection (expected Lambert Conformal Conic)")
	}
	a, invF := 6378137.0, 298.257222101
	if re := regexp.MustCompile(`(?i)SPHEROID\s*\[\s*"[^"]+"\s*,\s*([-+0-9.eE]+)\s*,\s*([-+0-9.eE]+)`); re != nil {
		m := re.FindStringSubmatch(wkt)
		if len(m) == 3 {
			var err error
			a, err = strconv.ParseFloat(m[1], 64)
			if err != nil {
				return lambert{}, err
			}
			invF, err = strconv.ParseFloat(m[2], 64)
			if err != nil {
				return lambert{}, err
			}
		}
	}
	if a <= 0 || invF <= 1 {
		return lambert{}, fmt.Errorf("invalid WKT ellipsoid")
	}
	lon0, err := wktNumber(wkt, "Central_Meridian")
	if err != nil {
		return lambert{}, err
	}
	lat0, err := wktNumber(wkt, "Latitude_Of_Origin")
	if err != nil {
		return lambert{}, err
	}
	phi1, err := wktNumber(wkt, "Standard_Parallel_1")
	if err != nil {
		return lambert{}, err
	}
	phi2, err := wktNumber(wkt, "Standard_Parallel_2")
	if err != nil {
		return lambert{}, err
	}
	const rad = math.Pi / 180
	f := 1 / invF
	e := math.Sqrt(2*f - f*f)
	m := func(phi float64) float64 { return math.Cos(phi) / math.Sqrt(1-e*e*math.Pow(math.Sin(phi), 2)) }
	t := func(phi float64) float64 {
		s := e * math.Sin(phi)
		return math.Tan(math.Pi/4-phi/2) / math.Pow((1-s)/(1+s), e/2)
	}
	p1, p2 := phi1*rad, phi2*rad
	n := math.Sin(p1)
	if math.Abs(p1-p2) > 1e-12 {
		n = math.Log(m(p1)/m(p2)) / math.Log(t(p1)/t(p2))
	}
	if math.Abs(n) < 1e-12 {
		return lambert{}, fmt.Errorf("degenerate Lambert projection")
	}
	F := m(p1) / (n * math.Pow(t(p1), n))
	return lambert{a: a, e: e, n: n, F: F, rho0: a * F * math.Pow(t(lat0*rad), n), lon0: lon0 * rad}, nil
}

func (p lambert) forward(lat, lon float64) (float64, float64) {
	const rad = math.Pi / 180
	phi := lat * rad
	s := p.e * math.Sin(phi)
	t := math.Tan(math.Pi/4-phi/2) / math.Pow((1-s)/(1+s), p.e/2)
	rho := p.a * p.F * math.Pow(t, p.n)
	theta := p.n * (lon*rad - p.lon0)
	return rho * math.Sin(theta), p.rho0 - rho*math.Cos(theta)
}

func (p lambert) inverse(x, y float64) (lat, lon float64) {
	rho := math.Copysign(math.Hypot(x, p.rho0-y), p.n)
	theta := math.Atan2(x*math.Copysign(1, p.n), (p.rho0-y)*math.Copysign(1, p.n))
	t := math.Pow(rho/(p.a*p.F), 1/p.n)
	phi := math.Pi/2 - 2*math.Atan(t)
	for i := 0; i < 12; i++ {
		s := p.e * math.Sin(phi)
		next := math.Pi/2 - 2*math.Atan(t*math.Pow((1-s)/(1+s), p.e/2))
		if math.Abs(next-phi) < 1e-14 {
			phi = next
			break
		}
		phi = next
	}
	return phi * 180 / math.Pi, (p.lon0 + theta/p.n) * 180 / math.Pi
}

// solve3 fits the affine PDF-point -> Lambert-metres map via the control points.
func solve3(A [3][3]float64, b [3]float64) ([3]float64, error) {
	for i := 0; i < 3; i++ {
		pivot := i
		for j := i + 1; j < 3; j++ {
			if math.Abs(A[j][i]) > math.Abs(A[pivot][i]) {
				pivot = j
			}
		}
		if math.Abs(A[pivot][i]) < 1e-8 {
			return [3]float64{}, fmt.Errorf("degenerate georeference")
		}
		A[i], A[pivot] = A[pivot], A[i]
		b[i], b[pivot] = b[pivot], b[i]
		for j := i + 1; j < 3; j++ {
			scale := A[j][i] / A[i][i]
			for k := i; k < 3; k++ {
				A[j][k] -= scale * A[i][k]
			}
			b[j] -= scale * b[i]
		}
	}
	var x [3]float64
	for i := 2; i >= 0; i-- {
		x[i] = b[i]
		for j := i + 1; j < 3; j++ {
			x[i] -= A[i][j] * x[j]
		}
		x[i] /= A[i][i]
	}
	return x, nil
}

func chartMesh(geo chartGeo, cols, rows int) ([]ChartVertex, []uint32, error) {
	var ata [3][3]float64
	var bx, by [3]float64
	var control [4][4]float64 // pdfX,pdfY,lambertX,lambertY
	v := geo.viewport
	for i := 0; i < 4; i++ {
		x := v[0] + geo.lpts[i*2]*(v[2]-v[0])
		y := v[1] + geo.lpts[i*2+1]*(v[3]-v[1])
		xp, yp := geo.projection.forward(geo.gpts[2*i], geo.gpts[2*i+1])
		control[i] = [4]float64{x, y, xp, yp}
		basis := [3]float64{x, y, 1}
		for j := 0; j < 3; j++ {
			bx[j] += basis[j] * xp
			by[j] += basis[j] * yp
			for k := 0; k < 3; k++ {
				ata[j][k] += basis[j] * basis[k]
			}
		}
	}
	cx, err := solve3(ata, bx)
	if err != nil {
		return nil, nil, err
	}
	cy, err := solve3(ata, by)
	if err != nil {
		return nil, nil, err
	}
	// Reject inconsistent geographic metadata rather than project an invented location.
	for _, c := range control {
		x := cx[0]*c[0] + cx[1]*c[1] + cx[2]
		y := cy[0]*c[0] + cy[1]*c[1] + cy[2]
		if math.Hypot(x-c[2], y-c[3]) > 100 {
			return nil, nil, fmt.Errorf("geospatial PDF control points disagree")
		}
	}
	box := geo.pageBox
	w, h := box[2]-box[0], box[3]-box[1]
	if w <= 0 || h <= 0 {
		return nil, nil, fmt.Errorf("invalid PDF page dimensions")
	}
	vertices := make([]ChartVertex, 0, (cols+1)*(rows+1))
	for j := 0; j <= rows; j++ {
		vf := float64(j) / float64(rows)
		pdfY := box[3] - vf*h
		for i := 0; i <= cols; i++ {
			uf := float64(i) / float64(cols)
			pdfX := box[0] + uf*w
			px := cx[0]*pdfX + cx[1]*pdfY + cx[2]
			py := cy[0]*pdfX + cy[1]*pdfY + cy[2]
			lat, lon := geo.projection.inverse(px, py)
			if math.IsNaN(lat) || math.IsInf(lat, 0) || math.IsNaN(lon) || math.IsInf(lon, 0) {
				return nil, nil, fmt.Errorf("invalid projected chart vertex")
			}
			vertices = append(vertices, ChartVertex{float32(lat), float32(lon), float32(uf), float32(vf)})
		}
	}
	indices := make([]uint32, 0, 6*cols*rows)
	for j := 0; j < rows; j++ {
		for i := 0; i < cols; i++ {
			a := uint32(j*(cols+1) + i)
			b := a + 1
			c := a + uint32(cols+1)
			d := c + 1
			indices = append(indices, a, b, d, a, d, c)
		}
	}
	return vertices, indices, nil
}
