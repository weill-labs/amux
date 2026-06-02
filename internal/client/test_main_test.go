package client

import (
	"fmt"
	"os"
	"testing"

	"github.com/weill-labs/amux/internal/testenv"
)

func TestMain(m *testing.M) {
	cleanup, err := testenv.IsolateSocketDirForTestProcess("internal-client")
	if err != nil {
		fmt.Fprintf(os.Stderr, "isolating socket dir: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	cleanup()
	os.Exit(code)
}
