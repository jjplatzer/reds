package asdex

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/util"

	"github.com/ebitengine/oto/v3"
)

const (
	alertMessageMaxAlerts = 5
	alertMessageFontSize  = 2
	alertMessageLineSpace = 2
	alertMessagePadding   = 2
	alertMessageBorderPx  = 2
)

var (
	alertMessageTextRGB   = renderer.RGB8(0, 248, 0)
	alertMessageBorderRGB = renderer.RGB8(255, 0, 0)
)

type AlertRepository struct {
	alerts []SafetyAlert
	aural  *AuralAlertManager
}

func NewAlertRepository(aural *AuralAlertManager) AlertRepository {
	return AlertRepository{aural: aural}
}

func (r *AlertRepository) ApplyChanges(
	changes SafetyAlertChanges,
	targetAlertsInhibited func(string) bool,
) {
	if r == nil || changes.Empty() {
		return
	}

	for _, id := range changes.Deleted {
		r.Delete(id)
	}
	for _, alert := range changes.Upserted {
		if safetyAlertInvolvesInhibitedTarget(alert, targetAlertsInhibited) {
			r.Delete(alert.ID)
			continue
		}
		r.Upsert(alert)
	}
}

func (r *AlertRepository) Upsert(alert SafetyAlert) {
	if r == nil || alert.ID == "" {
		return
	}

	for i := range r.alerts {
		if r.alerts[i].ID == alert.ID {
			r.alerts[i] = alert
			return
		}
	}

	r.alerts = append(r.alerts, alert)
	if alert.PlayAuralAlert && r.aural != nil {
		r.aural.Play(alert.AuralAlerts)
	}
}

func (r *AlertRepository) Delete(id string) {
	if r == nil || id == "" {
		return
	}

	out := r.alerts[:0]
	for _, alert := range r.alerts {
		if alert.ID != id {
			out = append(out, alert)
		}
	}
	r.alerts = out
}

func (r *AlertRepository) DeleteForAircraft(targetID string) {
	targetID = strings.TrimSpace(targetID)
	if r == nil || targetID == "" {
		return
	}

	out := r.alerts[:0]
	for _, alert := range r.alerts {
		if !safetyAlertInvolvesTarget(alert, targetID) {
			out = append(out, alert)
		}
	}
	r.alerts = out
}

func (r *AlertRepository) ClearInhibitedAircraft(
	targetAlertsInhibited func(string) bool,
) {
	if r == nil || targetAlertsInhibited == nil {
		return
	}

	out := r.alerts[:0]
	for _, alert := range r.alerts {
		if !safetyAlertInvolvesInhibitedTarget(alert, targetAlertsInhibited) {
			out = append(out, alert)
		}
	}
	r.alerts = out
}

func (r *AlertRepository) All() []SafetyAlert {
	if r == nil || len(r.alerts) == 0 {
		return nil
	}
	return append([]SafetyAlert(nil), r.alerts...)
}

func (r *AlertRepository) FirstN(n int) []SafetyAlert {
	if r == nil || n <= 0 || len(r.alerts) == 0 {
		return nil
	}
	if len(r.alerts) < n {
		n = len(r.alerts)
	}
	return append([]SafetyAlert(nil), r.alerts[:n]...)
}

func (r *AlertRepository) AlertInProgress() bool {
	return r != nil && len(r.alerts) > 0
}

func (r *AlertRepository) AircraftIsInAlert(targetID string) bool {
	if r == nil || targetID == "" {
		return false
	}

	for _, alert := range r.alerts {
		for _, id := range alert.AircraftIDs {
			if id == targetID {
				return true
			}
		}
	}
	return false
}

func (r *AlertRepository) AircraftIDsInAlertSet() map[string]bool {
	out := make(map[string]bool)
	if r == nil {
		return out
	}

	for _, alert := range r.alerts {
		for _, id := range alert.AircraftIDs {
			id = strings.TrimSpace(id)
			if id != "" {
				out[id] = true
			}
		}
	}
	return out
}

