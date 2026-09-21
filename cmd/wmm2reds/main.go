package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/juliusplatzer/reds/radar"
	"github.com/klauspost/compress/zstd"
)

const defaultEpsilonDegrees = 0.5

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "wmm2reds:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("wmm2reds", flag.ContinueOnError)
	inPath := fs.String("in", "cmd/wmm2reds/grid.txt.zst", "input WMM declination grid (.txt or .txt.zst)")
	outPath := fs.String("out", "resources/nav/tiles.json.zst", "output REDS magnetic tile set (.json or .json.zst)")
	epsilon := fs.Float64("eps", defaultEpsilonDegrees, "maximum allowed WMM deviation inside one generated tile, in degrees")
	minLat := fs.Float64("min-lat", radar.DefaultWMMGridSpec.MinLatitude, "input grid minimum latitude")
	maxLat := fs.Float64("max-lat", radar.DefaultWMMGridSpec.MaxLatitude, "input grid maximum latitude")
	minLon := fs.Float64("min-lon", radar.DefaultWMMGridSpec.MinLongitude, "input grid minimum longitude")
	maxLon := fs.Float64("max-lon", radar.DefaultWMMGridSpec.MaxLongitude, "input grid maximum longitude")
	step := fs.Float64("step", radar.DefaultWMMGridSpec.Step, "input grid spacing in degrees")
	epoch := fs.Int("epoch", radar.DefaultWMMGridSpec.Epoch, "WMM epoch/year represented by the input grid")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	r, closeInput, err := openMaybeZstd(*inPath)
	if err != nil {
		return err
	}
	defer closeInput()

	spec := radar.WMMGridSpec{
		MinLatitude:    *minLat,
		MaxLatitude:    *maxLat,
		MinLongitude:   *minLon,
		MaxLongitude:   *maxLon,
		Step:           *step,
		Epoch:          *epoch,
		AltitudeMeters: radar.DefaultWMMGridSpec.AltitudeMeters,
		Source: fmt.Sprintf("NOAA World Magnetic Model; %.0f m; %d epoch; %.6g-degree sampled declination grid",
			radar.DefaultWMMGridSpec.AltitudeMeters, *epoch, *step),
	}
	grid, err := radar.ParseWMMGrid(r, spec)
	if err != nil {
		return fmt.Errorf("parse %s: %w", *inPath, err)
	}

	tiles, err := radar.GenerateMagneticVariationTileSet(grid, *epsilon)
	if err != nil {
		return fmt.Errorf("generate magnetic variation tiles: %w", err)
	}
	if err := writeJSONMaybeZstd(*outPath, tiles); err != nil {
		return err
	}

	fmt.Printf("wrote %s\n", *outPath)
	fmt.Printf("source: %s\n", tiles.Source)
	fmt.Printf("tiles: %d\n", len(tiles.Tiles))
	fmt.Printf("tolerance: %.6g deg\n", tiles.ToleranceDegrees)
	fmt.Printf("max observed error: %.6g deg\n", tiles.MaxErrorDegrees)
	return nil
}

func openMaybeZstd(path string) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open %s: %w", path, err)
	}
	if !strings.HasSuffix(strings.ToLower(path), ".zst") {
		return f, func() { _ = f.Close() }, nil
	}

	zr, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
	if err != nil {
		_ = f.Close()
		return nil, func() {}, fmt.Errorf("open zstd %s: %w", path, err)
	}
	return zr, func() {
		zr.Close()
		_ = f.Close()
	}, nil
}

func writeJSONMaybeZstd(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".wmm2reds-*")
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	var writer io.Writer = tmp
	var zw *zstd.Encoder
	if strings.HasSuffix(strings.ToLower(path), ".zst") {
		zw, err = zstd.NewWriter(tmp,
			zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(19)),
			zstd.WithEncoderConcurrency(1),
		)
		if err != nil {
			return fmt.Errorf("create zstd output: %w", err)
		}
		writer = zw
	}

	enc := json.NewEncoder(writer)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		if zw != nil {
			_ = zw.Close()
		}
		return fmt.Errorf("encode magnetic tile set: %w", err)
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			return fmt.Errorf("close zstd output: %w", err)
		}
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync output: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	committed = true
	return nil
}
