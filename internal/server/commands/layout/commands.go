package layout

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	commandpkg "github.com/weill-labs/amux/internal/server/commands"
	cmdflags "github.com/weill-labs/amux/internal/server/commands/flags"
)

const (
	killArgsUsage              = "[--cleanup] [--timeout <duration>] [pane]"
	copyModeUsage              = "usage: copy-mode [pane] [--wait ui=copy-mode-shown] [--timeout <duration>]"
	defaultCopyModeWaitTimeout = 5 * time.Second
)

type Context interface {
	Split(actorPaneID uint32, args SplitArgs) commandpkg.Result
	Focus(actorPaneID uint32, direction string) commandpkg.Result
	Spawn(actorPaneID uint32, args SpawnArgs) commandpkg.Result
	Zoom(actorPaneID uint32, paneRef string) commandpkg.Result
	Reset(actorPaneID uint32, paneRef string) commandpkg.Result
	Kill(actorPaneID uint32, args KillArgs) commandpkg.Result
	Undo() commandpkg.Result
	CopyMode(actorPaneID uint32, opts CopyModeOptions) commandpkg.Result
	NewWindow(name string) commandpkg.Result
	SelectWindow(ref string) commandpkg.Result
	NextWindow() commandpkg.Result
	PrevWindow() commandpkg.Result
	RenameWindow(name string) commandpkg.Result
	ReorderWindow(from, to int) commandpkg.Result
	ResizeBorder(x, y, delta int) commandpkg.Result
	ResizeActive(direction string, delta int) commandpkg.Result
	ResizePane(actorPaneID uint32, paneRef, direction string, delta int) commandpkg.Result
	Equalize(widths, heights bool) commandpkg.Result
	ResizeWindow(cols, rows int) commandpkg.Result
	SetLead(actorPaneID uint32, paneRef string) commandpkg.Result
	UnsetLead(actorPaneID uint32) commandpkg.Result
	ToggleLead(actorPaneID uint32) commandpkg.Result
}

type killArgError struct {
	msg   string
	usage bool
}

func (e *killArgError) Error() string { return e.msg }

type KillArgs struct {
	PaneRef string
	Cleanup bool
	Timeout time.Duration
}

type CopyModeOptions struct {
	PaneRef           string
	WaitCopyModeShown bool
	WaitTimeout       time.Duration
}

