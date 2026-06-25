package asdex

import (
	"context"
	"sort"
	"time"

	redsnet "github.com/juliusplatzer/reds/net"
)

const playbackMaxHourOffset = 4

type PlaybackSession struct {
	Active  bool
	Loading bool

	Airport string
	Start   time.Time
	End     time.Time

	WallStart time.Time
	SimTime   time.Time

	Frames []redsnet.SmesFrame
	Times  []time.Time
	Next   int

	Error string
}

type PlaybackLoadResult struct {
	Seq       uint64
	Airport   string
	Start     time.Time
	End       time.Time
	Bootstrap redsnet.PlaybackBootstrapResponse
	Frames    []redsnet.SmesFrame
	Times     []time.Time
	Err       error
}

func (p *ASDEXPane) openPlayBackMenu() {
	if p == nil {
		return
	}

	p.playbackHourOffset = clampInt(p.playbackHourOffset, 0, playbackMaxHourOffset)
	p.dcb.SetMenu(DcbMenuPlayBack)
	p.updatePlayBackMenuCommand()
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) updatePlayBackMenuCommand() {
	if p == nil {
		return
	}

	hour := p.playbackHourStart()
	p.dcbMenuCommand = NewDcbMenuCommand(
		"PLAY BACK",
		hour.Format("15:00")+"-"+hour.Add(55*time.Minute).Format("15:04"),
	)
}

func (p *ASDEXPane) activatePlayBackDcbHit(hit DcbHit) bool {
	if p == nil {
		return false
	}

	switch hit.Function {
	case DcbFunctionPlayBackTimeBlock:
		p.startPlaybackBlock(hit.ConfigID)
		return true

	case DcbFunctionPlayBackPrevHour:
		p.playbackHourOffset = clampInt(p.playbackHourOffset+1, 0, playbackMaxHourOffset)
		p.updatePlayBackMenuCommand()
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true

	case DcbFunctionPlayBackNextHour:
		p.playbackHourOffset = clampInt(p.playbackHourOffset-1, 0, playbackMaxHourOffset)
		p.updatePlayBackMenuCommand()
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true

	default:
		return false
	}
}

func (p *ASDEXPane) startPlaybackBlock(blockIndex int) {
	if p == nil || p.playbackClient == nil {
		return
	}

	start := p.playbackSelectedTime(blockIndex)
	end := start.Add(5 * time.Minute)

	p.playbackLoadSeq++
	seq := p.playbackLoadSeq

	p.playbackSession = &PlaybackSession{
		Loading: true,
		Airport: p.airport,
		Start:   start,
		End:     end,
	}

	if p.smes != nil {
		p.smes.SetAirport("")
	}
	p.discardNetworkEvents()

	p.previewArea.SetSystemResponse("PLAYBACK LOAD")
	p.clearHighlightedTarget()

	go p.loadPlaybackBlock(seq, p.airport, start, end)
}

func (p *ASDEXPane) loadPlaybackBlock(seq uint64, airport string, start, end time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bootstrap, err := p.playbackClient.Bootstrap(ctx, airport, start)
	if err != nil {
		p.sendPlaybackResult(PlaybackLoadResult{
			Seq:     seq,
			Airport: airport,
			Start:   start,
			End:     end,
			Err:     err,
		})
		return
	}

	frames, err := p.playbackClient.Range(ctx, airport, start, end)
	if err != nil {
		p.sendPlaybackResult(PlaybackLoadResult{
			Seq:     seq,
			Airport: airport,
			Start:   start,
			End:     end,
			Err:     err,
		})
		return
	}

	frames, times := sortPlaybackFrames(frames)

	p.sendPlaybackResult(PlaybackLoadResult{
		Seq:       seq,
		Airport:   airport,
		Start:     start,
		End:       end,
		Bootstrap: bootstrap,
		Frames:    frames,
		Times:     times,
	})
}

func (p *ASDEXPane) sendPlaybackResult(result PlaybackLoadResult) {
	if p == nil {
		return
	}

	select {
	case p.playbackResults <- result:
	default:
		select {
		case <-p.playbackResults:
		default:
		}
		select {
		case p.playbackResults <- result:
		default:
		}
	}
}

func (p *ASDEXPane) consumePlaybackResults() {
	if p == nil || p.playbackResults == nil {
		return
	}

	for {
		select {
		case result := <-p.playbackResults:
			p.applyPlaybackLoadResult(result)
		default:
			return
		}
	}
}

