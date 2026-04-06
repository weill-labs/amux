package commands

import (
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type Result struct {
	Output          string
	Err             error
	BroadcastLayout bool
	PaneHistories   []PaneHistoryUpdate
	PaneRenders     []PaneRender
	StartPanes      []*mux.Pane
	ClosePanes      []*mux.Pane
	SendExit        bool
	ShutdownServer  bool
	Message         *proto.Message
	Mutate          func() Result
	Stream          func(StreamSender) error
}

type PaneHistoryUpdate struct {
	PaneID  uint32
	History []proto.StyledLine
}

type PaneRender struct {
	PaneID uint32
	Data   []byte
}

type StreamSender interface {
	Send(*proto.Message) error
}
