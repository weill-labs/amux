package test

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

// ---------------------------------------------------------------------------
// TmuxBenchHarness — lightweight tmux wrapper for comparison benchmarks
// ---------------------------------------------------------------------------

// TmuxBenchHarness manages a tmux session for benchmark comparisons.
type TmuxBenchHarness struct {
	tb      testing.TB
	session string
}

func newTmuxBenchHarness(b *testing.B) *TmuxBenchHarness {
	b.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		b.Skip("tmux not found, skipping tmux benchmarks")
	}

	var buf [4]byte
	rand.Read(buf[:])
	session := fmt.Sprintf("bench-%x", buf)

	out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "-x", "80", "-y", "24").CombinedOutput()
	if err != nil {
		b.Fatalf("tmux new-session: %v\n%s", err, out)
	}

	h := &TmuxBenchHarness{tb: b, session: session}
	b.Cleanup(h.cleanup)
	return h
}

func (h *TmuxBenchHarness) run(args ...string) string {
	h.tb.Helper()
	out, _ := exec.Command("tmux", args...).CombinedOutput()
	return string(out)
}

func (h *TmuxBenchHarness) cleanup() {
	exec.Command("tmux", "kill-session", "-t", h.session).Run()
}

// ---------------------------------------------------------------------------
// BenchmarkCapture — CLI round-trip: capture output
// ---------------------------------------------------------------------------

func BenchmarkCapture(b *testing.B) {
	b.Run("amux", func(b *testing.B) {
		for _, panes := range []int{1, 4} {
			b.Run(fmt.Sprintf("panes_%d", panes), func(b *testing.B) {
				b.StopTimer()
				h := newServerHarness(b)
				for i := 1; i < panes; i++ {
					h.splitV()
				}
				b.StartTimer()
				b.ReportAllocs()
				for b.Loop() {
					h.capture()
				}
			})
		}
	})

	b.Run("tmux", func(b *testing.B) {
		for _, panes := range []int{1, 4} {
			b.Run(fmt.Sprintf("panes_%d", panes), func(b *testing.B) {
				b.StopTimer()
				th := newTmuxBenchHarness(b)
				for i := 1; i < panes; i++ {
					th.run("split-window", "-t", th.session)
				}
				b.StartTimer()
				for b.Loop() {
					th.run("capture-pane", "-t", th.session, "-p")
				}
			})
		}
	})
}

// ---------------------------------------------------------------------------
// BenchmarkList — CLI round-trip: list panes
// ---------------------------------------------------------------------------

func BenchmarkList(b *testing.B) {
	b.Run("amux", func(b *testing.B) {
		for _, panes := range []int{1, 4} {
			b.Run(fmt.Sprintf("panes_%d", panes), func(b *testing.B) {
				b.StopTimer()
				h := newServerHarness(b)
				for i := 1; i < panes; i++ {
					h.splitV()
				}
				b.StartTimer()
				for b.Loop() {
					h.runCmd("list")
				}
			})
		}
	})

	b.Run("tmux", func(b *testing.B) {
		for _, panes := range []int{1, 4} {
			b.Run(fmt.Sprintf("panes_%d", panes), func(b *testing.B) {
				b.StopTimer()
				th := newTmuxBenchHarness(b)
				for i := 1; i < panes; i++ {
					th.run("split-window", "-t", th.session)
				}
				b.StartTimer()
				for b.Loop() {
					th.run("list-panes", "-t", th.session, "-F", "#{pane_id} #{pane_title}")
				}
			})
		}
	})
}

// ---------------------------------------------------------------------------
// BenchmarkSendKeys — CLI round-trip: inject keystrokes
// ---------------------------------------------------------------------------

func BenchmarkSendKeys(b *testing.B) {
	b.Run("amux", func(b *testing.B) {
		for _, panes := range []int{1, 4} {
			b.Run(fmt.Sprintf("panes_%d", panes), func(b *testing.B) {
				b.StopTimer()
				h := newServerHarness(b)
				for i := 1; i < panes; i++ {
					h.splitV()
				}
				b.StartTimer()
				for b.Loop() {
					h.sendKeys("pane-1", "x")
				}
			})
		}
	})

	b.Run("tmux", func(b *testing.B) {
		for _, panes := range []int{1, 4} {
			b.Run(fmt.Sprintf("panes_%d", panes), func(b *testing.B) {
				b.StopTimer()
				th := newTmuxBenchHarness(b)
				for i := 1; i < panes; i++ {
					th.run("split-window", "-t", th.session)
				}
				b.StartTimer()
				for b.Loop() {
					th.run("send-keys", "-t", th.session, "x", "")
				}
			})
		}
	})
}