func (p *ASDEXPane) applyPlaybackLoadResult(result PlaybackLoadResult) {
	if p == nil || result.Seq != p.playbackLoadSeq {
		return
	}

	if result.Err != nil {
		p.playbackSession = nil
		if p.smes != nil {
			p.smes.SetAirport(p.airport)
		}
		p.previewArea.SetSystemResponse("PLAYBACK FAIL")
		return
	}

	if p.smes != nil {
		p.smes.SetAirport("")
	}
	p.discardNetworkEvents()

	p.targets.Clear()
	clear(p.showBeaconUntilByTargetID)

	for key, changed := range result.Bootstrap.Targets {
		frame := redsnet.SmesFrame{
			Key:       key,
			Airport:   result.Airport,
			UpdatedAt: result.Start.Format(time.RFC3339Nano),
			IsFull:    true,
			Changed:   changed,
		}
		p.targets.ApplySmesFrame(frame, p.videomap)
	}

	p.playbackSession = &PlaybackSession{
		Active:    true,
		Airport:   result.Airport,
		Start:     result.Start,
		End:       result.End,
		WallStart: time.Now().UTC(),
		SimTime:   result.Start,
		Frames:    result.Frames,
		Times:     result.Times,
		Next:      0,
	}

	for p.playbackSession.Next < len(p.playbackSession.Times) &&
		!p.playbackSession.Times[p.playbackSession.Next].After(result.Start) {
		p.playbackSession.Next++
	}

	p.previewArea.SetSystemResponse("PLAYBACK ACTIVE")
}

func (p *ASDEXPane) playbackActiveOrLoading() bool {
	return p != nil &&
		p.playbackSession != nil &&
		(p.playbackSession.Active || p.playbackSession.Loading)
}

func (p *ASDEXPane) advancePlayback(now time.Time) {
	if p == nil || p.playbackSession == nil || !p.playbackSession.Active {
		return
	}

	session := p.playbackSession
	elapsed := now.Sub(session.WallStart)
	if elapsed < 0 {
		elapsed = 0
	}

	session.SimTime = session.Start.Add(elapsed)

	for session.Next < len(session.Frames) &&
		!session.Times[session.Next].After(session.SimTime) {
		frame := session.Frames[session.Next]
		p.targets.ApplySmesFrame(frame, p.videomap)
		session.Next++
	}

	if !session.SimTime.Before(session.End) {
		p.finishPlayback()
	}
}

func (p *ASDEXPane) finishPlayback() {
	if p == nil {
		return
	}

	p.playbackSession = nil
	p.targets.Clear()
	clear(p.showBeaconUntilByTargetID)

	if p.smes != nil {
		p.smes.SetAirport(p.airport)
	}

	p.previewArea.SetSystemResponse("PLAYBACK END")
}

func (p *ASDEXPane) discardNetworkEvents() {
	if p == nil || p.smes == nil {
		return
	}

	for {
		select {
		case <-p.smes.Status():
		case <-p.smes.Frames():
		default:
			return
		}
	}
}

func sortPlaybackFrames(frames []redsnet.SmesFrame) ([]redsnet.SmesFrame, []time.Time) {
	type item struct {
		frame redsnet.SmesFrame
		t     time.Time
	}

	items := make([]item, 0, len(frames))
	for _, frame := range frames {
		t, err := time.Parse(time.RFC3339Nano, frame.UpdatedAt)
		if err != nil {
			continue
		}
		items = append(items, item{frame: frame, t: t})
	}

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].t.Before(items[j].t)
	})

	outFrames := make([]redsnet.SmesFrame, len(items))
	outTimes := make([]time.Time, len(items))
	for i, item := range items {
		outFrames[i] = item.frame
		outTimes[i] = item.t
	}

	return outFrames, outTimes
}

func (p *ASDEXPane) playbackSelectedTime(blockIndex int) time.Time {
	if p == nil {
		return time.Now().UTC().Truncate(time.Hour)
	}

	blockIndex = clampInt(blockIndex, 0, 11)
	return p.playbackHourStart().Add(time.Duration(blockIndex*5) * time.Minute)
}

func (p *ASDEXPane) playbackHourStart() time.Time {
	offset := 0
	if p != nil {
		offset = clampInt(p.playbackHourOffset, 0, playbackMaxHourOffset)
	}

	return time.Now().UTC().
		Truncate(time.Hour).
		Add(-time.Duration(offset) * time.Hour)
}
