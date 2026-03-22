package layout

import (
	"fmt"

	"github.com/weill-labs/amux/internal/mux"
)

type SplitArgs struct {
	RootLevel  bool
	Dir        mux.SplitDir
	HostName   string
	Name       string
	Background bool
}

func ParseSplitArgs(args []string) (SplitArgs, error) {
	parsed := SplitArgs{Dir: mux.SplitHorizontal}
	hasExplicitDir := false

	setDir := func(next mux.SplitDir) error {
		if hasExplicitDir && parsed.Dir != next {
			return fmt.Errorf("conflicting split directions")
		}
		parsed.Dir = next
		hasExplicitDir = true
		return nil
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "root":
			parsed.RootLevel = true
		case "v", "--vertical":
			if err := setDir(mux.SplitVertical); err != nil {
				return SplitArgs{}, err
			}
		case "--horizontal":
			if err := setDir(mux.SplitHorizontal); err != nil {
				return SplitArgs{}, err
			}
		case "--host":
			if i+1 >= len(args) {
				return SplitArgs{}, fmt.Errorf("--host requires a value")
			}
			parsed.HostName = args[i+1]
			i++
		case "--name":
			if i+1 >= len(args) {
				return SplitArgs{}, fmt.Errorf("--name requires a value")
			}
			parsed.Name = args[i+1]
			i++
		case "--background":
			parsed.Background = true
		default:
			return SplitArgs{}, fmt.Errorf("unknown split arg %q", args[i])
		}
	}

	return parsed, nil
}

type SpawnArgs struct {
	Meta       mux.PaneMeta
	Background bool
}

func ParseSpawnArgs(args []string) (SpawnArgs, error) {
	parsed := SpawnArgs{
		Meta: mux.PaneMeta{Host: mux.DefaultHost},
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 >= len(args) {
				return SpawnArgs{}, fmt.Errorf("--name requires a value")
			}
			parsed.Meta.Name = args[i+1]
			i++
		case "--host":
			if i+1 >= len(args) {
				return SpawnArgs{}, fmt.Errorf("--host requires a value")
			}
			parsed.Meta.Host = args[i+1]
			i++
		case "--task":
			if i+1 >= len(args) {
				return SpawnArgs{}, fmt.Errorf("--task requires a value")
			}
			parsed.Meta.Task = args[i+1]
			i++
		case "--color":
			if i+1 >= len(args) {
				return SpawnArgs{}, fmt.Errorf("--color requires a value")
			}
			parsed.Meta.Color = args[i+1]
			i++
		case "--background":
			parsed.Background = true
		default:
			return SpawnArgs{}, fmt.Errorf("unknown spawn arg %q", args[i])
		}
	}

	if parsed.Meta.Name == "" {
		return SpawnArgs{}, fmt.Errorf("--name is required")
	}
	return parsed, nil
}

func DirName(dir mux.SplitDir) string {
	if dir == mux.SplitVertical {
		return "vertical"
	}
	return "horizontal"
}
