package server

import (
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestRemotePaneRefCommandsForwardToHostCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		command    string
		args       []string
		wantArgs   []string
		wantOutput string
	}{
		{
			name:       "capture pane ref",
			command:    "capture",
			args:       []string{"gpu/pane-7", "--format", "json"},
			wantArgs:   []string{"--format", "json", "pane-7"},
			wantOutput: "{\n  \"name\": \"pane-7\"\n}\n",
		},
		{
			name:       "capture host-only ref drops pane argument",
			command:    "capture",
			args:       []string{"gpu", "--format", "json"},
			wantArgs:   []string{"--format", "json"},
			wantOutput: "{\n  \"panes\": []\n}\n",
		},
		{
			name:       "send-keys pane ref",
			command:    "send-keys",
			args:       []string{"gpu/pane-7", "echo hi", "Enter"},
			wantArgs:   []string{"pane-7", "echo hi", "Enter"},
			wantOutput: "Sent 8 bytes to pane-7\n",
		},
		{
			name:       "wait content pane ref",
			command:    "wait",
			args:       []string{"content", "gpu/pane-7", "READY", "--timeout", "3s"},
			wantArgs:   []string{"content", "pane-7", "READY", "--timeout", "3s"},
			wantOutput: "matched\n",
		},
		{
			name:       "resize-pane pane ref",
			command:    "resize-pane",
			args:       []string{"gpu/pane-7", "right", "2"},
			wantArgs:   []string{"pane-7", "right", "2"},
			wantOutput: "Resized pane-7 right by 2\n",
		},
		{
			name:       "kill pane ref",
			command:    "kill",
			args:       []string{"--cleanup", "--timeout", "250ms", "gpu/pane-7"},
			wantArgs:   []string{"--cleanup", "--timeout", "250ms", "pane-7"},
			wantOutput: "Cleaning up pane-7\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			transport := &stubPaneTransport{
				hostStatusByName: map[string]proto.ConnState{"gpu": proto.Connected},
				runHostOutput:    tt.wantOutput,
			}
			installTestPaneTransport(t, sess, transport, nil)

			res := runTestCommand(t, srv, sess, tt.command, tt.args...)
			if res.cmdErr != "" {
				t.Fatalf("%s error = %q", tt.command, res.cmdErr)
			}
			if res.output != tt.wantOutput {
				t.Fatalf("%s output = %q, want %q", tt.command, res.output, tt.wantOutput)
			}
			if len(transport.runHostCalls) != 1 {
				t.Fatalf("RunHostCommand calls = %d, want 1", len(transport.runHostCalls))
			}
			call := transport.runHostCalls[0]
			if call.hostName != "gpu" {
				t.Fatalf("RunHostCommand host = %q, want %q", call.hostName, "gpu")
			}
			wantSession := managedSessionName(sess.Name)
			if call.sessionName != wantSession {
				t.Fatalf("RunHostCommand session = %q, want %q", call.sessionName, wantSession)
			}
			if call.cmdName != tt.command {
				t.Fatalf("RunHostCommand cmd = %q, want %q", call.cmdName, tt.command)
			}
			if !reflect.DeepEqual(call.cmdArgs, tt.wantArgs) {
				t.Fatalf("RunHostCommand args = %#v, want %#v", call.cmdArgs, tt.wantArgs)
			}
		})
	}
}
