package server

import "os"

// ServerEnv holds server-only environment variables that are read once at
// startup and unset so child processes (pane shells, inner amux) don't
// inherit them. The values are re-exported before syscall.Exec in Reload()
// so they survive hot-reload.
type ServerEnv struct {
	ExitUnattached bool // AMUX_EXIT_UNATTACHED=1
	NoWatch        bool // AMUX_NO_WATCH=1
	LogDir         string
}

// ReadServerEnv reads all server-only env vars and unsets them from the
// process environment. Call once at startup in runServer().
func ReadServerEnv() ServerEnv {
	env := ServerEnv{
		ExitUnattached: os.Getenv("AMUX_EXIT_UNATTACHED") == "1",
		NoWatch:        os.Getenv("AMUX_NO_WATCH") == "1",
		LogDir:         os.Getenv("AMUX_LOG_DIR"),
	}
	os.Unsetenv("AMUX_EXIT_UNATTACHED")
	os.Unsetenv("AMUX_NO_WATCH")
	os.Unsetenv("AMUX_LOG_DIR")
	return env
}

// Export returns the env vars as key=value strings for syscall.Exec.
// Only exports vars that are set (non-zero).
func (e ServerEnv) Export() []string {
	var out []string
	if e.ExitUnattached {
		out = append(out, "AMUX_EXIT_UNATTACHED=1")
	}
	if e.NoWatch {
		out = append(out, "AMUX_NO_WATCH=1")
	}
	if e.LogDir != "" {
		out = append(out, "AMUX_LOG_DIR="+e.LogDir)
	}
	return out
}
