package asdex

import (
	stdmath "math"
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	closedRunwayXAngleDeg         = 15.0
	closedRunwayBrightnessDefault = 95
)

type TempData struct {
	closedRunways map[string]bool
}

func NewTempData() TempData {
	return TempData{
		closedRunways: make(map[string]bool),
	}
}

func (td *TempData) DcbRunwayClosureStates(sl *SafetyLogic) []DcbRunwayClosureState {
	if td == nil || sl == nil {
		return nil
	}

	out := make([]DcbRunwayClosureState, 0, len(sl.runways))
	for _, rwy := range sl.runways {
		out = append(out, DcbRunwayClosureState{
			ID:       rwy.ID,
			IsClosed: td.closedRunways != nil && td.closedRunways[rwy.ID],
		})
	}
	return out
}

func (td *TempData) ToggleRunwayClosedByDcbIndex(sl *SafetyLogic, index int) bool {
	if td == nil || sl == nil || index < 1 || index > len(sl.runways) {
		return false
	}

	rwy := sl.runways[index-1]
	if rwy.ID == "" {
		return false
	}

	if td.closedRunways == nil {
		td.closedRunways = make(map[string]bool)
	}
	if td.closedRunways[rwy.ID] {
		delete(td.closedRunways, rwy.ID)
	} else {
		td.closedRunways[rwy.ID] = true
	}
	return true
}

func (td *TempData) RunwayClosed(id string) bool {
	if td == nil || td.closedRunways == nil {
		return false
	}
	id = strings.ToUpper(strings.TrimSpace(id))
	return td.closedRunways[id]
}

func (td *TempData) DrawClosedRunways(
	cb *renderer.CmdBuffer,
	sl *SafetyLogic,
	brightness int,
) {
	if td == nil || sl == nil || cb == nil || len(td.closedRunways) == 0 {
		return
	}

	builder := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(builder)

	for _, rwy := range sl.runways {
		if !td.closedRunways[rwy.ID] {
			continue
		}
		buildClosedRunwayXLines(builder, rwy)
	}

	cb.SetRGB(applyBrightness(renderer.RGB8(255, 255, 255), brightness, brightnessFloorDefault))
	cb.LineWidth(1)
	builder.GenerateCommands(cb)
}

func buildClosedRunwayXLines(
	builder *renderer.LinesBuilder,
	rwy surfaceRunway,
) {
	if builder == nil || rwy.LengthFeet <= 0 {
		return
	}

	c0 := runwayCorner(rwy, rwy.MinAlongFeet, rwy.MinAcrossFeet)
	c1 := runwayCorner(rwy, rwy.MaxAlongFeet, rwy.MinAcrossFeet)
	c2 := runwayCorner(rwy, rwy.MaxAlongFeet, rwy.MaxAcrossFeet)
	c3 := runwayCorner(rwy, rwy.MinAlongFeet, rwy.MaxAcrossFeet)

	angle := float32(closedRunwayXAngleDeg * stdmath.Pi / 180)
	addClosedXLine(builder, c0, closedXDirection(rwy, 1, 1, angle), c3, c2)
	addClosedXLine(builder, c1, closedXDirection(rwy, -1, 1, angle), c3, c2)
	addClosedXLine(builder, c2, closedXDirection(rwy, -1, -1, angle), c0, c1)
	addClosedXLine(builder, c3, closedXDirection(rwy, 1, -1, angle), c0, c1)
}

func closedXDirection(
	rwy surfaceRunway,
	alongSign float32,
	acrossSign float32,
	angleRad float32,
) redsmath.Vec2 {
	direction := rwy.AxisFeet.Mul(alongSign * float32(stdmath.Cos(float64(angleRad)))).
		Add(rwy.NormalFeet.Mul(acrossSign * float32(stdmath.Sin(float64(angleRad)))))
	normalized, ok := safetyNormalize(direction)
	if !ok {
		return redsmath.Vec2{}
	}
	return normalized
}

func runwayCorner(rwy surfaceRunway, along float32, across float32) redsmath.Vec2 {
	return rwy.CenterFeet.
		Add(rwy.AxisFeet.Mul(along)).
		Add(rwy.NormalFeet.Mul(across))
}