func safetyAlertInvolvesInhibitedTarget(
	alert SafetyAlert,
	targetAlertsInhibited func(string) bool,
) bool {
	if targetAlertsInhibited == nil {
		return false
	}

	for _, id := range alert.AircraftIDs {
		if targetAlertsInhibited(strings.TrimSpace(id)) {
			return true
		}
	}
	return false
}

func safetyAlertInvolvesTarget(alert SafetyAlert, targetID string) bool {
	for _, id := range alert.AircraftIDs {
		if strings.TrimSpace(id) == targetID {
			return true
		}
	}
	return false
}

func alertFlashOn(now time.Time) bool {
	if now.IsZero() {
		return true
	}

	// CRC toggles alert aircraft symbol color once per second.
	return now.Unix()%2 == 0
}

type AlertMessageBox struct {
	location RelativeScreenLocation
	list     ScreenList
}

func NewAlertMessageBox() AlertMessageBox {
	size := redsmath.Vec2{X: 300, Y: 500}
	defaultDisplay := redsmath.Vec2{X: 1300, Y: 900}
	topLeft := redsmath.Vec2{X: 300, Y: 150}

	return AlertMessageBox{
		location: RelativeScreenLocationFromTopLeft(topLeft, size, defaultDisplay),
		list: NewScreenList(ScreenListStyle{
			Location:       topLeft,
			RepositionSize: size,

			FontSize:      alertMessageFontSize,
			Brightness:    brightnessDefault,
			MinBrightness: brightnessFloorDefault,
			LineSpacing:   alertMessageLineSpace,

			BaseTextColor: alertMessageTextRGB,
		}),
	}
}

func (b *AlertMessageBox) SetLocation(pos redsmath.Vec2, displaySize redsmath.Vec2) {
	if b == nil {
		return
	}
	b.location = RelativeScreenLocationFromTopLeft(pos, b.list.style.RepositionSize, displaySize)
	b.list.SetLocation(pos)
}

func (b *AlertMessageBox) LocationForDisplay(displaySize redsmath.Vec2) redsmath.Vec2 {
	if b == nil {
		return redsmath.Vec2{}
	}
	return b.location.Location(displaySize, b.list.style.RepositionSize)
}

func (b *AlertMessageBox) RepositionSize() redsmath.Vec2 {
	if b == nil {
		return redsmath.Vec2{}
	}
	return b.list.style.RepositionSize
}

func (b *AlertMessageBox) SetBrightness(brightness int) {
	if b == nil {
		return
	}
	b.list.SetBrightness(brightness)
}

func (b *AlertMessageBox) Render(
	cb *renderer.CmdBuffer,
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	alerts []SafetyAlert,
	displaySize redsmath.Vec2,
) {
	if b == nil || cb == nil || td == nil || font == nil || len(alerts) == 0 {
		return
	}

	if len(alerts) > alertMessageMaxAlerts {
		alerts = alerts[:alertMessageMaxAlerts]
	}

	location := b.LocationForDisplay(displaySize)
	b.list.SetLocation(location)

	textPos := redsmath.Vec2{
		X: location.X + alertMessagePadding,
		Y: location.Y + alertMessagePadding,
	}

	boxWidth := float32(4)
	boxHeight := float32(4)

	for _, alert := range alerts {
		block := alertMessageBlock(alert)
		blockWidth, blockHeight := measureAlertMessageBlock(font, alertMessageFontSize, block)

		itemHeight := float32(blockHeight + 4)
		if w := float32(blockWidth + 4); w > boxWidth {
			boxWidth = w
		}
		boxHeight += itemHeight

		oldLocation := b.list.style.Location
		b.list.style.Location = textPos
		b.list.Render(td, font, block)
		b.list.style.Location = oldLocation

		textPos.Y += itemHeight
	}

	boxHeight -= 4
	renderAlertBorder(cb, redsmath.NewRect(
		location.X,
		location.Y,
		location.X+boxWidth,
		location.Y+boxHeight,
	))
}

func alertMessageBlock(alert SafetyAlert) TextBlock {
	block := TextBlock{LineSpacing: alertMessageLineSpace}
	for _, line := range alert.MessageLines {
		block.Fragments = append(block.Fragments, TextFragment{
			Text:       strings.TrimRight(line, "\r\n"),
			Foreground: alertMessageTextRGB,
			NewLine:    true,
		})
	}
	return block
}

