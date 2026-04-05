package cli

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	_ = os.Unsetenv("AMUX_SESSION")
	_ = os.Unsetenv("TMUX")

	os.Exit(m.Run())
}
