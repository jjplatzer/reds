package main

import (
	"encoding/xml"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/AllenDang/cimgui-go/imgui"
	"github.com/go-gl/gl/v2.1/gl"
)

const chevronSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="6" viewBox="0 0 10 6" fill="none" stroke="#9fa0a2" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M1 1 L5 5 L9 1"/></svg>`

type svgIcon struct {
	texture uint32
	ref     *imgui.TextureRef
}

var chevronIcon *svgIcon

func initSVGIcons() error {
	icon, err := newSVGIconTexture(chevronSVG, 4)
	if err != nil {
		return err
	}
	chevronIcon = icon
	return nil
}

func disposeSVGIcons() {
	if chevronIcon == nil {
		return
	}
	if chevronIcon.texture != 0 {
		gl.DeleteTextures(1, &chevronIcon.texture)
	}
	if chevronIcon.ref != nil {
		chevronIcon.ref.Destroy()
	}
	chevronIcon = nil
}

func newSVGIconTexture(src string, scale int) (*svgIcon, error) {
	if scale < 1 {
		scale = 1
	}
	width, height, pixels, err := renderStrokeSVG(src, scale)
	if err != nil {
		return nil, err
	}

	var texture uint32
	gl.GenTextures(1, &texture)
	gl.BindTexture(gl.TEXTURE_2D, texture)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexImage2D(
		gl.TEXTURE_2D,
		0,
		int32(gl.RGBA),
		int32(width),
		int32(height),
		0,
		gl.RGBA,
		gl.UNSIGNED_BYTE,
		gl.Ptr(pixels),
	)
	gl.BindTexture(gl.TEXTURE_2D, 0)

	return &svgIcon{
		texture: texture,
		ref:     imgui.NewTextureRefTextureID(imgui.TextureID(texture)),
	}, nil
}

type svgRoot struct {
	Width       string    `xml:"width,attr"`
	Height      string    `xml:"height,attr"`
	ViewBox     string    `xml:"viewBox,attr"`
	StrokeWidth string    `xml:"stroke-width,attr"`
	Paths       []svgPath `xml:"path"`
}

type svgPath struct {
	D           string `xml:"d,attr"`
	StrokeWidth string `xml:"stroke-width,attr"`
}

type svgPoint struct {
	x float64
	y float64
}

func renderStrokeSVG(src string, scale int) (int, int, []uint8, error) {
	var root svgRoot
	if err := xml.Unmarshal([]byte(src), &root); err != nil {
		return 0, 0, nil, err
	}
	width, err := parseSVGLength(root.Width)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("svg width: %w", err)
	}
	height, err := parseSVGLength(root.Height)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("svg height: %w", err)
	}
	if len(root.Paths) == 0 {
		return 0, 0, nil, fmt.Errorf("svg has no path")
	}
	strokeWidthText := root.Paths[0].StrokeWidth
	if strokeWidthText == "" {
		strokeWidthText = root.StrokeWidth
	}
	strokeWidth, err := parseSVGLength(strokeWidthText)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("svg stroke width: %w", err)
	}
	points, err := parseSVGPolylinePath(root.Paths[0].D)
	if err != nil {
		return 0, 0, nil, err
	}
	if len(points) < 2 {
		return 0, 0, nil, fmt.Errorf("svg path needs at least two points")
	}

	outW := int(math.Ceil(width * float64(scale)))
	outH := int(math.Ceil(height * float64(scale)))
	pixels := make([]uint8, outW*outH*4)
	halfStroke := strokeWidth / 2
	aa := 0.75 / float64(scale)

	for y := 0; y < outH; y++ {
		for x := 0; x < outW; x++ {
			px := (float64(x) + 0.5) / float64(scale)
			py := (float64(y) + 0.5) / float64(scale)
			dist := math.MaxFloat64
			for i := 0; i < len(points)-1; i++ {
				dist = math.Min(dist, distancePointSegment(px, py, points[i], points[i+1]))
			}
			alpha := strokeCoverage(dist, halfStroke, aa)
			if alpha == 0 {
				continue
			}
			o := (y*outW + x) * 4
			pixels[o+0] = 255
			pixels[o+1] = 255
			pixels[o+2] = 255
			pixels[o+3] = uint8(math.Round(alpha * 255))
		}
	}

	return outW, outH, pixels, nil
}

func parseSVGLength(text string) (float64, error) {
	text = strings.TrimSpace(strings.TrimSuffix(text, "px"))
	if text == "" {
		return 0, fmt.Errorf("missing value")
	}
	return strconv.ParseFloat(text, 64)
}

func parseSVGPolylinePath(path string) ([]svgPoint, error) {
	path = strings.NewReplacer(",", " ", "M", " M ", "m", " M ", "L", " L ", "l", " L ").Replace(path)
	fields := strings.Fields(path)
	var points []svgPoint
	for i := 0; i < len(fields); {
		cmd := fields[i]
		if cmd != "M" && cmd != "L" {
			return nil, fmt.Errorf("unsupported svg path command %q", cmd)
		}
		if i+2 >= len(fields) {
			return nil, fmt.Errorf("svg path command %q is missing coordinates", cmd)
		}
		x, err := strconv.ParseFloat(fields[i+1], 64)
		if err != nil {
			return nil, err
		}
		y, err := strconv.ParseFloat(fields[i+2], 64)
		if err != nil {
			return nil, err
		}
		points = append(points, svgPoint{x: x, y: y})
		i += 3
	}
	return points, nil
}

func distancePointSegment(px, py float64, a, b svgPoint) float64 {
	vx := b.x - a.x
	vy := b.y - a.y
	wx := px - a.x
	wy := py - a.y
	len2 := vx*vx + vy*vy
	if len2 == 0 {
		return math.Hypot(px-a.x, py-a.y)
	}
	t := (wx*vx + wy*vy) / len2
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	cx := a.x + t*vx
	cy := a.y + t*vy
	return math.Hypot(px-cx, py-cy)
}

func strokeCoverage(distance, halfStroke, aa float64) float64 {
	if distance <= halfStroke-aa {
		return 1
	}
	if distance >= halfStroke+aa {
		return 0
	}
	return (halfStroke + aa - distance) / (2 * aa)
}
