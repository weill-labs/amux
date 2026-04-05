package server

import (
	"fmt"
	"sync"
	"time"

	eventscmd "github.com/weill-labs/amux/internal/server/commands/events"
)

func cmdEvents(ctx *CommandContext) {
	ctx.applyCommandResult(eventscmd.Events(eventsCommandContext{ctx}, ctx.Args))
}

type eventsArgs struct {
	filter   eventFilter
	throttle time.Duration
}

func parseEventsArgs(args []string) eventsArgs {
	parsed := eventscmd.ParseArgs(args, DefaultEventThrottle)
	return eventsArgs{
		filter: eventFilter{
			Types:    append([]string(nil), parsed.Filter.Types...),
			PaneName: parsed.Filter.PaneName,
			Host:     parsed.Filter.Host,
			ClientID: parsed.Filter.ClientID,
		},
		throttle: parsed.Throttle,
	}
}

type eventsCommandContext struct {
	*CommandContext
}

func (ctx eventsCommandContext) DefaultThrottle() time.Duration {
	return DefaultEventThrottle
}

func (ctx eventsCommandContext) Subscribe(filter eventscmd.Filter) (eventscmd.Subscription, error) {
	res := ctx.Sess.enqueueEventSubscribe(eventFilter{
		Types:    append([]string(nil), filter.Types...),
		PaneName: filter.PaneName,
		Host:     filter.Host,
		ClientID: filter.ClientID,
	}, true)
	if res.sub == nil {
		return eventscmd.Subscription{}, fmt.Errorf("session shutting down")
	}
	events := make(chan []byte, 64)
	stop := make(chan struct{})
	writerDone := ctx.CC.ensureWriter().done
	var closeOnce sync.Once
	closeSub := func() {
		closeOnce.Do(func() {
			close(stop)
			ctx.Sess.enqueueEventUnsubscribe(res.sub)
		})
	}
	go func() {
		defer close(events)
		for {
			select {
			case <-stop:
				return
			case <-writerDone:
				closeSub()
				return
			case data := <-res.sub.Ch:
				select {
				case events <- data:
				case <-stop:
					return
				case <-writerDone:
					closeSub()
					return
				}
			}
		}
	}()
	return eventscmd.Subscription{
		InitialState: res.initialState,
		Events:       events,
		Close:        closeSub,
	}, nil
}

func flushPendingOutputEvents(ctx *CommandContext, pending map[uint32][]byte) error {
	return eventscmd.FlushPendingOutputEvents(commandStreamSender{cc: ctx.CC}, pending)
}

func peekOutputPaneID(data []byte) (uint32, bool) {
	return eventscmd.PeekOutputPaneID(data)
}
