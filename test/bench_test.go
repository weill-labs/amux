package test

import (
	"crypto/rand"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
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

// ---------------------------------------------------------------------------
// BenchmarkThroughput — high-bandwidth output rendering
// ---------------------------------------------------------------------------

func BenchmarkThroughput(b *testing.B) {
	b.Run("amux", func(b *testing.B) {
		b.StopTimer()
		h := newServerHarness(b)
		b.StartTimer()
		for i := range b.N {
			marker := fmt.Sprintf("DONE-%04d", i)
			h.sendKeys("pane-1", fmt.Sprintf("seq 1 10000; echo %s", marker), "Enter")
			h.waitFor("pane-1", marker)
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
			for range b.N {
				b.StopTimer()
				h := newServerHarness(b)
				b.StartTimer()
				for i := 1; i < n; i++ {
					h.splitV()
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
			h := newServerHarness(b)
			for i := 1; i < n; i++ {
				h.splitV()
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
	if !h.waitFor("[pane-", 5*time.Second) {
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

		// Wait for re-render (layout generation bumps after reconnect)
		h.waitLayout(gen)
		if !h.waitFor("[pane-", 10*time.Second) {
			b.Fatal("client did not reconnect after hot-reload")
		}

		reconnectDur := time.Since(reconnectStart)
		totalDur := buildDur + reconnectDur

		b.ReportMetric(float64(totalDur.Nanoseconds()), "total-ns/op")
		b.ReportMetric(float64(reconnectDur.Nanoseconds()), "reconnect-ns/op")
	}
}
