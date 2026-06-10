package asdex

// ASDE-X datablock field model.
//
// The datablock is assembled from individual fields, not from fixed semantic
// lines. Settings decide which fields are appended to each display line.
//
// Line 0:
//   Duplicate beacon field:
//     "DUP BCN" when duplicate beacon logic is active.
//     Not implemented in the first REDS pass.
//
// Line 1:
//   ACID field:
//     aircraft identification / target identity.
//     Usually Target.Callsign, otherwise Target.Beacon / "NO BCN".
//   Altitude field:
//     altitude in hundreds of feet, or "XXX" when unavailable.
//     Controlled by ShowAltitude.
//   Sensor/coast field:
//     "CST" when coasting, otherwise sensor text such as "FUS".
//     Sensor text is controlled by ShowSensors.
//
// Line 2 primary:
//   Target type field:
//     Target.TargetType, e.g. "A320", "B738", "VEH".
//     Controlled by ShowTargetType.
//   CWT field:
//     Target.CWT.
//     Controlled by ShowCWT.
//   Fix field:
//     Target.Fix.
//     Controlled by ShowFix.
//   Velocity field:
//     Target.GroundSpeedKt in tens of knots.
//     Controlled by ShowVelocity.
//
// Line 2 scratchpad:
//   Target.Scratchpad1 and Target.Scratchpad2.
//   Controlled by ShowScratchpads.
//
// When both primary line 2 and scratchpad line 2 exist, the displayed second
// line timeshares between them unless AlertInProgress forces the primary line.

