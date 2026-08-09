package eram

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
	"sync"
	"time"

	redslog "github.com/juliusplatzer/reds/log"
	"github.com/juliusplatzer/reds/util"

	"github.com/ebitengine/oto/v3"
)

type eramSoundEvent uint8

const (
	eramSoundError eramSoundEvent = iota
)

// ERAM's exported CRC sounds are 44.1 kHz, stereo, signed 16-bit PCM. Keep a
// single Oto context for ERAM panes; individual one-shot players are cheap and
// may overlap, matching CRC SoundManager.Play for non-looping sounds.
var (
	eramOtoOnce  sync.Once
	eramOtoCtx   *oto.Context
	eramOtoReady <-chan struct{}
	eramOtoErr   error
)

type eramSoundManager struct {
	ctx    *oto.Context
	ready  <-chan struct{}
	sounds map[eramSoundEvent][]byte
	logger *redslog.Logger
}

func newERAMSoundManager(logger *redslog.Logger) *eramSoundManager {
	m := &eramSoundManager{
		sounds: make(map[eramSoundEvent][]byte),
		logger: logger,
	}

	if data, err := loadERAMPCM("resources/audio/eram/Error.wav"); err != nil {
		m.warn("ERAM sound disabled", "Error.wav", err)
	} else {
		m.sounds[eramSoundError] = data
	}

	eramOtoOnce.Do(func() {
		eramOtoCtx, eramOtoReady, eramOtoErr = oto.NewContext(&oto.NewContextOptions{
			SampleRate:   44100,
			ChannelCount: 2,
			Format:       oto.FormatSignedInt16LE,
		})
	})
	if eramOtoErr != nil {
		m.warn("ERAM audio disabled", "", eramOtoErr)
		return m
	}
	m.ctx = eramOtoCtx
	m.ready = eramOtoReady
	return m
}

func (m *eramSoundManager) warn(message, sound string, err error) {
	attrs := []any{slog.Any("error", err)}
	if sound != "" {
		attrs = append([]any{slog.String("sound", sound)}, attrs...)
	}
	if m != nil && m.logger != nil {
		m.logger.Warn(message, attrs...)
		return
	}
	slog.Warn(message, attrs...)
}

func (m *eramSoundManager) Play(event eramSoundEvent) {
	if m == nil || m.ctx == nil {
		return
	}
	data := m.sounds[event]
	if len(data) == 0 {
		return
	}
	// The player owns this reader until playback completes; the PCM slice is
	// immutable after load, so no per-click audio copy is required.
	go func() {
		if m.ready != nil {
			<-m.ready
		}
		player := m.ctx.NewPlayer(bytes.NewReader(data))
		player.Play()
		for player.IsPlaying() {
			time.Sleep(10 * time.Millisecond)
		}
		_ = player.Close()
	}()
}

func loadERAMPCM(path string) ([]byte, error) {
	raw := util.LoadResourceBytes(path)
	if len(raw) < 12 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a RIFF/WAVE file")
	}

	var (
		audioFormat uint16
		channels    uint16
		sampleRate  uint32
		bits        uint16
		pcm         []byte
	)
	for offset := 12; offset+8 <= len(raw); {
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
			audioFormat = binary.LittleEndian.Uint16(raw[offset : offset+2])
			channels = binary.LittleEndian.Uint16(raw[offset+2 : offset+4])
			sampleRate = binary.LittleEndian.Uint32(raw[offset+4 : offset+8])
			bits = binary.LittleEndian.Uint16(raw[offset+14 : offset+16])
		case "data":
			pcm = append([]byte(nil), raw[offset:offset+chunkSize]...)
		}

		offset += chunkSize
		if chunkSize&1 != 0 {
			offset++
		}
	}

	if audioFormat != 1 || channels != 2 || sampleRate != 44100 || bits != 16 {
		return nil, fmt.Errorf("unsupported WAV format, expected PCM s16le stereo 44100Hz")
	}
	if len(pcm) == 0 {
		return nil, fmt.Errorf("missing data chunk")
	}
	return pcm, nil
}
