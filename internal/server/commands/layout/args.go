package layout

import (
	"fmt"

	"github.com/weill-labs/amux/internal/mux"
	cmdflags "github.com/weill-labs/amux/internal/server/commands/flags"
)

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

func ParseSplitArgs(args []string) (SplitArgs, error) {
	flags, err := cmdflags.ParseCommandFlags(args, []cmdflags.FlagSpec{
		{Name: "--vertical", Type: cmdflags.FlagTypeBool},
		{Name: "--horizontal", Type: cmdflags.FlagTypeBool},
		{Name: "--host", Type: cmdflags.FlagTypeString},
		{Name: "--focus", Type: cmdflags.FlagTypeBool},
		{Name: "--name", Type: cmdflags.FlagTypeString},
		{Name: "--task", Type: cmdflags.FlagTypeString},
		{Name: "--color", Type: cmdflags.FlagTypeString},
	})
	if err != nil {
		return SplitArgs{}, err
	}

	parsed := SplitArgs{
		Dir:      mux.SplitHorizontal,
		Focus:    flags.Bool("--focus"),
		HostName: flags.String("--host"),
		Name:     flags.String("--name"),
		Task:     flags.String("--task"),
		Color:    flags.String("--color"),
	}
	hasExplicitDir := false

	setDir := func(next mux.SplitDir) error {
		if hasExplicitDir && parsed.Dir != next {
			return fmt.Errorf("conflicting split directions")
		}
		parsed.Dir = next
		hasExplicitDir = true
		return nil
	}

	if flags.Bool("--vertical") {
		if err := setDir(mux.SplitVertical); err != nil {
			return SplitArgs{}, err
		}
	}
	if flags.Bool("--horizontal") {
		if err := setDir(mux.SplitHorizontal); err != nil {
			return SplitArgs{}, err
		}
	}

	for _, arg := range flags.Positionals() {
		switch arg {
		case "root":
			parsed.RootLevel = true
		case "v":
			if err := setDir(mux.SplitVertical); err != nil {
				return SplitArgs{}, err
			}
		default:
			if parsed.PaneRef == "" && arg != "" {
				parsed.PaneRef = arg
			} else {
				return SplitArgs{}, fmt.Errorf("unknown split arg %q", arg)
			}
		}
	}

	return parsed, nil
}
type SpawnArgs struct {
	Meta         mux.PaneMeta
	Focus        bool
	Spiral       bool
	HostExplicit bool
}

func ParseSpawnArgs(args []string) (SpawnArgs, error) {
	flags, err := cmdflags.ParseCommandFlags(args, []cmdflags.FlagSpec{
		{Name: "--name", Type: cmdflags.FlagTypeString},
		{Name: "--focus", Type: cmdflags.FlagTypeBool},
		{Name: "--host", Type: cmdflags.FlagTypeString},
		{Name: "--task", Type: cmdflags.FlagTypeString},
		{Name: "--color", Type: cmdflags.FlagTypeString},
		{Name: "--spiral", Type: cmdflags.FlagTypeBool},
	})
	if err != nil {
		return SpawnArgs{}, err
	}
	positionals := flags.Positionals()
	if len(positionals) > 0 {
		return SpawnArgs{}, fmt.Errorf("unknown spawn arg %q", positionals[0])
	}

	host := flags.String("--host")
	if host == "" {
		host = mux.DefaultHost
	}
	return SpawnArgs{
		Focus:        flags.Bool("--focus"),
		Spiral:       flags.Bool("--spiral"),
		HostExplicit: flags.Seen("--host"),
		Meta: mux.PaneMeta{
			Name:  flags.String("--name"),
			Host:  host,
			Task:  flags.String("--task"),
			Color: flags.String("--color"),
		},
	}, nil
}

func DirName(dir mux.SplitDir) string {
	if dir == mux.SplitVertical {
		return "vertical"
	}
	return "horizontal"
}