import (
	"fmt"
	stdmath "math"
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

type LeaderDirection int

const (
	LeaderNE LeaderDirection = iota
	LeaderN
	LeaderE
	LeaderSE
	LeaderS
	LeaderSW
	LeaderW
	LeaderNW
)

const (
	datablockLineSpacing = 2

	leaderStartOffsetPx = 7
	leaderStepPx        = 15
	zeroLengthAnchorPx  = 10
)

type DataBlockSettings struct {
	ShowDataBlocks bool
	FullDataBlocks bool

	FontSize        int
	Brightness      int
	LeaderLength    int
	LeaderDirection LeaderDirection

	TimesharePrimary bool
	AlertInProgress  bool

	ShowAltitude    bool
	ShowTargetType  bool
	ShowSensors     bool
	ShowCWT         bool
	ShowFix         bool
	ShowVelocity    bool
	ShowScratchpads bool
}

type datablockField int

const (
	datablockFieldDefault datablockField = iota
	datablockFieldTargetType
	datablockFieldScratchpad
)

func fmtField(field datablockField, value string) string {
	value = strings.TrimSpace(value)
	switch field {
	case datablockFieldTargetType:
		return fitFieldWidth(value, 4)
	case datablockFieldScratchpad:
		// Scratchpad constraints will be added with the DB edit commands.
		return value
	default:
		return value
	}
}

func fitFieldWidth(value string, width int) string {
	if value == "" || width <= 0 {
		return value
	}

	runes := []rune(value)
	if len(runes) > width {
		runes = runes[:width]
	}
	for len(runes) < width {
		runes = append(runes, ' ')
	}
	return string(runes)
}

func DefaultDataBlockSettings() DataBlockSettings {
	return DataBlockSettings{
		ShowDataBlocks: true,
		FullDataBlocks: true,

		FontSize:        2,
		Brightness:      brightnessDefault,
		LeaderLength:    2,
		LeaderDirection: LeaderNE,

		TimesharePrimary: true,
		AlertInProgress:  false,

		ShowAltitude:    false,
		ShowTargetType:  true,
		ShowSensors:     false,
		ShowCWT:         false,
		ShowFix:         true,
		ShowVelocity:    false,
		ShowScratchpads: true,
	}
}

func leaderHeadingDegrees(direction LeaderDirection) int {
	switch direction {
	case LeaderN:
		return 360
	case LeaderE:
		return 90
	case LeaderSE:
		return 135
	case LeaderS:
		return 180
	case LeaderSW:
		return 225
	case LeaderW:
		return 270
	case LeaderNW:
		return 315
	default:
		return 45
	}
}

func isLeftDatablock(direction LeaderDirection) bool {
	return direction == LeaderSW || direction == LeaderW || direction == LeaderNW
}

func leaderDelta(distancePx float32, headingDegrees int) redsmath.Vec2 {
	radians := float64(headingDegrees) * stdmath.Pi / 180
	dx := distancePx * float32(stdmath.Sin(radians))
	dy := distancePx * float32(stdmath.Cos(radians))
	return redsmath.Vec2{
		X: float32(int(dx)),
		Y: -float32(int(dy)),
	}
}

func datablockIDField(target *Target, showBeaconCode bool) string {
	if target == nil {
		return ""
	}
	if !showBeaconCode {
		if callsign := strings.TrimSpace(target.Callsign); callsign != "" {
			return callsign
		}
	}
	return beaconField(target)
}

func beaconField(target *Target) string {
	if target == nil {
		return "NO BCN"
	}

	beacon := strings.TrimSpace(target.Beacon)
	if beacon == "" {
		return "NO BCN"
	}
	for len(beacon) < 4 {
		beacon = "0" + beacon
	}
	return beacon
}

func altitudeField(target *Target) string {
	if target == nil || !target.HasAltitude {
		return "XXX"
	}

	hundreds := int(stdmath.Round(float64(target.AltitudeFt) / 100))
	return fmt.Sprintf("%03d", clampInt(hundreds, 0, 999))
}

func sensorField(target *Target) string {
	if target == nil {
		return ""
	}
	if target.Coasting {
		return "CST"
	}

	source := strings.TrimSpace(string(target.Source))
	if source == "" {
		return "FUS"
	}
	return source
}

func targetTypeField(target *Target) string {
	if target == nil || target.TargetType == nil {
		return ""
	}
	return fmtField(datablockFieldTargetType, *target.TargetType)
}

func cwtField(target *Target) string {
	if target == nil {
		return ""
	}
	return strings.TrimSpace(target.CWT)
}

func fixField(target *Target) string {
	if target == nil {
		return ""
	}
	return strings.TrimSpace(target.Fix)
}

func velocityField(target *Target) string {
	if target == nil {
		return ""
	}

	tens := int(stdmath.Round(float64(target.GroundSpeedKt) / 10))
	return fmt.Sprintf("%02d", clampInt(tens, 0, 99))
}

func appendDBField(line string, field string) string {
	if strings.TrimSpace(field) == "" {
		return line
	}

	if strings.TrimSpace(line) == "" {
		return field
	}
	return line + " " + field
}

func buildLine1(target *Target, settings DataBlockSettings, showBeaconCode bool) string {
	line := datablockIDField(target, showBeaconCode)
	if settings.ShowAltitude {
		line = appendDBField(line, altitudeField(target))
	}

	if target != nil && target.Coasting {
		line = appendDBField(line, "CST")
	} else if settings.ShowSensors {
		line = appendDBField(line, sensorField(target))
	}
	return line
}

func buildPrimaryLine2(target *Target, settings DataBlockSettings) string {
	var line string
	if settings.ShowTargetType {
		line = appendDBField(line, targetTypeField(target))
	}
	if settings.ShowCWT {
		line = appendDBField(line, cwtField(target))
	}
	if settings.ShowFix {
		line = appendDBField(line, fixField(target))
	}
	if settings.ShowVelocity {
		line = appendDBField(line, velocityField(target))
	}
	return line
}

func scratchpadLine(target *Target, settings DataBlockSettings) string {
	if target == nil || !settings.ShowScratchpads {
		return ""
	}

	var line string
	line = appendDBField(line, target.Scratchpad1)
	line = appendDBField(line, target.Scratchpad2)
	return line
}

func chooseLine2(primaryLine2 string, scratchLine2 string, settings DataBlockSettings) string {
	hasPrimary := strings.TrimSpace(primaryLine2) != ""
	hasScratch := strings.TrimSpace(scratchLine2) != ""
	if settings.AlertInProgress {
		return primaryLine2
	}
	if hasPrimary && hasScratch {
		if settings.TimesharePrimary {
			return primaryLine2
		}
		return scratchLine2
	}
	if hasPrimary {
		return primaryLine2
	}
	return scratchLine2
}

type builtDataBlock struct {
	lines []string

	maxLineWidth int

	longestHighestLineNumber int
	longestLowestLineNumber  int
}

func buildDataBlock(
	target *Target,
	settings DataBlockSettings,
	font *renderer.BitmapFont,
	showBeaconCode bool,
) builtDataBlock {
	var out builtDataBlock

	// Duplicate beacon warnings are reserved for the next target-logic pass.
	duplicateBeaconLine := ""
	out.lines = append(out.lines, duplicateBeaconLine)

	if !settings.FullDataBlocks {
		line1 := datablockIDField(target, showBeaconCode)
		out.lines = append(out.lines, line1)
		measureDataBlockLine(&out, duplicateBeaconLine, 0, settings.FontSize, font)
		measureDataBlockLine(&out, line1, 1, settings.FontSize, font)
		return out
	}

	line1 := buildLine1(target, settings, showBeaconCode)
	primaryLine2 := buildPrimaryLine2(target, settings)
	scratchLine2 := scratchpadLine(target, settings)
	line2 := chooseLine2(primaryLine2, scratchLine2, settings)

	out.lines = append(out.lines, line1, line2)
	measureDataBlockLine(&out, duplicateBeaconLine, 0, settings.FontSize, font)
	measureDataBlockLine(&out, line1, 1, settings.FontSize, font)
	measureDataBlockLine(&out, primaryLine2, 2, settings.FontSize, font)
	measureDataBlockLine(&out, scratchLine2, 2, settings.FontSize, font)
	return out
}

func measureDataBlockLine(
	block *builtDataBlock,
	line string,
	lineNumber int,
	fontSize int,
	font *renderer.BitmapFont,
) {
	if block == nil || font == nil {
		return
	}

	if strings.TrimSpace(line) == "" {
		return
	}
	width, _ := font.MeasureText(line, fontSize)
	if width <= 0 {
		return
	}

	if width > block.maxLineWidth {
		block.maxLineWidth = width
		block.longestHighestLineNumber = lineNumber
		block.longestLowestLineNumber = lineNumber
		return
	}
	if width == block.maxLineWidth {
		if lineNumber < block.longestHighestLineNumber {
			block.longestHighestLineNumber = lineNumber
		}
		if lineNumber > block.longestLowestLineNumber {
			block.longestLowestLineNumber = lineNumber
		}
	}
}

func drawOneDataBlock(
	target *Target,
	targetScreen redsmath.Vec2,
	lineBuilder *renderer.LinesBuilder,
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	settings DataBlockSettings,
	showBeaconCode bool,
) {
	if target == nil || lineBuilder == nil || td == nil || font == nil {
		return
	}

	block := buildDataBlock(target, settings, font, showBeaconCode)
	if block.maxLineWidth <= 0 {
		return
	}

	height := font.LineHeight(settings.FontSize)
	if height <= 0 {
		return
	}

	direction := settings.LeaderDirection
	heading := leaderHeadingDegrees(direction)
	left := isLeftDatablock(direction)

	leaderLengthPx := settings.LeaderLength * leaderStepPx
	if leaderLengthPx < 0 {
		leaderLengthPx = 0
	}

	leaderStart := targetScreen.Add(leaderDelta(leaderStartOffsetPx, heading))
	anchorDistance := float32(leaderLengthPx)
	if leaderLengthPx == 0 {
		anchorDistance = zeroLengthAnchorPx
	}
	leaderEnd := targetScreen.Add(leaderDelta(anchorDistance, heading))

	if leaderLengthPx > 0 {
		lineBuilder.AddLine(
			renderer.PointVertex{X: leaderStart.X, Y: leaderStart.Y},
			renderer.PointVertex{X: leaderEnd.X, Y: leaderEnd.Y},
		)
	}

	verticalLeftOffset := 0
	if left {
		selectedLine := block.longestHighestLineNumber
		if direction == LeaderNW {
			selectedLine = block.longestLowestLineNumber
		}
		verticalLeftOffset = (height + datablockLineSpacing) * (-1 + selectedLine)
	}

	textX := int(leaderEnd.X)
	if left {
		textX += -2 - block.maxLineWidth
	} else {
		textX += 2
	}
	textY := int(leaderEnd.Y) -
		height*3/2 -
		datablockLineSpacing -
		verticalLeftOffset

	style := renderer.TextStyle{
		Size: settings.FontSize,
		Color: applyBrightness(
			renderer.RGB8(0, 208, 0),
			settings.Brightness,
			20,
		).ToRGBA(),
	}
	pos := redsmath.Vec2{X: float32(textX), Y: float32(textY)}

	for _, line := range block.lines {
		if strings.TrimSpace(line) != "" {
			td.AddText(line, pos, style)
		}
		pos.Y += float32(height + datablockLineSpacing)
	}
}

type DataBlockDrawOptions struct {
	Font *renderer.BitmapFont

	FontTextureForSize func(size int) renderer.TextureID

	SettingsForTarget       func(target *Target) DataBlockSettings
	ShowDataBlockForTarget  func(target *Target, settings DataBlockSettings) bool
	ShowBeaconCodeForTarget func(target *Target) bool
}

func DrawDatablocks(
	targets []*Target,
	cb *renderer.CmdBuffer,
	transforms radar.ScopeTransformations,
	opts DataBlockDrawOptions,
) {
	if cb == nil || opts.Font == nil || opts.FontTextureForSize == nil {
		return
	}

	transforms.LoadWindowViewingMatrices(cb)
	for _, target := range targets {
		if target == nil {
			continue
		}

		settings := DefaultDataBlockSettings()
		if opts.SettingsForTarget != nil {
			settings = opts.SettingsForTarget(target)
		}

		showDataBlock := target.EffectiveShowDB()
		if opts.ShowDataBlockForTarget != nil {
			showDataBlock = opts.ShowDataBlockForTarget(target, settings)
		}
		if !showDataBlock {
			continue
		}

		showBeaconCode := false
		if opts.ShowBeaconCodeForTarget != nil {
			showBeaconCode = opts.ShowBeaconCodeForTarget(target)
		}

		textureID := opts.FontTextureForSize(settings.FontSize)
		if textureID == 0 {
			continue
		}

		lineBuilder := renderer.GetLinesBuilder()
		td := renderer.GetTextDrawBuilder()
		td.SetFont(opts.Font)

		drawOneDataBlock(
			target,
			transforms.WindowFromWorldP(target.PosFeet),
			lineBuilder,
			td,
			opts.Font,
			settings,
			showBeaconCode,
		)

		cb.SetRGB(applyBrightness(
			renderer.RGB8(0, 208, 0),
			settings.Brightness,
			20,
		))
		cb.LineWidth(1)
		lineBuilder.GenerateCommands(cb)
		td.GenerateCommands(cb, textureID)

		renderer.ReturnTextDrawBuilder(td)
		renderer.ReturnLinesBuilder(lineBuilder)
	}
}

func clampInt(value, lo, hi int) int {
	if value < lo {
		return lo
	}
	if value > hi {
		return hi
	}
	return value
}
