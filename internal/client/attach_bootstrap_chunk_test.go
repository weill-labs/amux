package client

import (
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestApplyAttachBootstrapMessageAppendsPaneHistoryChunks(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(20, 4)
	t.Cleanup(cr.renderer.Close)
	cr.HandleLayout(singlePane20x3())

	applyAttachBootstrapMessage(cr, attachBootstrapMessage{
		msg: &proto.Message{
			Type:          proto.MsgTypePaneHistory,
			PaneID:        1,
			History:       []string{"old-1"},
			StyledHistory: []proto.StyledLine{{Text: "old-1"}},
		},
	})
	applyAttachBootstrapMessage(cr, attachBootstrapMessage{
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
