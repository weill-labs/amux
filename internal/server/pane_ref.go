package server

import (
	"context"

	"github.com/weill-labs/amux/internal/proto"
)

func (s *Session) queryPaneRef(ref string) (proto.PaneRef, error) {
	return s.queryPaneRefContext(s.context(), ref)
}

func (s *Session) queryPaneRefContext(ctx context.Context, ref string) (proto.PaneRef, error) {
	return enqueueSessionQueryOnState(ctx, s, func(s *Session) (proto.PaneRef, error) {
		return s.parsePaneRef(ref)
	})
}

func (s *Session) parsePaneRef(ref string) (proto.PaneRef, error) {
	return proto.ParsePaneRef(ref)
}
