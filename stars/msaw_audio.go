package stars

import (
	"context"
	"log/slog"
	"sync"
	"time"

	redslog "github.com/juliusplatzer/reds/log"
	redsnet "github.com/juliusplatzer/reds/net"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/util"
)

// TI 6191.409 Rev. 30 §2.16.3: the adapted audible LA alarm sounds for
// nominally ten seconds, or until acknowledgment. Unlike VICE's five-second
// simulation setting, this is the manual's nominal interval. Changing the
// interval later should be a site adaptation, not a change to detection.
const msawAudioDuration = 10 * time.Second

const msawAudioResource = "resources/audio/stars/Msaw.wav"

// msawAudioManager owns one repeating MSAW alarm per local TCW/TDW. The
// half-second resource is repeated on a goroutine, never on the draw thread.
// Only a short PCM sample is retained; no per-target audio player is created.
type msawAudioManager struct {
	player *platform.AudioPlayer
	pcm    []byte
	logger *redslog.Logger

	mu       sync.Mutex
	cancel   context.CancelFunc
	disabled bool
}

func newMSAWAudioManager(logger *redslog.Logger) *msawAudioManager {
	if !util.ResourceExists(msawAudioResource) {
		if logger != nil {
			logger.Warn("STARS MSAW sound unavailable", slog.String("resource", msawAudioResource))
		}
		return nil
	}
	pcm, err := platform.DecodeWAV(util.LoadResourceBytes(msawAudioResource))
	if err != nil {
		if logger != nil {
			logger.Warn("STARS MSAW sound cannot be decoded", slog.String("resource", msawAudioResource), slog.Any("error", err))
		}
		return nil
	}
	player, err := platform.NewAudioPlayer()
	if err != nil {
		if logger != nil {
			logger.Warn("STARS MSAW audio output unavailable", slog.Any("error", err))
		}
		return nil
	}
	return &msawAudioManager{player: player, pcm: pcm.Data, logger: logger}
}

// SetPlaying is idempotent. Switching off cancels playback promptly (without
// waiting for the current WAV to finish), including when an LA is acknowledged.
func (m *msawAudioManager) SetPlaying(wanted bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !wanted {
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		return
	}
	if m.disabled || m.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	go m.repeat(ctx)
}

func (m *msawAudioManager) Stop() { m.SetPlaying(false) }

func (m *msawAudioManager) repeat(ctx context.Context) {
	// Playback must not allocate or hold a goroutine for each active aircraft.
	// A single repeated sample covers any concurrent unacknowledged LAs.
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		playback, err := m.player.PlayPCM(m.pcm)
		if err != nil {
			m.mu.Lock()
			m.disabled = true // avoid retrying every rendered frame
			m.mu.Unlock()
			if m.logger != nil {
				m.logger.Warn("STARS MSAW audio playback failed", slog.Any("error", err))
			}
			return
		}
		for playback.IsPlaying() && ctx.Err() == nil {
			select {
			case <-ctx.Done():
			case <-ticker.C:
			}
		}
		_ = playback.Close()
	}
}

// msawAudioWanted checks the effective local LA presentation, not merely the
// raw below-MVA condition. Q/V inhibits, automatic VFR inhibition and a
// controller's slew-click acknowledgment therefore immediately silence audio.
// A later independent LA starts its own nominal ten-second interval.
func (p *STARSPane) msawAudioWanted(snapshot redsnet.TaisSnapshot, now time.Time) bool {
	if p == nil || !snapshot.Ready || p.msawAirspace == nil {
		return false
	}
	for i := range snapshot.Targets {
		state, active := p.msawAlert(&snapshot.Targets[i])
		if active && !state.Acknowledged && !now.Before(state.Since) && now.Before(state.Since.Add(msawAudioDuration)) {
			return true
		}
	}
	return false
}

func (p *STARSPane) updateMSAWAudio(snapshot redsnet.TaisSnapshot, now time.Time) {
	if p != nil {
		p.msawAudio.SetPlaying(p.msawAudioWanted(snapshot, now))
	}
}
