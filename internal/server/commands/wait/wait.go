package wait

import (
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	cmdflags "github.com/weill-labs/amux/internal/server/commands/flags"
)

func ParseWaitArgs(args []string) (afterGen uint64, afterSet bool, timeout time.Duration, err error) {
	return ParseWaitArgsWithDefault(args, 3*time.Second)
}

func ParseWaitArgsWithDefault(args []string, defaultTimeout time.Duration) (afterGen uint64, afterSet bool, timeout time.Duration, err error) {
	parsed, err := cmdflags.ParseCommandFlags(args, []cmdflags.FlagSpec{
		{Name: "--after", Type: cmdflags.FlagTypeInt},
		{Name: "--timeout", Type: cmdflags.FlagTypeDuration, Default: defaultTimeout},
	})
	if err != nil {
		return 0, false, 0, err
	}
	positionals := parsed.Positionals()
	if len(positionals) > 0 {
		return 0, false, 0, fmt.Errorf("unknown flag: %s", positionals[0])
	}
	timeout = parsed.Duration("--timeout")
	afterSet = parsed.Seen("--after")
	if afterSet {
		after := parsed.Int("--after")
		if after < 0 {
			return 0, false, 0, fmt.Errorf("invalid value for --after: %d", after)
		}
		afterGen = uint64(after)
	}
	return afterGen, afterSet, timeout, nil
}

func ParseTimeout(args []string, startIdx int, defaultTimeout time.Duration) (time.Duration, error) {
	parsed, err := cmdflags.ParseCommandFlags(args[startIdx:], []cmdflags.FlagSpec{
		{Name: "--timeout", Type: cmdflags.FlagTypeDuration, Default: defaultTimeout},
	})
	if err != nil {
		return 0, err
	}
	positionals := parsed.Positionals()
	if len(positionals) > 0 {
		return 0, fmt.Errorf("unknown flag: %s", positionals[0])
	}
	return parsed.Duration("--timeout"), nil
}

func ParseUIGenArgs(args []string) (clientID string, err error) {
	parsed, err := cmdflags.ParseCommandFlags(args, []cmdflags.FlagSpec{
		{Name: "--client", Type: cmdflags.FlagTypeString},
	})
	if err != nil {
		return "", err
	}
	positionals := parsed.Positionals()
	if len(positionals) > 0 {
		return "", fmt.Errorf("unknown flag: %s", positionals[0])
	}
	return parsed.String("--client"), nil
}

func ParseWaitUIArgs(args []string) (eventName, clientID string, afterGen uint64, afterSet bool, timeout time.Duration, err error) {
	if len(args) < 1 {
		return "", "", 0, false, 0, fmt.Errorf("usage: wait ui <event> [--client <id>] [--after N] [--timeout <duration>]")
	}
	eventName = args[0]
	parsed, err := cmdflags.ParseCommandFlags(args[1:], []cmdflags.FlagSpec{
		{Name: "--client", Type: cmdflags.FlagTypeString},
		{Name: "--after", Type: cmdflags.FlagTypeInt},
		{Name: "--timeout", Type: cmdflags.FlagTypeDuration, Default: 5 * time.Second},
	})
	if err != nil {
		return "", "", 0, false, 0, err
	}
	positionals := parsed.Positionals()
	if len(positionals) > 0 {
		return "", "", 0, false, 0, fmt.Errorf("unknown flag: %s", positionals[0])
	}
	clientID = parsed.String("--client")
	afterSet = parsed.Seen("--after")
	if afterSet {
		after := parsed.Int("--after")
		if after < 0 {
			return "", "", 0, false, 0, fmt.Errorf("invalid value for --after: %d", after)
		}
		afterGen = uint64(after)
	}
	timeout = parsed.Duration("--timeout")
	return eventName, clientID, afterGen, afterSet, timeout, nil
}

func WaitBusyForegroundPID(status mux.ForegroundJobState) int {
	if status.Idle {
		return 0
	}
	return status.ForegroundProcessGroup
}

func WaitBusyReady(candidatePID int, status mux.ForegroundJobState) (nextPID int, ready bool) {
	nextPID = WaitBusyForegroundPID(status)
	return nextPID, nextPID != 0 && nextPID == candidatePID
}
