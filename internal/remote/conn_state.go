package remote

import "github.com/weill-labs/amux/internal/proto"

type ConnState = proto.ConnState

const (
	Disconnected = proto.Disconnected
	Connecting   = proto.Connecting
	Connected    = proto.Connected
	Reconnecting = proto.Reconnecting
)
