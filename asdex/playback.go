package asdex

import "time"

const playbackMaxHourOffset = 4

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
		// Visual-only phase. Later this will start playback at the selected timestamp.
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
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
