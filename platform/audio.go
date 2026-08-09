package platform

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

const (
	audioSampleRate   = 44100
	audioChannelCount = 2
)

type PCM struct {
	Data []byte
}

type AudioPlayer struct {
	ctx   *oto.Context
	ready <-chan struct{}
}

type AudioPlayback struct {
	player *oto.Player
}

var (
	audioContextOnce  sync.Once
	audioContext      *oto.Context
	audioContextReady <-chan struct{}
	audioContextErr   error
)

func NewAudioPlayer() (*AudioPlayer, error) {
	audioContextOnce.Do(func() {
		audioContext, audioContextReady, audioContextErr = oto.NewContext(&oto.NewContextOptions{
			SampleRate:   audioSampleRate,
			ChannelCount: audioChannelCount,
			Format:       oto.FormatSignedInt16LE,
		})
	})
	if audioContextErr != nil {
		return nil, audioContextErr
	}
	return &AudioPlayer{
		ctx:   audioContext,
		ready: audioContextReady,
	}, nil
}

func (p *AudioPlayer) PlayPCM(data []byte) (*AudioPlayback, error) {
	if p == nil || p.ctx == nil {
		return nil, fmt.Errorf("audio player is not initialized")
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty PCM data")
	}
	if p.ready != nil {
		<-p.ready
	}

	player := p.ctx.NewPlayer(bytes.NewReader(data))
	player.Play()
	return &AudioPlayback{player: player}, nil
}

func (p *AudioPlayer) PlayWAV(data []byte) (*AudioPlayback, error) {
	pcm, err := DecodeWAV(data)
	if err != nil {
		return nil, err
	}
	return p.PlayPCM(pcm.Data)
}

func (p *AudioPlayback) IsPlaying() bool {
	return p != nil && p.player != nil && p.player.IsPlaying()
}

func (p *AudioPlayback) Wait() {
	for p.IsPlaying() {
		time.Sleep(10 * time.Millisecond)
	}
}

func (p *AudioPlayback) Close() error {
	if p == nil || p.player == nil {
		return nil
	}
	return p.player.Close()
}

func DecodeWAV(raw []byte) (PCM, error) {
	if len(raw) < 12 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return PCM{}, fmt.Errorf("not a RIFF/WAVE file")
	}

	var (
		audioFormat uint16
		channels    uint16
		sampleRate  uint32
		bits        uint16
		data        []byte
	)
	for offset := 12; offset+8 <= len(raw); {
		chunkID := string(raw[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(raw[offset+4 : offset+8]))
		offset += 8
		if chunkSize < 0 || offset+chunkSize > len(raw) {
			return PCM{}, fmt.Errorf("invalid WAV chunk")
		}

		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return PCM{}, fmt.Errorf("short fmt chunk")
			}
			audioFormat = binary.LittleEndian.Uint16(raw[offset : offset+2])
			channels = binary.LittleEndian.Uint16(raw[offset+2 : offset+4])
			sampleRate = binary.LittleEndian.Uint32(raw[offset+4 : offset+8])
			bits = binary.LittleEndian.Uint16(raw[offset+14 : offset+16])
		case "data":
			data = append([]byte(nil), raw[offset:offset+chunkSize]...)
		}

		offset += chunkSize
		if chunkSize&1 != 0 {
			offset++
		}
	}

	if len(data) == 0 {
		return PCM{}, fmt.Errorf("missing data chunk")
	}

	switch {
	case audioFormat == 1 && channels == audioChannelCount && sampleRate == audioSampleRate && bits == 16:
		return PCM{Data: data}, nil
	case audioFormat == 3 && channels == audioChannelCount && sampleRate == audioSampleRate && bits == 32:
		pcm, err := float32PCMToInt16(data)
		if err != nil {
			return PCM{}, err
		}
		return PCM{Data: pcm}, nil
	default:
		return PCM{}, fmt.Errorf("unsupported WAV format, expected PCM s16le or IEEE float32 stereo 44100Hz")
	}
}

func float32PCMToInt16(data []byte) ([]byte, error) {
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("invalid float32 PCM data length")
	}

	pcm := make([]byte, len(data)/2)
	for input, output := 0, 0; input < len(data); input, output = input+4, output+2 {
		sample := math.Float32frombits(binary.LittleEndian.Uint32(data[input : input+4]))
		if math.IsNaN(float64(sample)) {
			sample = 0
		}
		sample = clampFloat32(sample, -1, 1)

		scale := float32(32767)
		if sample < 0 {
			scale = 32768
		}
		scaled := int16(sample * scale)
		binary.LittleEndian.PutUint16(pcm[output:output+2], uint16(scaled))
	}
	return pcm, nil
}

func clampFloat32(value, minValue, maxValue float32) float32 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
