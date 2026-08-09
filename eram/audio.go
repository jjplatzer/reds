package eram

import (
	"log/slog"

	redslog "github.com/juliusplatzer/reds/log"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/util"
)

type eramAudioEvent uint8

const (
	eramAudioError eramAudioEvent = iota
)

type eramAudioManager struct {
	player *platform.AudioPlayer
	sounds map[eramAudioEvent][]byte
	logger *redslog.Logger
}

func newERAMAudioManager(logger *redslog.Logger) *eramAudioManager {
	m := &eramAudioManager{
		sounds: make(map[eramAudioEvent][]byte),
		logger: logger,
	}

	m.loadSound(eramAudioError, "Error.wav")

	player, err := platform.NewAudioPlayer()
	if err != nil {
		m.warn("ERAM audio disabled", "", err)
		return m
	}
	m.player = player
	return m
}

func (m *eramAudioManager) loadSound(event eramAudioEvent, name string) {
	path := "resources/audio/eram/" + name
	pcm, err := platform.DecodeWAV(util.LoadResourceBytes(path))
	if err != nil {
		m.warn("ERAM sound disabled", name, err)
		return
	}
	m.sounds[event] = pcm.Data
}

func (m *eramAudioManager) warn(message, sound string, err error) {
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

func (m *eramAudioManager) Play(event eramAudioEvent) {
	if m == nil || m.player == nil {
		return
	}
	data := m.sounds[event]
	if len(data) == 0 {
		return
	}

	// Individual ERAM UI sounds are one-shot effects and may overlap.
	go func() {
		playback, err := m.player.PlayPCM(data)
		if err != nil {
			m.warn("ERAM sound playback failed", "", err)
			return
		}
		playback.Wait()
		_ = playback.Close()
	}()
}
