package wrap

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/weill-labs/amux/internal/tmux"
	"golang.org/x/term"
)

// Config holds parameters for the PTY wrapper.
type Config struct {
	PaneID string   // tmux pane ID (e.g., "%5")
	Cmd    string   // command to run (e.g., "claude")
	Args   []string // command arguments
}

// Run starts the child process in a PTY, reserves the last terminal row for
// a status bar, and proxies I/O. Returns the child's exit code.
func Run(cfg Config, t tmux.Tmux) int {
	// Get terminal size from our stdout (the tmux pane).
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux wrap: get terminal size: %v\n", err)
		return 1
	}

	if rows < 3 {
		fmt.Fprintf(os.Stderr, "amux wrap: terminal too small (%d rows)\n", rows)
		return 1
	}

	// Start the child process in a PTY with 1 less row (status bar takes row H).
	child := exec.Command(cfg.Cmd, cfg.Args...)
	child.Env = os.Environ()
	childPTY, err := pty.StartWithSize(child, &pty.Winsize{
		Rows: uint16(rows - 1),
		Cols: uint16(cols),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux wrap: start child: %v\n", err)
		return 1
	}
	defer childPTY.Close()

	// Set @amux_wrapped so amux output knows to strip the status bar line.
	if cfg.PaneID != "" {
		t.SetOption(cfg.PaneID, "@amux_wrapped", "1")
	}

	// Put our stdin in raw mode so keystrokes pass through to the child.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux wrap: raw mode: %v\n", err)
		return 1
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Set scroll region to rows 1..(H-1), leaving row H for the status bar.
	setScrollRegion(os.Stdout, rows-1)

	parser := &Parser{}
	bar := &StatusBar{PaneID: cfg.PaneID, T: t}

	var mu sync.Mutex // protects stdout writes and altScreen state
	altScreen := false

	// drawBar saves cursor, renders the status bar at the bottom row, restores cursor.
	// Must be called with mu held.
	drawBar := func() {
		meta := bar.Fetch()
		fmt.Fprint(os.Stdout, "\x1b7") // save cursor
		fmt.Fprint(os.Stdout, bar.Render(cols, rows, meta))
		fmt.Fprint(os.Stdout, "\x1b8") // restore cursor
	}

	// resizeChild sets the child PTY size, accounting for the status bar row
	// when not in alt-screen mode. Must be called with mu held.
	resizeChild := func() {
		childRows := rows
		if !altScreen {
			childRows = rows - 1
		}
		pty.Setsize(childPTY, &pty.Winsize{
			Rows: uint16(childRows),
			Cols: uint16(cols),
		})
	}

	// Draw initial status bar.
	mu.Lock()
	drawBar()
	mu.Unlock()

	// proxyInput: stdin -> child PTY
	go func() {
		io.Copy(childPTY, os.Stdin)
	}()

	// proxyOutput: child PTY -> parser -> stdout
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := childPTY.Read(buf)
			if n > 0 {
				data := parser.Feed(buf[:n])

				mu.Lock()
				inAlt := parser.InAltScreen()
				if inAlt != altScreen {
					altScreen = inAlt
					if inAlt {
						clearScrollRegion(os.Stdout)
					} else {
						setScrollRegion(os.Stdout, rows-1)
					}
					resizeChild()
					if !inAlt {
						drawBar()
					}
				}
				os.Stdout.Write(data)
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	// updateStatusBar: periodic refresh (2s)
	stopBar := make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				if !altScreen {
					drawBar()
				}
				mu.Unlock()
			case <-stopBar:
				return
			}
		}
	}()

	// Signal handler: SIGWINCH -> resize, SIGTERM/SIGINT -> forward
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		for sig := range sigCh {
			switch sig {
			case syscall.SIGWINCH:
				newCols, newRows, err := term.GetSize(int(os.Stdout.Fd()))
				if err != nil || newRows < 3 {
					continue
				}
				mu.Lock()
				cols = newCols
				rows = newRows
				resizeChild()
				if !altScreen {
					setScrollRegion(os.Stdout, rows-1)
					drawBar()
				}
				mu.Unlock()
			case syscall.SIGTERM, syscall.SIGINT:
				if child.Process != nil {
					child.Process.Signal(sig)
				}
			}
		}
	}()

	// Wait for child to exit.
	err = child.Wait()
	close(stopBar)
	signal.Stop(sigCh)
	close(sigCh)

	// Restore terminal: clear scroll region. The deferred term.Restore handles raw mode.
	clearScrollRegion(os.Stdout)

	// Clear @amux_wrapped on exit.
	if cfg.PaneID != "" {
		t.SetOption(cfg.PaneID, "@amux_wrapped", "")
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

// setScrollRegion sets DECSTBM to confine scrolling to rows 1..n.
func setScrollRegion(w io.Writer, n int) {
	fmt.Fprintf(w, "\x1b[1;%dr", n)
}

// clearScrollRegion resets DECSTBM to the full terminal.
func clearScrollRegion(w io.Writer) {
	fmt.Fprint(w, "\x1b[r")
}