func measureAlertMessageBlock(
	font *renderer.BitmapFont,
	fontSize int,
	block TextBlock,
) (width int, height int) {
	if font == nil {
		return 0, 0
	}

	lineHeight := font.LineHeight(fontSize)
	spacing := block.LineSpacing
	if spacing <= 0 {
		spacing = alertMessageLineSpace
	}

	currentWidth := 0
	maxWidth := 0
	lines := 0
	for _, fragment := range block.Fragments {
		if fragment.Text != "" {
			w, _ := font.MeasureText(fragment.Text, fontSize)
			currentWidth += w
		}
		if fragment.NewLine {
			if currentWidth > maxWidth {
				maxWidth = currentWidth
			}
			currentWidth = 0
			lines++
		}
	}
	if currentWidth > 0 {
		if currentWidth > maxWidth {
			maxWidth = currentWidth
		}
		lines++
	}
	if lines <= 0 {
		return maxWidth, 0
	}

	return maxWidth, lines*lineHeight + (lines-1)*spacing
}

func renderAlertBorder(cb *renderer.CmdBuffer, rect redsmath.Rect) {
	if cb == nil || rect.Empty() {
		return
	}

	builder := renderer.GetColoredTrianglesBuilder()
	defer renderer.ReturnColoredTrianglesBuilder(builder)

	addWindowBorderRect(builder, rect, alertMessageBorderPx, alertMessageBorderRGB)
	builder.GenerateCommands(cb)
}

type AuralAlertManager struct {
	ctx   *oto.Context
	ready chan struct{}

	sounds map[SafetyAuralAlert][]byte
	queue  []SafetyAuralAlert

	current *oto.Player
	playing bool
	volume  int
	mu      sync.Mutex
}

func NewAuralAlertManager() *AuralAlertManager {
	manager := &AuralAlertManager{
		sounds: make(map[SafetyAuralAlert][]byte),
		volume: defaultAuralVolume,
	}
	manager.loadSounds()

	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   44100,
		ChannelCount: 2,
		Format:       oto.FormatSignedInt16LE,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "asdex aural alerts disabled: %v\n", err)
		return manager
	}

	manager.ctx = ctx
	manager.ready = ready
	return manager
}

func (m *AuralAlertManager) loadSounds() {
	if m == nil {
		return
	}

	for _, alert := range []SafetyAuralAlert{
		SafetyAuralWarning,
		SafetyAuralRunway,
		SafetyAuralZero,
		SafetyAuralOne,
		SafetyAuralTwo,
		SafetyAuralThree,
		SafetyAuralFour,
		SafetyAuralFive,
		SafetyAuralSix,
		SafetyAuralSeven,
		SafetyAuralEight,
		SafetyAuralNine,
		SafetyAuralLeft,
		SafetyAuralRight,
		SafetyAuralCenter,
		SafetyAuralClosed,
	} {
		name := safetyAuralAlertResourceName(alert)
		if name == "" {
			continue
		}

		path := "resources/audio/asdex/" + name
		if !util.ResourceExists(path) {
			fmt.Fprintf(os.Stderr, "asdex aural alert %s disabled: missing resource\n", name)
			continue
		}

		data, err := loadAuralPCM(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "asdex aural alert %s disabled: %v\n", name, err)
			continue
		}
		m.sounds[alert] = data
	}
}

func safetyAuralAlertResourceName(alert SafetyAuralAlert) string {
	switch alert {
	case SafetyAuralWarning:
		return "Warning.wav"
	case SafetyAuralRunway:
		return "Runway.wav"
	case SafetyAuralZero:
		return "Zero.wav"
	case SafetyAuralOne:
		return "One.wav"
	case SafetyAuralTwo:
		return "Two.wav"
	case SafetyAuralThree:
		return "Three.wav"
	case SafetyAuralFour:
		return "Four.wav"
	case SafetyAuralFive:
		return "Five.wav"
	case SafetyAuralSix:
		return "Six.wav"
	case SafetyAuralSeven:
		return "Seven.wav"
	case SafetyAuralEight:
		return "Eight.wav"
	case SafetyAuralNine:
		return "Nine.wav"
	case SafetyAuralLeft:
		return "Left.wav"
	case SafetyAuralRight:
		return "Right.wav"
	case SafetyAuralCenter:
		return "Center.wav"
	case SafetyAuralClosed:
		return "Closed.wav"
	default:
		return ""
	}
}