func Split(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	parsed, err := ParseSplitArgs(args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	return ctx.Split(actorPaneID, parsed)
}

func Focus(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	direction := "next"
	if len(args) > 0 {
		direction = args[0]
	}
	return ctx.Focus(actorPaneID, direction)
}

func Spawn(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	parsed, err := ParseSpawnArgs(args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	return ctx.Spawn(actorPaneID, parsed)
}

func Zoom(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	paneRef := ""
	if len(args) > 0 {
		paneRef = args[0]
	}
	return ctx.Zoom(actorPaneID, paneRef)
}

func Reset(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	if len(args) < 1 {
		return commandpkg.Result{Err: fmt.Errorf("usage: reset <pane>")}
	}
	return ctx.Reset(actorPaneID, args[0])
}

func Kill(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	parsed, err := ParseKillCommandArgs(args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	return ctx.Kill(actorPaneID, parsed)
}

func Undo(ctx Context, _ []string) commandpkg.Result {
	return ctx.Undo()
}

func CopyMode(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	opts, err := ParseCopyModeArgs(args, defaultCopyModeWaitTimeout)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	return ctx.CopyMode(actorPaneID, opts)
}

func NewWindow(ctx Context, args []string) commandpkg.Result {
	name := ""
	for i := 0; i < len(args)-1; i += 2 {
		if args[i] == "--name" {
			name = args[i+1]
		}
	}
	return ctx.NewWindow(name)
}

func SelectWindow(ctx Context, args []string) commandpkg.Result {
	if len(args) < 1 {
		return commandpkg.Result{Err: fmt.Errorf("usage: select-window <index|name>")}
	}
	return ctx.SelectWindow(args[0])
}

func NextWindow(ctx Context, _ []string) commandpkg.Result {
	return ctx.NextWindow()
}

func PrevWindow(ctx Context, _ []string) commandpkg.Result {
	return ctx.PrevWindow()
}

func RenameWindow(ctx Context, args []string) commandpkg.Result {
	if len(args) < 1 {
		return commandpkg.Result{Err: fmt.Errorf("usage: rename-window <name>")}
	}
	return ctx.RenameWindow(args[0])
}

func ReorderWindow(ctx Context, args []string) commandpkg.Result {
	if len(args) < 2 {
		return commandpkg.Result{Err: fmt.Errorf("usage: reorder-window <from-index> <to-index>")}
	}
	from, err1 := strconv.Atoi(args[0])
	to, err2 := strconv.Atoi(args[1])
	if err1 != nil || err2 != nil || from <= 0 || to <= 0 {
		return commandpkg.Result{Err: fmt.Errorf("reorder-window: invalid window indices")}
	}
	return ctx.ReorderWindow(from, to)
}

func ResizeBorder(ctx Context, args []string) commandpkg.Result {
	if len(args) < 3 {
		return commandpkg.Result{Err: fmt.Errorf("usage: resize-border <x> <y> <delta>")}
	}
	x, err1 := strconv.Atoi(args[0])
	y, err2 := strconv.Atoi(args[1])
	delta, err3 := strconv.Atoi(args[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return commandpkg.Result{Err: fmt.Errorf("resize-border: invalid arguments")}
	}
	return ctx.ResizeBorder(x, y, delta)
}

func ResizeActive(ctx Context, args []string) commandpkg.Result {
	if len(args) < 2 {
		return commandpkg.Result{Err: fmt.Errorf("usage: resize-active <direction> <delta>")}
	}
	delta, err := strconv.Atoi(args[1])
	if err != nil {
		return commandpkg.Result{Err: fmt.Errorf("resize-active: invalid delta")}
	}
	return ctx.ResizeActive(args[0], delta)
}

func ResizePane(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	if len(args) < 2 {
		return commandpkg.Result{Err: fmt.Errorf("usage: resize-pane <pane> <direction> [delta]")}
	}
	direction := args[1]
	switch direction {
	case "left", "right", "up", "down":
	default:
		return commandpkg.Result{Err: fmt.Errorf("resize-pane: invalid direction %q (use left/right/up/down)", direction)}
	}
	delta := 1
	if len(args) >= 3 {
		var err error
		delta, err = strconv.Atoi(args[2])
		if err != nil || delta <= 0 {
			return commandpkg.Result{Err: fmt.Errorf("resize-pane: invalid delta")}
		}
	}
	return ctx.ResizePane(actorPaneID, args[0], direction, delta)
}

func Equalize(ctx Context, args []string) commandpkg.Result {
	widths, heights, err := ParseEqualizeCommandArgs(args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	return ctx.Equalize(widths, heights)
}

func ResizeWindow(ctx Context, args []string) commandpkg.Result {
	if len(args) < 2 {
		return commandpkg.Result{Err: fmt.Errorf("usage: resize-window <cols> <rows>")}
	}
	cols, err1 := strconv.Atoi(args[0])
	rows, err2 := strconv.Atoi(args[1])
	if err1 != nil || err2 != nil || cols <= 0 || rows <= 0 {
		return commandpkg.Result{Err: fmt.Errorf("resize-window: invalid dimensions")}
	}
	return ctx.ResizeWindow(cols, rows)
}

func SetLead(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	paneRef := ""
	if len(args) > 0 {
		paneRef = args[0]
	}
	return ctx.SetLead(actorPaneID, paneRef)
}

func UnsetLead(ctx Context, actorPaneID uint32, _ []string) commandpkg.Result {
	return ctx.UnsetLead(actorPaneID)
}

func ToggleLead(ctx Context, actorPaneID uint32, _ []string) commandpkg.Result {
	return ctx.ToggleLead(actorPaneID)
}

func KillCommandUsage(command string) string {
	if command == "" {
		return fmt.Sprintf("usage: kill %s", killArgsUsage)
	}
	return fmt.Sprintf("usage: %s kill %s", command, killArgsUsage)
}

func ValidateKillCommandArgs(args []string) error {
	_, err := ParseKillCommandArgs(args)
	return err
}

func FormatKillCommandError(err error, command string) string {
	var argErr *killArgError
	if errors.As(err, &argErr) && argErr.usage {
		return KillCommandUsage(command)
	}
	return err.Error()
}

func ParseKillCommandArgs(args []string) (KillArgs, error) {
	flags, err := cmdflags.ParseCommandFlags(args, []cmdflags.FlagSpec{
		{Name: "--cleanup", Type: cmdflags.FlagTypeBool},
		{Name: "--timeout", Type: cmdflags.FlagTypeDuration, Default: 5 * time.Second},
	})
	if err != nil {
		return KillArgs{}, &killArgError{msg: err.Error()}
	}
	positionals := flags.Positionals()
	if len(positionals) > 1 {
		return KillArgs{}, newKillUsageError()
	}
	opts := KillArgs{
		Cleanup: flags.Bool("--cleanup"),
		Timeout: flags.Duration("--timeout"),
	}
	if len(positionals) == 1 {
		opts.PaneRef = positionals[0]
	}
	if flags.Seen("--timeout") && !opts.Cleanup {
		return KillArgs{}, newKillUsageError()
	}
	return opts, nil
}

func ParseCopyModeArgs(args []string, defaultTimeout time.Duration) (CopyModeOptions, error) {
	flags, err := cmdflags.ParseCommandFlags(args, []cmdflags.FlagSpec{
		{Name: "--wait", Type: cmdflags.FlagTypeString},
		{Name: "--timeout", Type: cmdflags.FlagTypeDuration, Default: defaultTimeout},
	})
	if err != nil {
		return CopyModeOptions{}, err
	}
	positionals := flags.Positionals()
	if len(positionals) > 1 {
		return CopyModeOptions{}, fmt.Errorf(copyModeUsage)
	}
	opts := CopyModeOptions{WaitTimeout: flags.Duration("--timeout")}
	if len(positionals) == 1 {
		opts.PaneRef = positionals[0]
	}
	if flags.Seen("--wait") {
		target := flags.String("--wait")
		if target != "ui=copy-mode-shown" {
			return CopyModeOptions{}, fmt.Errorf("copy-mode: unsupported --wait target %q (want ui=copy-mode-shown)", target)
		}
		opts.WaitCopyModeShown = true
	}
	if flags.Seen("--timeout") && !opts.WaitCopyModeShown {
		return CopyModeOptions{}, fmt.Errorf("copy-mode: --timeout requires --wait ui=copy-mode-shown")
	}
	return opts, nil
}

func ParseEqualizeCommandArgs(args []string) (widths, heights bool, err error) {
	flags, err := cmdflags.ParseCommandFlags(args, []cmdflags.FlagSpec{
		{Name: "--vertical", Type: cmdflags.FlagTypeBool},
		{Name: "--all", Type: cmdflags.FlagTypeBool},
	})
	if err != nil {
		return false, false, err
	}
	positionals := flags.Positionals()
	if len(positionals) > 0 {
		return false, false, fmt.Errorf(`equalize: unknown mode %q (use --vertical or --all)`, positionals[0])
	}
	if flags.Bool("--vertical") && flags.Bool("--all") {
		return false, false, fmt.Errorf("equalize: conflicting equalize modes")
	}
	if flags.Bool("--all") {
		return true, true, nil
	}
	if flags.Bool("--vertical") {
		return false, true, nil
	}
	return true, false, nil
}

func newKillUsageError() error {
	return &killArgError{msg: KillCommandUsage(""), usage: true}
}
