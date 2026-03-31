package events

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

type Filter struct {
	Types    []string
	PaneName string
	Host     string
	ClientID string
}

type Args struct {
	Filter   Filter
	Throttle time.Duration
}

type Subscription struct {
	InitialState [][]byte
	Events       <-chan []byte
	Close        func()
}

type Context interface {
	DefaultThrottle() time.Duration
	Subscribe(filter Filter) (Subscription, error)
}

func ParseArgs(args []string, defaultThrottle time.Duration) Args {
	parsed := Args{Throttle: defaultThrottle}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--filter":
			if i+1 < len(args) {
				i++
				parsed.Filter.Types = strings.Split(args[i], ",")
			}
		case "--pane":
			if i+1 < len(args) {
				i++
				parsed.Filter.PaneName = args[i]
			}
		case "--host":
			if i+1 < len(args) {
				i++
				parsed.Filter.Host = args[i]
			}
		case "--client":
			if i+1 < len(args) {
				i++
				parsed.Filter.ClientID = args[i]
			}
		case "--throttle":
			if i+1 < len(args) {
				i++
				if d, err := time.ParseDuration(args[i]); err == nil {
					parsed.Throttle = d
				}
			}
		}
	}
	return parsed
}

func Events(ctx Context, args []string) commandpkg.Result {
	parsed := ParseArgs(args, ctx.DefaultThrottle())
	return commandpkg.Result{
		Stream: func(sender commandpkg.StreamSender) error {
			sub, err := ctx.Subscribe(parsed.Filter)
			if err != nil {
				return err
			}
			if sub.Close != nil {
				defer sub.Close()
			}

			for _, data := range sub.InitialState {
				if err := sendLine(sender, data); err != nil {
					return err
				}
			}

			if parsed.Throttle <= 0 {
				for data := range sub.Events {
					if err := sendLine(sender, data); err != nil {
						return err
					}
				}
				return nil
			}

			ticker := time.NewTicker(parsed.Throttle)
			defer ticker.Stop()
			pending := make(map[uint32][]byte)

			for {
				select {
				case data, ok := <-sub.Events:
					if !ok {
						return nil
					}
					if paneID, isOutput := PeekOutputPaneID(data); isOutput {
						pending[paneID] = data
						continue
					}
					if err := sendLine(sender, data); err != nil {
						return err
					}
				case <-ticker.C:
					if err := FlushPendingOutputEvents(sender, pending); err != nil {
						return err
					}
				}
			}
		},
	}
}

func FlushPendingOutputEvents(sender commandpkg.StreamSender, pending map[uint32][]byte) error {
	if len(pending) == 0 {
		return nil
	}
	ids := make([]uint32, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if err := sendLine(sender, pending[id]); err != nil {
			return err
		}
		delete(pending, id)
	}
	return nil
}

func PeekOutputPaneID(data []byte) (uint32, bool) {
	if !bytes.Contains(data, []byte(`"type":"output"`)) {
		return 0, false
	}
	var partial struct {
		PaneID uint32 `json:"pane_id"`
	}
	if err := json.Unmarshal(data, &partial); err != nil {
		return 0, false
	}
	return partial.PaneID, true
}

func sendLine(sender commandpkg.StreamSender, data []byte) error {
	return sender.Send(&proto.Message{
		Type:      proto.MsgTypeCmdResult,
		CmdOutput: string(data) + "\n",
	})
}
