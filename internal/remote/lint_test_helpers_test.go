package remote

import (
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/weill-labs/amux/internal/proto"
)

func mustWriteMsg(tb testing.TB, writer *proto.Writer, msg *proto.Message) {
	tb.Helper()
	if err := writer.WriteMsg(msg); err != nil {
		tb.Fatalf("WriteMsg() error = %v", err)
	}
}

func mustReadMsg(tb testing.TB, reader *proto.Reader) *proto.Message {
	tb.Helper()
	msg, err := reader.ReadMsg()
	if err != nil {
		tb.Fatalf("ReadMsg() error = %v", err)
	}
	return msg
}

func ignoreReject(newChannel ssh.NewChannel, reason ssh.RejectionReason, message string) {
	_ = newChannel.Reject(reason, message) //nolint:errcheck // test server teardown can race channel shutdown
}

func ignoreReply(req *ssh.Request, ok bool) {
	_ = req.Reply(ok, nil) //nolint:errcheck // request channel may already be closed during test teardown
}

func ignoreSendRequest(ch ssh.Channel, name string, wantReply bool, payload []byte) {
	_, _ = ch.SendRequest(name, wantReply, payload) //nolint:errcheck // exit-status best-effort in test SSH server
}
