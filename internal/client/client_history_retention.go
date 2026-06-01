package client

import (
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func (cr *ClientRenderer) trimBaseHistoryAfterPaneOutputInfo(paneID uint32, info paneOutputRenderInfo) {
	if !info.hasScrollback {
		return
	}
	if !baseHistoryNeedsTrimForLiveScrollback(cr.loadState().baseHistory[paneID], info.scrollbackLen, cr.scrollbackLines) {
		return
	}
	cr.updateState(func(next *clientSnapshot) clientUIResult {
		setTrimmedBaseHistory(next.baseHistory, paneID, info.scrollbackLen, cr.scrollbackLines)
		return clientUIResult{}
	})
}

func (cr *ClientRenderer) trimBaseHistoryAfterPaneOutputInfos(infos map[uint32]paneOutputRenderInfo) {
	if len(infos) == 0 {
		return
	}
	state := cr.loadState()
	liveScrollback := make(map[uint32]int)
	for paneID, info := range infos {
		if !info.hasScrollback {
			continue
		}
		if !baseHistoryNeedsTrimForLiveScrollback(state.baseHistory[paneID], info.scrollbackLen, cr.scrollbackLines) {
			continue
		}
		liveScrollback[paneID] = info.scrollbackLen
	}
	if len(liveScrollback) == 0 {
		return
	}

	cr.updateState(func(next *clientSnapshot) clientUIResult {
		for paneID, scrollbackLen := range liveScrollback {
			setTrimmedBaseHistory(next.baseHistory, paneID, scrollbackLen, cr.scrollbackLines)
		}
		return clientUIResult{}
	})
}

func baseHistoryNeedsTrimForLiveScrollback(history []proto.StyledLine, liveScrollbackLen, scrollbackLimit int) bool {
	keep := baseHistoryKeepForLiveScrollback(liveScrollbackLen, scrollbackLimit)
	return len(history) > keep
}

func setTrimmedBaseHistory(histories map[uint32][]proto.StyledLine, paneID uint32, liveScrollbackLen, scrollbackLimit int) {
	history := histories[paneID]
	keep := baseHistoryKeepForLiveScrollback(liveScrollbackLen, scrollbackLimit)
	if len(history) <= keep {
		return
	}
	if keep == 0 {
		delete(histories, paneID)
		return
	}
	histories[paneID] = append([]proto.StyledLine(nil), history[len(history)-keep:]...)
}

func baseHistoryKeepForLiveScrollback(liveScrollbackLen, scrollbackLimit int) int {
	if scrollbackLimit <= 0 {
		scrollbackLimit = mux.DefaultScrollbackLines
	}
	if liveScrollbackLen < 0 {
		liveScrollbackLen = 0
	}
	keep := scrollbackLimit - liveScrollbackLen
	if keep < 0 {
		return 0
	}
	return keep
}
