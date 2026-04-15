package client

import (
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestApplyAttachBootstrapReplayMessageAppendsPaneHistoryChunks(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(20, 4)
	t.Cleanup(cr.renderer.Close)
	cr.HandleLayout(singlePane20x3())

	applyAttachBootstrapReplayMessage(cr, attachBootstrapMessage{
		msg: &proto.Message{
			Type:          proto.MsgTypePaneHistory,
			PaneID:        1,
			History:       []string{"old-1"},
			StyledHistory: []proto.StyledLine{{Text: "old-1"}},
		},
	})
	applyAttachBootstrapReplayMessage(cr, attachBootstrapMessage{
		msg: &proto.Message{
			Type:          proto.MsgTypePaneHistory,
			PaneID:        1,
			History:       []string{"old-2", "old-3"},
			StyledHistory: []proto.StyledLine{{Text: "old-2"}, {Text: "old-3"}},
		},
	})

	got := proto.StyledLineText(cr.loadState().baseHistory[1])
	want := []string{"old-1", "old-2", "old-3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bootstrap pane history = %v, want %v", got, want)
	}
}

func TestApplyAttachBootstrapMessageReplacesPaneHistoryDuringCorrection(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(20, 4)
	t.Cleanup(cr.renderer.Close)
	cr.HandleLayout(singlePane20x3())
	cr.HandlePaneHistory(1, []string{"old-1", "old-2"})

	applyAttachBootstrapMessage(cr, attachBootstrapMessage{
		msg: &proto.Message{
			Type:          proto.MsgTypePaneHistory,
			PaneID:        1,
			History:       []string{"reset"},
			StyledHistory: []proto.StyledLine{{Text: "reset"}},
		},
	})

	got := proto.StyledLineText(cr.loadState().baseHistory[1])
	want := []string{"reset"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("correction pane history = %v, want %v", got, want)
	}
}
