package server

import (
	"bytes"
	"testing"
)

func TestWriteReadMsg(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		msg  Message
	}{
		{
			name: "input message",
			msg:  Message{Type: MsgTypeInput, Input: []byte("hello")},
		},
		{
			name: "resize message",
			msg:  Message{Type: MsgTypeResize, Cols: 120, Rows: 40},
		},
		{
			name: "attach message",
			msg:  Message{Type: MsgTypeAttach, Session: "test-session", Cols: 80, Rows: 24},
		},
		{
			name: "detach message",
			msg:  Message{Type: MsgTypeDetach},
		},
		{
			name: "command message",
			msg:  Message{Type: MsgTypeCommand, CmdName: "list", CmdArgs: []string{"--all"}},
		},
		{
			name: "render message",
			msg:  Message{Type: MsgTypeRender, RenderData: []byte("\033[2J\033[Hhello world")},
		},
		{
			name: "command result",
			msg:  Message{Type: MsgTypeCmdResult, CmdOutput: "pane-1 local\n"},
		},
		{
			name: "exit message",
			msg:  Message{Type: MsgTypeExit},
		},
		{
			name: "empty input",
			msg:  Message{Type: MsgTypeInput, Input: []byte{}},
		},
		{
			name: "pane output",
			msg:  Message{Type: MsgTypePaneOutput, PaneID: 7, PaneData: []byte("terminal output")},
		},
		{
			name: "pane output empty data",
			msg:  Message{Type: MsgTypePaneOutput, PaneID: 1, PaneData: []byte{}},
		},
		{
			name: "pane output large pane id",
			msg:  Message{Type: MsgTypePaneOutput, PaneID: 0xFFFFFFFF, PaneData: []byte("x")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer

			if err := WriteMsg(&buf, &tt.msg); err != nil {
				t.Fatalf("WriteMsg: %v", err)
			}

			got, err := ReadMsg(&buf)
			if err != nil {
				t.Fatalf("ReadMsg: %v", err)
			}

			if got.Type != tt.msg.Type {
				t.Errorf("Type = %d, want %d", got.Type, tt.msg.Type)
			}
			if !bytes.Equal(got.Input, tt.msg.Input) {
				t.Errorf("Input = %q, want %q", got.Input, tt.msg.Input)
			}
			if got.Cols != tt.msg.Cols || got.Rows != tt.msg.Rows {
				t.Errorf("Cols/Rows = %d/%d, want %d/%d", got.Cols, got.Rows, tt.msg.Cols, tt.msg.Rows)
			}
			if got.Session != tt.msg.Session {
				t.Errorf("Session = %q, want %q", got.Session, tt.msg.Session)
			}
			if got.CmdName != tt.msg.CmdName {
				t.Errorf("CmdName = %q, want %q", got.CmdName, tt.msg.CmdName)
			}
			if !bytes.Equal(got.RenderData, tt.msg.RenderData) {
				t.Errorf("RenderData = %q, want %q", got.RenderData, tt.msg.RenderData)
			}
			if got.CmdOutput != tt.msg.CmdOutput {
				t.Errorf("CmdOutput = %q, want %q", got.CmdOutput, tt.msg.CmdOutput)
			}
			if got.PaneID != tt.msg.PaneID {
				t.Errorf("PaneID = %d, want %d", got.PaneID, tt.msg.PaneID)
			}
			if !bytes.Equal(got.PaneData, tt.msg.PaneData) {
				t.Errorf("PaneData = %q, want %q", got.PaneData, tt.msg.PaneData)
			}
		})
	}
}

func TestWriteReadMultiple(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer

	// Interleave gob and binary (PaneOutput) messages on the same stream
	// to verify the discriminator byte correctly routes decoding.
	msgs := []Message{
		{Type: MsgTypeAttach, Session: "s1", Cols: 80, Rows: 24},
		{Type: MsgTypePaneOutput, PaneID: 1, PaneData: []byte("hello from pane 1")},
		{Type: MsgTypeInput, Input: []byte("ls\n")},
		{Type: MsgTypePaneOutput, PaneID: 2, PaneData: []byte("hello from pane 2")},
		{Type: MsgTypeResize, Cols: 120, Rows: 40},
		{Type: MsgTypeDetach},
	}

	for _, msg := range msgs {
		if err := WriteMsg(&buf, &msg); err != nil {
			t.Fatalf("WriteMsg: %v", err)
		}
	}

	for i, want := range msgs {
		got, err := ReadMsg(&buf)
		if err != nil {
			t.Fatalf("ReadMsg[%d]: %v", i, err)
		}
		if got.Type != want.Type {
			t.Errorf("msg[%d].Type = %d, want %d", i, got.Type, want.Type)
		}
		if got.PaneID != want.PaneID {
			t.Errorf("msg[%d].PaneID = %d, want %d", i, got.PaneID, want.PaneID)
		}
		if !bytes.Equal(got.PaneData, want.PaneData) {
			t.Errorf("msg[%d].PaneData = %q, want %q", i, got.PaneData, want.PaneData)
		}
	}
}