func loadAuralPCM(path string) ([]byte, error) {
	raw := util.LoadResourceBytes(path)
	if len(raw) < 12 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a RIFF/WAVE file")
	}

	offset := 12
	formatOK := false
	var data []byte

	for offset+8 <= len(raw) {
		chunkID := string(raw[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(raw[offset+4 : offset+8]))
		offset += 8
		if chunkSize < 0 || offset+chunkSize > len(raw) {
			return nil, fmt.Errorf("invalid WAV chunk")
		}

		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, fmt.Errorf("short fmt chunk")
			}
			audioFormat := binary.LittleEndian.Uint16(raw[offset : offset+2])
			channels := binary.LittleEndian.Uint16(raw[offset+2 : offset+4])
			sampleRate := binary.LittleEndian.Uint32(raw[offset+4 : offset+8])
			bits := binary.LittleEndian.Uint16(raw[offset+14 : offset+16])
			formatOK = audioFormat == 1 &&
				channels == 2 &&
				sampleRate == 44100 &&
				bits == 16
		case "data":
			data = append([]byte(nil), raw[offset:offset+chunkSize]...)
		}

		offset += chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}

	if !formatOK {
		return nil, fmt.Errorf("unsupported WAV format, expected PCM s16le stereo 44100Hz")
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("missing data chunk")
	}
	return data, nil
}

func (m *AuralAlertManager) Play(alerts []SafetyAuralAlert) {
	if m == nil || len(alerts) == 0 || m.ctx == nil {
		return
	}

	m.mu.Lock()
	m.queue = append(m.queue, alerts...)
	if m.playing {
		m.mu.Unlock()
		return
	}
	m.playing = true
	m.mu.Unlock()

	go m.playLoop()
}

func (m *AuralAlertManager) playLoop() {
	if m == nil || m.ctx == nil {
		return
	}

	if m.ready != nil {
		<-m.ready
	}

	for {
		m.mu.Lock()
		if len(m.queue) == 0 {
			m.playing = false
			m.current = nil
			m.mu.Unlock()
			return
		}

		alert := m.queue[0]
		m.queue = m.queue[1:]
		data := append([]byte(nil), m.sounds[alert]...)
		volume := m.volume
		m.mu.Unlock()

		if len(data) == 0 {
			continue
		}

		data = applyPCMVolume(data, volume)
		player := m.ctx.NewPlayer(bytes.NewReader(data))

		m.mu.Lock()
		m.current = player
		m.mu.Unlock()

		player.Play()
		for player.IsPlaying() {
			time.Sleep(10 * time.Millisecond)
		}
		_ = player.Close()

		m.mu.Lock()
		if m.current == player {
			m.current = nil
		}
		m.mu.Unlock()
	}
}

func applyPCMVolume(pcm []byte, volume int) []byte {
	volume = clampInt(volume, minAuralVolume, maxAuralVolume)
	if volume >= maxAuralVolume {
		return pcm
	}

	scale := float64(volume) / float64(maxAuralVolume)
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(binary.LittleEndian.Uint16(pcm[i : i+2]))
		scaled := int16(float64(sample) * scale)
		binary.LittleEndian.PutUint16(pcm[i:i+2], uint16(scaled))
	}
	return pcm
}

func (m *AuralAlertManager) Stop() {
	if m == nil {
		return
	}

	m.mu.Lock()
	m.queue = nil
	current := m.current
	m.mu.Unlock()

	if current != nil {
		_ = current.Close()
	}
}

func (m *AuralAlertManager) SetVolume(volume int) {
	if m == nil {
		return
	}

	m.mu.Lock()
	m.volume = clampInt(volume, minAuralVolume, maxAuralVolume)
	m.mu.Unlock()
}

func (m *AuralAlertManager) IsPlaying() bool {
	if m == nil {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.playing
}
