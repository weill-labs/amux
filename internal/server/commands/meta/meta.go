package meta

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/weill-labs/amux/internal/mux"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

const MetaUsage = "usage: meta <set|get|rm> ..."

type Context interface {
	ResolvePaneForMutation(paneRef string) (*mux.Pane, error)
	QueryPaneKV(paneRef string, requested []string) (string, error)
}

func Meta(ctx Context, args []string) commandpkg.Result {
	if len(args) == 0 {
		return commandpkg.Result{Err: errors.New(MetaUsage)}
	}

	switch args[0] {
	case "set":
		return MetaSet(ctx, args[1:])
	case "get":
		return MetaGet(ctx, args[1:])
	case "rm":
		return MetaRm(ctx, args[1:])
	default:
		return commandpkg.Result{Err: errors.New(MetaUsage)}
	}
}

func MetaSet(ctx Context, args []string) commandpkg.Result {
	if len(args) < 2 {
		return commandpkg.Result{Err: fmt.Errorf("usage: meta set <pane> key=value [key=value...]")}
	}
	paneRef := args[0]
	kvPairs := append([]string(nil), args[1:]...)
	return setKV(ctx, paneRef, kvPairs)
}

func MetaGet(ctx Context, args []string) commandpkg.Result {
	if len(args) < 1 {
		return commandpkg.Result{Err: fmt.Errorf("usage: meta get <pane> [key]")}
	}
	output, err := ctx.QueryPaneKV(args[0], args[1:])
	return commandpkg.Result{Output: output, Err: err}
}

func MetaRm(ctx Context, args []string) commandpkg.Result {
	if len(args) < 2 {
		return commandpkg.Result{Err: fmt.Errorf("usage: meta rm <pane> key [key...]")}
	}
	paneRef := args[0]
	keys := append([]string(nil), args[1:]...)
	return rmKV(ctx, paneRef, keys)
}

func SetKV(ctx Context, args []string) commandpkg.Result {
	if len(args) < 2 {
		return commandpkg.Result{Err: fmt.Errorf("usage: set-kv <pane> key=value [key=value...]")}
	}
	paneRef := args[0]
	kvPairs := append([]string(nil), args[1:]...)
	return setKV(ctx, paneRef, kvPairs)
}

func GetKV(ctx Context, args []string) commandpkg.Result {
	if len(args) < 1 {
		return commandpkg.Result{Err: fmt.Errorf("usage: get-kv <pane> [key...]")}
	}
	output, err := ctx.QueryPaneKV(args[0], args[1:])
	return commandpkg.Result{Output: output, Err: err}
}

func RmKV(ctx Context, args []string) commandpkg.Result {
	if len(args) < 2 {
		return commandpkg.Result{Err: fmt.Errorf("usage: rm-kv <pane> key [key...]")}
	}
	paneRef := args[0]
	keys := append([]string(nil), args[1:]...)
	return rmKV(ctx, paneRef, keys)
}

func setKV(ctx Context, paneRef string, kvPairs []string) commandpkg.Result {
	return commandpkg.Result{
		Mutate: func() commandpkg.Result {
			pane, err := ctx.ResolvePaneForMutation(paneRef)
			if err != nil {
				return commandpkg.Result{Err: err}
			}
			for _, raw := range kvPairs {
				key, value, err := ParseKVArg(raw)
				if err != nil {
					return commandpkg.Result{Err: err}
				}
				if err := SetPaneKVValue(pane, key, value); err != nil {
					return commandpkg.Result{Err: err}
				}
			}
			return commandpkg.Result{BroadcastLayout: true}
		},
	}
}

func rmKV(ctx Context, paneRef string, keys []string) commandpkg.Result {
	return commandpkg.Result{
		Mutate: func() commandpkg.Result {
			pane, err := ctx.ResolvePaneForMutation(paneRef)
			if err != nil {
				return commandpkg.Result{Err: err}
			}
			for _, key := range keys {
				if err := RemovePaneKVValue(pane, key); err != nil {
					return commandpkg.Result{Err: err}
				}
			}
			return commandpkg.Result{BroadcastLayout: true}
		},
	}
}

func ParseKVArg(raw string) (key, value string, err error) {
	key, value, ok := strings.Cut(raw, "=")
	if !ok {
		return "", "", fmt.Errorf("invalid key=value: %q", raw)
	}
	if strings.TrimSpace(key) == "" {
		return "", "", fmt.Errorf("invalid key=value: %q", raw)
	}
	return key, value, nil
}

func SetPaneKVValue(pane *mux.Pane, key, value string) error {
	manualBranch, err := mux.SetPaneMetaKV(&pane.Meta, key, value)
	if err != nil {
		return err
	}
	if key == mux.PaneMetaKeyBranch {
		pane.SetMetaManualBranch(manualBranch)
	}
	return nil
}

func RemovePaneKVValue(pane *mux.Pane, key string) error {
	manualBranch, err := mux.RemovePaneMetaKV(&pane.Meta, key)
	if err != nil {
		return err
	}
	if key == mux.PaneMetaKeyBranch {
		pane.SetMetaManualBranch(manualBranch)
	}
	return nil
}

func FormatPaneKV(meta mux.PaneMeta, requested []string) string {
	kv := mux.NormalizedMetaKV(meta)
	if len(kv) == 0 {
		return ""
	}

	keys := requested
	if len(keys) == 0 {
		keys = make([]string, 0, len(kv))
		for key := range kv {
			keys = append(keys, key)
		}
		sort.Strings(keys)
	}

	var out strings.Builder
	for _, key := range keys {
		value, ok := kv[key]
		if !ok {
			continue
		}
		out.WriteString(key)
		out.WriteByte('=')
		out.WriteString(value)
		out.WriteByte('\n')
	}
	return out.String()
}
