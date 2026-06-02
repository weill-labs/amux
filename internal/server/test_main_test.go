package server

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/testenv"
)

const serverSplitCountChildEnv = "AMUX_SERVER_SPLIT_COUNT_CHILD"

func TestMain(m *testing.M) {
	flag.Parse()

	if os.Getenv(sessionLockHelperModeEnv) != "" {
		os.Exit(m.Run())
	}

	count := currentTestCount()
	if count > 1 && os.Getenv(serverSplitCountChildEnv) != "1" {
		os.Exit(runServerTestCountInChildren(count))
	}

	cleanup, err := testenv.IsolateSocketDirForTestProcess("internal-server")
	if err != nil {
		fmt.Fprintf(os.Stderr, "isolating socket dir: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	cleanup()
	os.Exit(code)
}

func currentTestCount() int {
	countFlag := flag.Lookup("test.count")
	if countFlag == nil {
		return 1
	}
	count, err := strconv.Atoi(countFlag.Value.String())
	if err != nil || count < 1 {
		return 1
	}
	return count
}

func runServerTestCountInChildren(count int) int {
	args := serverTestArgsWithCount(os.Args[1:], 1)
	for i := 0; i < count; i++ {
		cmd := testenv.NewCommand(os.Args[0], args...)
		cmd.Env = append(cmd.Env,
			serverSplitCountChildEnv+"=1",
			fmt.Sprintf("AMUX_SERVER_SPLIT_COUNT_INDEX=%d", i+1),
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "server test iteration %d/%d failed: %v\n", i+1, count, err)
			return 1
		}
	}
	return 0
}

func serverTestArgsWithCount(args []string, count int) []string {
	next := make([]string, 0, len(args)+1)
	replaced := false
	countArg := "-test.count=" + strconv.Itoa(count)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-test.count" || arg == "--test.count":
			next = append(next, countArg)
			replaced = true
			if i+1 < len(args) {
				i++
			}
		case strings.HasPrefix(arg, "-test.count=") || strings.HasPrefix(arg, "--test.count="):
			next = append(next, countArg)
			replaced = true
		default:
			next = append(next, arg)
		}
	}
	if !replaced {
		next = append(next, countArg)
	}
	return next
}