func addClosedXLine(
	builder *renderer.LinesBuilder,
	start redsmath.Vec2,
	dir redsmath.Vec2,
	edgeA redsmath.Vec2,
	edgeB redsmath.Vec2,
) {
	end, ok := intersectRaySegment(start, dir, edgeA, edgeB)
	if !ok {
		return
	}

	builder.AddLine(
		renderer.PointVertex{X: start.X, Y: start.Y},
		renderer.PointVertex{X: end.X, Y: end.Y},
	)
}

func intersectRaySegment(
	p redsmath.Vec2,
	r redsmath.Vec2,
	q redsmath.Vec2,
	sEnd redsmath.Vec2,
) (redsmath.Vec2, bool) {
	s := sEnd.Sub(q)
	denominator := cross2(r, s)
	if abs32(denominator) < degenerateRunwayAxisLength2 {
		return redsmath.Vec2{}, false
	}

	qmp := q.Sub(p)
	t := cross2(qmp, s) / denominator
	u := cross2(qmp, r) / denominator
	if t < 0 || u < 0 || u > 1 {
		return redsmath.Vec2{}, false
	}

	return p.Add(r.Mul(t)), true
}

func cross2(a, b redsmath.Vec2) float32 {
	return a.X*b.Y - a.Y*b.X
}

func (p *ASDEXPane) activateTempDataDcbHit(hit DcbHit) bool {
	if p == nil {
		return false
	}

	switch hit.Function {
	case DcbFunctionTempData:
		p.openTempDataDcbMenu()
		return true
	case DcbFunctionDone:
		p.closeDcbCurrentSubmenu()
		return true
	case DcbFunctionClosedRunway:
		p.openTempDataClosedRunwayDcbMenu()
		return true
	case DcbFunctionCloseRunway:
		if strings.TrimSpace(hit.Label) == "" {
			return true
		}
		p.tempData.ToggleRunwayClosedByDcbIndex(&p.safetyLogic, hit.ConfigID)
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true
	case DcbFunctionStoredGlobalTempData,
		DcbFunctionDefineClosedArea,
		DcbFunctionDefineRestrictedArea,
		DcbFunctionDefineTempText,
		DcbFunctionShowHiddenTempData,
		DcbFunctionHideTempData,
		DcbFunctionDeleteGlobalTempData:
		return true
	default:
		return false
	}
}

func (p *ASDEXPane) clearTempDataCommandConflicts() {
	if p == nil {
		return
	}

	p.dcbSpinner = nil
	p.commandEntry.Clear()
	p.datablockEdit = nil
	p.editingTargetID = ""
	p.initControlEntry = nil
	p.termControlEntry = nil
	p.multiFunction = nil
	p.previewReposition = nil
	p.coastListReposition = nil
	p.mapReposition = nil
	p.mapRotate = nil
}

func (p *ASDEXPane) openTempDataDcbMenu() {
	if p == nil {
		return
	}

	p.dcb.SetMenu(DcbMenuTempData)
	p.clearTempDataCommandConflicts()
	p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) openTempDataClosedRunwayDcbMenu() {
	if p == nil {
		return
	}

	p.dcb.SetMenu(DcbMenuClosedRunway)
	p.clearTempDataCommandConflicts()
	p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA", "CLOSED RUNWAY")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) closeDcbCurrentSubmenu() {
	if p == nil {
		return
	}

	switch p.dcb.Menu() {
	case DcbMenuClosedRunway:
		p.dcb.SetMenu(DcbMenuTempData)
		p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA")
	case DcbMenuTempData:
		p.dcb.SetMenu(DcbMenuMain)
		p.dcbMenuCommand = nil
	default:
		p.closeDcbSubmenu()
		return
	}

	p.dcbSpinner = nil
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) closeDcbSubmenu() {
	if p == nil {
		return
	}

	if p.dcb.Menu() != DcbMenuOff {
		p.dcb.SetMenu(DcbMenuMain)
	}
	p.dcbMenuCommand = nil
	p.dcbSpinner = nil
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) handleDcbMenuKeyboard(ctx *panes.Context) bool {
	if p == nil || p.dcbMenuCommand == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	if keyboard.WasPressed(platform.KeyEscape) ||
		keyboard.WasPressed(platform.KeyBackspace) ||
		keyboard.WasPressed(platform.KeyDelete) {
		p.closeDcbCurrentSubmenu()
		return true
	}

	return false
}
