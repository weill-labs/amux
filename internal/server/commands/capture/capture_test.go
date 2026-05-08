package capture

import (
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

type captureCommandContext struct {
	called string
	args   []string
}

func (c *captureCommandContext) CaptureHistory(args []string) *proto.Message {
	c.called = "history"
	c.args = append([]string(nil), args...)
	return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: "history"}
}

func (c *captureCommandContext) CapturePaneWithFallback(args []string) *proto.Message {
	c.called = "pane"
	c.args = append([]string(nil), args...)
	return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: "pane"}
}

func (c *captureCommandContext) ForwardCapture(args []string) *proto.Message {
	c.called = "forward"
	c.args = append([]string(nil), args...)
	return &proto.Message{Type: proto.MsgTypeCaptureResponse, CmdOutput: "forward"}
}

func TestCaptureClientAndDisplayForwardToClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "client", args: []string{"--client", "pane-1"}},
		{name: "display", args: []string{"--display"}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := &captureCommandContext{}
			res := Capture(ctx, tt.args)
			if ctx.called != "forward" {
				t.Fatalf("called = %q, want forward", ctx.called)
			}
			if !reflect.DeepEqual(ctx.args, tt.args) {
				t.Fatalf("args = %v, want %v", ctx.args, tt.args)
			}
			if res.Message == nil || res.Message.CmdOutput != "forward" {
				t.Fatalf("result message = %#v, want forward capture response", res.Message)
			}
		})
	}
}
