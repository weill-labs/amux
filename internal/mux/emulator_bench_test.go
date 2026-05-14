package mux

import (
	"fmt"
	"strings"
	"testing"
)

// realisticTerminalPayload generates n bytes of colored text with ANSI SGR
// escape sequences, simulating real terminal output (shell prompt + ls output).
func realisticTerminalPayload(n int) []byte {
	// Realistic ANSI SGR sequences interleaved with text
	fragments := []string{
		"\033[32m$ \033[0mls -la\r\n",
		"\033[1;34mdrwxr-xr-x\033[0m  5 user staff  160 Mar 15 10:00 \033[1;34m.\033[0m\r\n",
		"\033[1;34mdrwxr-xr-x\033[0m  3 user staff   96 Mar 14 09:00 \033[1;34m..\033[0m\r\n",
		"\033[0;32m-rw-r--r--\033[0m  1 user staff 1234 Mar 15 10:00 \033[0mmain.go\033[0m\r\n",
		"\033[0;32m-rw-r--r--\033[0m  1 user staff  567 Mar 14 09:30 \033[0;33mREADME.md\033[0m\r\n",
		"\033[0;32m-rwxr-xr-x\033[0m  1 user staff 8901 Mar 15 09:50 \033[0;31mamux\033[0m\r\n",
	}

	buf := make([]byte, 0, n)
	for len(buf) < n {
		for _, f := range fragments {
			buf = append(buf, f...)
			if len(buf) >= n {
				break
			}
		}
	}
	return buf[:n]
}

func BenchmarkEmulatorWrite(b *testing.B) {
	for _, size := range []int{256, 4096, 32768} {
		b.Run(fmt.Sprintf("bytes_%d", size), func(b *testing.B) {
			payload := realisticTerminalPayload(size)
			emu := NewVTEmulatorWithDrain(80, 24)

			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				mustWrite(b, emu, payload)
			}
		})
	}
}

var benchmarkParserBufferWorkloadSink []TerminalEmulator

func BenchmarkPaneParserDataBufferWorkload(b *testing.B) {
	const (
		panes       = 16
		chunkSize   = 1024
		chunkCount  = 60
		paneWidth   = 120
		paneHeight  = 40
		payloadSize = chunkSize * chunkCount
	)

	payload := realisticTerminalPayload(payloadSize)
	chunks := make([][]byte, 0, chunkCount)
	for offset := 0; offset < len(payload); offset += chunkSize {
		chunks = append(chunks, payload[offset:offset+chunkSize])
	}

	b.SetBytes(int64(panes * payloadSize))
	b.ReportAllocs()
	for b.Loop() {
		emulators := make([]TerminalEmulator, panes)
		for pane := range panes {
			emu := NewVTEmulatorWithScrollback(paneWidth, paneHeight, DefaultScrollbackLines)
			for _, chunk := range chunks {
				mustWrite(b, emu, chunk)
			}
			emulators[pane] = emu
		}
		for _, emu := range emulators {
			_ = emu.Close()
		}
		benchmarkParserBufferWorkloadSink = emulators
	}
}

func BenchmarkEmulatorRender(b *testing.B) {
	emu := NewVTEmulatorWithDrain(80, 24)

	// Write realistic 80x24 content once in setup
	payload := realisticTerminalPayload(80 * 24)
	mustWrite(b, emu, payload)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		emu.Render()
	}
}

func BenchmarkEmulatorContentLines(b *testing.B) {
	emu := NewVTEmulatorWithDrain(80, 24)
	payload := realisticTerminalPayload(80 * 24)
	mustWrite(b, emu, payload)

	b.Run("Render+StripANSI", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			rendered := emu.Render()
			lines := strings.Split(rendered, "\n")
			for i, line := range lines {
				lines[i] = StripANSI(strings.TrimRight(line, " "))
			}
		}
	})

	b.Run("ScreenLineText", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			EmulatorContentLines(emu)
		}
	})
}

func BenchmarkScrollbackLineText(b *testing.B) {
	const (
		width           = 200
		height          = 60
		scrollbackLines = 5000
	)

	emu := NewVTEmulatorWithScrollback(width, height, scrollbackLines)
	defer emu.Close()

	var payload strings.Builder
	wideText := strings.Repeat("scrollback payload ", 16)
	for line := 0; line < scrollbackLines+height; line++ {
		fmt.Fprintf(&payload, "\033[3%dm%04d %s\033[0m\r\n", line%8, line, wideText)
	}
	mustWrite(b, emu, []byte(payload.String()))

	rows := emu.ScrollbackLen()
	if rows == 0 {
		b.Fatal("expected scrollback rows")
	}

	total := 0
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		total += len(emu.ScrollbackLineText(i % rows))
	}
	if total == 0 {
		b.Fatal("expected scrollback text")
	}
}

func BenchmarkScreenContains(b *testing.B) {
	emu := NewVTEmulatorWithDrain(80, 24)
	payload := realisticTerminalPayload(80 * 24)
	mustWrite(b, emu, payload)

	// Search for a string that appears near the bottom of the screen
	target := "README.md"

	b.Run("Render+StripANSI+Contains", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			strings.Contains(StripANSI(emu.Render()), target)
		}
	})

	b.Run("ScreenContains", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			emu.ScreenContains(target)
		}
	})
}
