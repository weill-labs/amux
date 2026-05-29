package layout

import (
	"fmt"
	"strings"

	"github.com/weill-labs/amux/internal/mux"
	cmdflags "github.com/weill-labs/amux/internal/server/commands/flags"
)

type createPaneMode uint8

const (
	createPaneModeSplit createPaneMode = iota
	createPaneModeSpawn
)

type createPaneArgs struct {
	PaneRef   string
	WindowRef string
	RootLevel bool
	Dir       mux.SplitDir
	Auto      bool
	Focus     bool
	Name      string
	Task      string
	Color     string
	Attach    *RemoteAttachRef
}

type RemoteAttachRef struct {
	Host     string
	PaneName string
}

type SplitArgs struct {
	PaneRef   string // explicit target pane to split (empty = use actor context)
	RootLevel bool
	Dir       mux.SplitDir
	Focus     bool
	Name      string
	Task      string
	Color     string
}

func parseCreatePaneArgs(mode createPaneMode, args []string) (createPaneArgs, error) {
	specs := []cmdflags.FlagSpec{
		{Name: "--focus", Type: cmdflags.FlagTypeBool},
		{Name: "--name", Type: cmdflags.FlagTypeString},
		{Name: "--task", Type: cmdflags.FlagTypeString},
		{Name: "--color", Type: cmdflags.FlagTypeString},
	}
	parsed := createPaneArgs{
		Dir: mux.SplitHorizontal,
	}
	if mode == createPaneModeSplit {
		specs = append(specs,
			cmdflags.FlagSpec{Name: "--vertical", Type: cmdflags.FlagTypeBool},
			cmdflags.FlagSpec{Name: "--horizontal", Type: cmdflags.FlagTypeBool},
		)
	} else {
		parsed.Dir = mux.SplitVertical
		specs = append(specs,
			cmdflags.FlagSpec{Name: "--auto", Type: cmdflags.FlagTypeBool},
			cmdflags.FlagSpec{Name: "--at", Type: cmdflags.FlagTypeString},
			cmdflags.FlagSpec{Name: "--window", Type: cmdflags.FlagTypeString},
			cmdflags.FlagSpec{Name: "--attach", Type: cmdflags.FlagTypeString},
			cmdflags.FlagSpec{Name: "--root", Type: cmdflags.FlagTypeBool},
			cmdflags.FlagSpec{Name: "--vertical", Type: cmdflags.FlagTypeBool},
			cmdflags.FlagSpec{Name: "--horizontal", Type: cmdflags.FlagTypeBool},
		)
	}

	flags, err := cmdflags.ParseCommandFlags(args, specs)
	if err != nil {
		return createPaneArgs{}, err
	}

	parsed.Focus = flags.Bool("--focus")
	parsed.Name = flags.String("--name")
	parsed.Task = flags.String("--task")
	parsed.Color = flags.String("--color")
	if mode == createPaneModeSpawn {
		parsed.Auto = flags.Bool("--auto")
		parsed.PaneRef = flags.String("--at")
		parsed.WindowRef = flags.String("--window")
		parsed.RootLevel = flags.Bool("--root")
		if flags.Seen("--attach") {
			attach, err := ParseRemoteAttachRef(flags.String("--attach"))
			if err != nil {
				return createPaneArgs{}, err
			}
			parsed.Attach = &attach
		}
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

	if mode == createPaneModeSplit {
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
	if parsed.WindowRef != "" && parsed.PaneRef != "" {
		return createPaneArgs{}, fmt.Errorf("spawn --window cannot be combined with --at")
	}
	if parsed.Auto && (parsed.RootLevel || hasExplicitDir) {
		return createPaneArgs{}, fmt.Errorf("spawn --auto cannot be combined with explicit placement")
	}
	if parsed.Auto && parsed.Attach != nil {
		return createPaneArgs{}, fmt.Errorf("spawn --auto cannot be combined with --attach")
	}
	if (((parsed.PaneRef != "" || parsed.WindowRef != "") && !parsed.Auto) || parsed.RootLevel) && !hasExplicitDir {
		parsed.Dir = mux.SplitHorizontal
	}
	return parsed, nil
}

func ParseRemoteAttachRef(value string) (RemoteAttachRef, error) {
	host, paneName, ok := strings.Cut(value, ":")
	host = strings.TrimSpace(host)
	paneName = strings.TrimSpace(paneName)
	if !ok || host == "" || paneName == "" {
		return RemoteAttachRef{}, fmt.Errorf("spawn --attach requires <host>:<pane-name>")
	}
	return RemoteAttachRef{Host: host, PaneName: paneName}, nil
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
		Name:      parsed.Name,
		Task:      parsed.Task,
		Color:     parsed.Color,
	}, nil
}

type SpawnArgs struct {
	PaneRef   string
	WindowRef string
	RootLevel bool
	Dir       mux.SplitDir
	Meta      mux.PaneMeta
	Auto      bool
	Focus     bool
	Attach    *RemoteAttachRef
}

func ParseSpawnArgs(args []string) (SpawnArgs, error) {
	parsed, err := parseCreatePaneArgs(createPaneModeSpawn, args)
	if err != nil {
		return SpawnArgs{}, err
	}
	host := mux.DefaultHost
	if parsed.Attach != nil {
		host = parsed.Attach.Host
	}
	return SpawnArgs{
		PaneRef:   parsed.PaneRef,
		WindowRef: parsed.WindowRef,
		RootLevel: parsed.RootLevel,
		Dir:       parsed.Dir,
		Meta: mux.PaneMeta{
			Name:  parsed.Name,
			Host:  host,
			Task:  parsed.Task,
			Color: parsed.Color,
		},
		Auto:   parsed.Auto,
		Focus:  parsed.Focus,
		Attach: parsed.Attach,
	}, nil
}

func DirName(dir mux.SplitDir) string {
	if dir == mux.SplitVertical {
		return "vertical"
	}
	return "horizontal"
}
