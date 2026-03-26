package wait

import (
	"fmt"
	"strconv"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func ParseWaitArgs(args []string) (afterGen uint64, afterSet bool, timeout time.Duration, err error) {
	timeout = 3 * time.Second
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--after":
			if i+1 >= len(args) {
				return 0, false, 0, fmt.Errorf("missing value for --after")
			}
			i++
			afterSet = true
			afterGen, err = strconv.ParseUint(args[i], 10, 64)
			if err != nil {
				return 0, false, 0, fmt.Errorf("invalid generation: %s", args[i])
			}
		case "--timeout":
			if i+1 >= len(args) {
				return 0, false, 0, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err = time.ParseDuration(args[i])
			if err != nil {
				return 0, false, 0, fmt.Errorf("invalid timeout: %s", args[i])
			}
		default:
			return 0, false, 0, fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	return afterGen, afterSet, timeout, nil
}

func ParseTimeout(args []string, startIdx int, defaultTimeout time.Duration) (time.Duration, error) {
	for i := startIdx; i < len(args); i++ {
		if args[i] == "--timeout" && i+1 < len(args) {
			i++
			d, err := time.ParseDuration(args[i])
			if err != nil {
				return 0, fmt.Errorf("invalid timeout: %s", args[i])
			}
			return d, nil
		}
	}
	return defaultTimeout, nil
}

func ParseUIGenArgs(args []string) (clientID string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--client":
			if i+1 >= len(args) {
				return "", fmt.Errorf("missing value for --client")
			}
			i++
			clientID = args[i]
		default:
			return "", fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	return clientID, nil
}

func ParseWaitUIArgs(args []string) (eventName, clientID string, afterGen uint64, afterSet bool, timeout time.Duration, err error) {
	if len(args) < 1 {
		return "", "", 0, false, 0, fmt.Errorf("usage: wait ui <event> [--client <id>] [--after N] [--timeout <duration>]")
	}
	eventName = args[0]
	timeout = 5 * time.Second
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--client":
			if i+1 >= len(args) {
				return "", "", 0, false, 0, fmt.Errorf("missing value for --client")
			}
			i++
			clientID = args[i]
		case "--after":
			if i+1 >= len(args) {
				return "", "", 0, false, 0, fmt.Errorf("missing value for --after")
			}
			i++
			afterSet = true
			afterGen, err = strconv.ParseUint(args[i], 10, 64)
			if err != nil {
				return "", "", 0, false, 0, fmt.Errorf("invalid --after generation: %s", args[i])
			}
		case "--timeout":
			if i+1 >= len(args) {
				return "", "", 0, false, 0, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err = time.ParseDuration(args[i])
			if err != nil {
				return "", "", 0, false, 0, fmt.Errorf("invalid timeout: %s", args[i])
			}
		default:
			return "", "", 0, false, 0, fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	return eventName, clientID, afterGen, afterSet, timeout, nil
}

func WaitBusyForegroundPID(status mux.AgentStatus) int {
	if status.Idle || len(status.ChildPIDs) == 0 {
		return 0
	}
	return status.ChildPIDs[len(status.ChildPIDs)-1]
}

func WaitBusyReady(candidatePID int, status mux.AgentStatus) (nextPID int, ready bool) {
	nextPID = WaitBusyForegroundPID(status)
	return nextPID, nextPID != 0 && nextPID == candidatePID
}
