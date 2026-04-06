package layout

import (
	"fmt"

	"github.com/weill-labs/amux/internal/mux"
	cmdflags "github.com/weill-labs/amux/internal/server/commands/flags"
)

type createPaneMode uint8

const (
	createPaneModeSplit createPaneMode = iota
	createPaneModeSpawn
)

type createPaneArgs struct {
	PaneRef      string
	RootLevel    bool
	Dir          mux.SplitDir
	Focus        bool
	HostName     string
	HostExplicit bool
	Name         string
	Task         string
	Color        string
}

type SplitArgs struct {
	PaneRef   string // explicit target pane to split (empty = use actor context)
	RootLevel bool
	Dir       mux.SplitDir
	Focus     bool
	HostName  string
	Name      string
	Task      string
	Color     string
}

func parseCreatePaneArgs(mode createPaneMode, args []string) (createPaneArgs, error) {
	specs := []cmdflags.FlagSpec{
		{Name: "--host", Type: cmdflags.FlagTypeString},
		{Name: "--focus", Type: cmdflags.FlagTypeBool},
		{Name: "--name", Type: cmdflags.FlagTypeString},
		{Name: "--task", Type: cmdflags.FlagTypeString},
		{Name: "--color", Type: cmdflags.FlagTypeString},
	}
	parsed := createPaneArgs{
		Dir:          mux.SplitHorizontal,
		HostExplicit: false,
	}
	if mode == createPaneModeSplit {
		specs = append(specs,
			cmdflags.FlagSpec{Name: "--vertical", Type: cmdflags.FlagTypeBool},
			cmdflags.FlagSpec{Name: "--horizontal", Type: cmdflags.FlagTypeBool},
		)
	} else {
		parsed.Dir = mux.SplitVertical
	}

	flags, err := cmdflags.ParseCommandFlags(args, specs)
	if err != nil {
		return createPaneArgs{}, err
	}

	parsed.Focus = flags.Bool("--focus")
	parsed.HostName = flags.String("--host")
	parsed.Name = flags.String("--name")
	parsed.Task = flags.String("--task")
	parsed.Color = flags.String("--color")
	if mode == createPaneModeSpawn {
		parsed.HostExplicit = flags.Seen("--host")
	}

	hasExplicitDir := false
	setDir := func(next mux.SplitDir) error {
		if hasExplicitDir && parsed.Dir != next {
			return fmt.Errorf("conflicting %s directions", mode.command())
		}
		parsed.Dir = next
		hasExplicitDir = true
		return nil
	}

	if mode == createPaneModeSplit {
		if flags.Bool("--vertical") {
			if err := setDir(mux.SplitVertical); err != nil {
				return createPaneArgs{}, err
			}
		}
		if flags.Bool("--horizontal") {
			if err := setDir(mux.SplitHorizontal); err != nil {
				return createPaneArgs{}, err
			}
		}
		for _, arg := range flags.Positionals() {
			switch arg {
			case "root":
				parsed.RootLevel = true
			case "v":
				if err := setDir(mux.SplitVertical); err != nil {
					return createPaneArgs{}, err
				}
			default:
				if parsed.PaneRef == "" && arg != "" {
					parsed.PaneRef = arg
					continue
				}
				return createPaneArgs{}, fmt.Errorf("unknown split arg %q", arg)
			}
		}
		return parsed, nil
	}

	positionals := flags.Positionals()
	if len(positionals) > 0 {
		return createPaneArgs{}, fmt.Errorf("unknown spawn arg %q", positionals[0])
	}
	return parsed, nil
}

func (m createPaneMode) command() string {
	if m == createPaneModeSplit {
		return "split"
	}
	return "spawn"
}

func ParseSplitArgs(args []string) (SplitArgs, error) {
	parsed, err := parseCreatePaneArgs(createPaneModeSplit, args)
	if err != nil {
		return SplitArgs{}, err
	}
	return SplitArgs{
		PaneRef:   parsed.PaneRef,
		RootLevel: parsed.RootLevel,
		Dir:       parsed.Dir,
		Focus:     parsed.Focus,
		HostName:  parsed.HostName,
		Name:      parsed.Name,
		Task:      parsed.Task,
		Color:     parsed.Color,
	}, nil
}

type SpawnArgs struct {
	Meta         mux.PaneMeta
	Focus        bool
	HostExplicit bool
}

func ParseSpawnArgs(args []string) (SpawnArgs, error) {
	parsed, err := parseCreatePaneArgs(createPaneModeSpawn, args)
	if err != nil {
		return SpawnArgs{}, err
	}
	host := parsed.HostName
	if host == "" {
		host = mux.DefaultHost
	}
	return SpawnArgs{
		Meta: mux.PaneMeta{
			Name:  parsed.Name,
			Host:  host,
			Task:  parsed.Task,
			Color: parsed.Color,
		},
		Focus:        parsed.Focus,
		HostExplicit: parsed.HostExplicit,
	}, nil
}

func DirName(dir mux.SplitDir) string {
	if dir == mux.SplitVertical {
		return "vertical"
	}
	return "horizontal"
}