// ---------------------------------------------------------------------------
// BenchmarkInputLatency — keystroke to visible output
// ---------------------------------------------------------------------------

func BenchmarkInputLatency(b *testing.B) {
	b.Run("amux", func(b *testing.B) {
		b.StopTimer()
		h := newServerHarness(b)
		b.StartTimer()
		for i := range b.N {
			marker := fmt.Sprintf("BENCH-%04d", i)
			h.sendKeys("pane-1", fmt.Sprintf("echo %s", marker), "Enter")
			h.waitFor("pane-1", marker)
		}
	})

	b.Run("tmux", func(b *testing.B) {
		b.StopTimer()
		th := newTmuxBenchHarness(b)
		b.StartTimer()
		for i := range b.N {
			marker := fmt.Sprintf("BENCH-%04d", i)
			th.run("send-keys", "-t", th.session, fmt.Sprintf("echo %s", marker), "Enter")
			// Poll at 5ms since tmux has no blocking wait-for
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				out := th.run("capture-pane", "-t", th.session, "-p")
				if strings.Contains(out, marker) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	})
}

// BenchmarkInputLatencyPersistent isolates the keystroke-to-visible-output path
// by sending commands over the already-attached headless client connection
// instead of spawning fresh `amux` processes for each iteration.
func BenchmarkInputLatencyPersistent(b *testing.B) {
	b.Run("amux", func(b *testing.B) {
		b.StopTimer()
		h := newServerHarness(b)
		b.StartTimer()
		for i := range b.N {
			marker := fmt.Sprintf("BENCH-%04d", i)

			msg := h.client.runCommand("send-keys", "pane-1", fmt.Sprintf("echo %s", marker), "Enter")
			if msg.CmdErr != "" {
				b.Fatalf("send-keys failed: %s", msg.CmdErr)
			}

			msg = h.client.runCommand("wait", "content", "pane-1", marker, "--timeout", "10s")
			if msg.CmdErr != "" {
				b.Fatalf("wait-for failed: %s", msg.CmdErr)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// BenchmarkThroughput — high-bandwidth output via CLI round-trips.
// This includes two short-lived `amux` command processes per iteration
// (`send-keys` and `wait-for`), so it measures harness overhead as well
// as the underlying pane-output path.
// ---------------------------------------------------------------------------

func BenchmarkThroughput(b *testing.B) {
	b.Run("amux", func(b *testing.B) {
		b.StopTimer()
		h := newServerHarness(b)
		b.StartTimer()
		for i := range b.N {
			marker := fmt.Sprintf("DONE-%04d", i)
			h.sendKeys("pane-1", fmt.Sprintf("seq 1 10000; echo %s", marker), "Enter")
			h.waitForTimeout("pane-1", marker, "30s")
		}
	})

	b.Run("tmux", func(b *testing.B) {
		b.StopTimer()
		th := newTmuxBenchHarness(b)
		b.StartTimer()
		for i := range b.N {
			marker := fmt.Sprintf("DONE-%04d", i)
			th.run("send-keys", "-t", th.session, fmt.Sprintf("seq 1 10000; echo %s", marker), "Enter")
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				out := th.run("capture-pane", "-t", th.session, "-p")
				if strings.Contains(out, marker) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	})
}

// BenchmarkThroughputPersistent isolates the pane-output path by sending
// commands over the already-attached headless client connection instead of
// spawning fresh `amux` processes for each iteration.
func BenchmarkThroughputPersistent(b *testing.B) {
	b.Run("amux", func(b *testing.B) {
		b.StopTimer()
		h := newServerHarness(b)
		b.StartTimer()
		for i := range b.N {
			marker := fmt.Sprintf("DONE-%04d", i)

			msg := h.client.runCommand("send-keys", "pane-1", fmt.Sprintf("seq 1 10000; echo %s", marker), "Enter")
			if msg.CmdErr != "" {
				b.Fatalf("send-keys failed: %s", msg.CmdErr)
			}

			msg = h.client.runCommand("wait", "content", "pane-1", marker, "--timeout", "30s")
			if msg.CmdErr != "" {
				b.Fatalf("wait-for failed: %s", msg.CmdErr)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// BenchmarkSplitScale — pane creation latency at various scales
// ---------------------------------------------------------------------------

func BenchmarkSplitScale(b *testing.B) {
	for _, n := range []int{4, 10, 20} {
		b.Run(fmt.Sprintf("amux/panes_%d", n), func(b *testing.B) {
			// Each iteration needs a fresh harness since splits accumulate.
			// StopTimer/StartTimer per iteration is a known anti-pattern that
			// can inflate b.N, but it's unavoidable here — the alternative
			// (timing setup+teardown) is worse. Eager cleanup prevents fd exhaustion.
			// Use root splits so space is distributed evenly (leaf splits halve
			// exponentially, hitting the minimum pane size at ~7 panes).
			for range b.N {
				b.StopTimer()
				h := newServerHarnessWithSize(b, 80, 200)
				b.StartTimer()
				for i := 1; i < n; i++ {
					h.splitRootH()
				}
				b.StopTimer()
				h.cleanup()
				b.StartTimer()
			}
		})

		b.Run(fmt.Sprintf("tmux/panes_%d", n), func(b *testing.B) {
			for range b.N {
				b.StopTimer()
				th := newTmuxBenchHarness(b)
				b.StartTimer()
				for i := 1; i < n; i++ {
					th.run("split-window", "-t", th.session)
				}
				b.StopTimer()
				th.cleanup()
				b.StartTimer()
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BenchmarkCaptureScale — capture latency with varying pane counts
// ---------------------------------------------------------------------------

func BenchmarkCaptureScale(b *testing.B) {
	for _, n := range []int{1, 4, 10, 20} {
		b.Run(fmt.Sprintf("amux/panes_%d", n), func(b *testing.B) {
			b.StopTimer()
			// Use root splits so space is distributed evenly (leaf splits halve
			// exponentially, hitting the minimum pane size at ~7 panes).
			h := newServerHarnessWithSize(b, 80, 200)
			for i := 1; i < n; i++ {
				h.splitRootH()
			}
			b.StartTimer()
			for b.Loop() {
				h.capture()
			}
		})

		b.Run(fmt.Sprintf("tmux/panes_%d", n), func(b *testing.B) {
			b.StopTimer()
			th := newTmuxBenchHarness(b)
			for i := 1; i < n; i++ {
				th.run("split-window", "-t", th.session)
			}
			b.StartTimer()
			for b.Loop() {
				th.run("capture-pane", "-t", th.session, "-p")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BenchmarkHotReload — amux-only: binary rebuild + client reconnect
// ---------------------------------------------------------------------------

func BenchmarkHotReload(b *testing.B) {
	b.StopTimer()
	h := newAmuxHarness(b)

	// Verify the inner amux is running
	if !h.waitFor("[pane-", 10*time.Second) {
		b.Fatal("inner amux did not render")
	}

	for range b.N {
		b.StopTimer()
		gen := h.generation()
		buildStart := time.Now()

		// Rebuild binary (triggers hot-reload)
		if err := buildAmux(amuxBin); err != nil {
			b.Fatalf("rebuild: %v", err)
		}
		buildDur := time.Since(buildStart)

		b.StartTimer()
		reconnectStart := time.Now()

		// Wait for re-render (layout generation bumps after reconnect).
		// Use 30s timeout — go build on CI can take 10-20s, and the
		// server re-exec + client reconnect adds more time.
		h.waitLayoutTimeout(gen, "30s")
		if !h.waitFor("[pane-", 30*time.Second) {
			b.Fatal("client did not reconnect after hot-reload")
		}

		reconnectDur := time.Since(reconnectStart)
		totalDur := buildDur + reconnectDur

		b.ReportMetric(float64(totalDur.Nanoseconds()), "total-ns/op")
		b.ReportMetric(float64(reconnectDur.Nanoseconds()), "reconnect-ns/op")
	}
}

// ---------------------------------------------------------------------------
// BenchmarkOutputDetection — polling vs push for pane output detection
// ---------------------------------------------------------------------------

// BenchmarkOutputDetection measures how quickly a caller can detect that a
// pane produced output. This is the core operation agents need — "did the
// command finish?"
//   - polling: fork `amux capture --format json` each iteration (CLI round-trip)
//   - push:    read from a persistent event stream subscription (zero fork overhead)
func BenchmarkOutputDetection(b *testing.B) {
	b.Run("polling", func(b *testing.B) {
		for _, panes := range []int{1, 4} {
			b.Run(fmt.Sprintf("panes_%d", panes), func(b *testing.B) {
				b.StopTimer()
				h := newServerHarness(b)
				for i := 1; i < panes; i++ {
					h.splitV()
				}
				b.StartTimer()
				b.ReportAllocs()
				for b.Loop() {
					h.runCmd("capture", "--format", "json")
				}
			})
		}
	})

	b.Run("push", func(b *testing.B) {
		for _, panes := range []int{1, 4} {
			b.Run(fmt.Sprintf("panes_%d", panes), func(b *testing.B) {
				b.StopTimer()
				h := newServerHarness(b)
				for i := 1; i < panes; i++ {
					h.splitV()
				}

				// Open persistent event stream filtered to layout events
				sockPath := server.SocketPath(h.session)
				conn, err := net.Dial("unix", sockPath)
				if err != nil {
					b.Fatalf("dial: %v", err)
				}
				defer conn.Close()

				writeMsgOnConn(conn, &server.Message{
					Type:    server.MsgTypeCommand,
					CmdName: "events",
					CmdArgs: []string{"--filter", "layout"},
				})

				pr, pw := net.Pipe()
				defer pr.Close()
				go func() {
					defer pw.Close()
					for {
						msg, err := readMsgOnConn(conn)
						if err != nil {
							return
						}
						if msg.CmdOutput != "" {
							pw.Write([]byte(msg.CmdOutput))
						}
					}
				}()
				scanner := bufio.NewScanner(pr)

				// Drain initial layout snapshot
				scanner.Scan()

				b.StartTimer()
				b.ReportAllocs()
				for b.Loop() {
					// Trigger a layout change (focus cycle), read the pushed event
					h.runCmd("focus", "next")
					scanner.Scan()
				}
			})
		}
	})
}

// ---------------------------------------------------------------------------
// BenchmarkDetectLayoutChange — polling vs push for layout change detection
// ---------------------------------------------------------------------------

// BenchmarkDetectLayoutChange measures the detection overhead after a layout
// change. The change (focus cycle) is synchronous — by the time the CLI
// returns, the server has already emitted the event. We measure how long
// it takes to confirm the change:
//   - polling: one `amux capture --format json` CLI round-trip (what agents do today)
//   - push:    one buffered read from an open event stream (near-zero overhead)
func BenchmarkDetectLayoutChange(b *testing.B) {
	b.Run("polling", func(b *testing.B) {
		b.StopTimer()
		h := newServerHarness(b)
		h.splitV() // need 2 panes for focus cycling
		b.StartTimer()
		b.ReportAllocs()
		for b.Loop() {
			// Simulate what a polling agent does each cycle:
			// call capture --format json and parse it.
			h.runCmd("capture", "--format", "json")
		}
	})

	b.Run("push", func(b *testing.B) {
		b.StopTimer()
		h := newServerHarness(b)
		h.splitV() // need 2 panes for focus cycling

		// Open persistent event stream (reused across iterations)
		sockPath := server.SocketPath(h.session)
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			b.Fatalf("dial: %v", err)
		}
		defer conn.Close()

		writeMsgOnConn(conn, &server.Message{
			Type:    server.MsgTypeCommand,
			CmdName: "events",
			CmdArgs: []string{"--filter", "layout"},
		})

		pr, pw := net.Pipe()
		defer pr.Close()
		go func() {
			defer pw.Close()
			for {
				msg, err := readMsgOnConn(conn)
				if err != nil {
					return
				}
				if msg.CmdOutput != "" {
					pw.Write([]byte(msg.CmdOutput))
				}
			}
		}()
		scanner := bufio.NewScanner(pr)

		// Drain initial layout snapshot
		scanner.Scan()

		b.StartTimer()
		b.ReportAllocs()
		for b.Loop() {
			// Trigger a layout change (focus next), then read the pushed event
			h.runCmd("focus", "next")
			scanner.Scan()
		}
	})
}
