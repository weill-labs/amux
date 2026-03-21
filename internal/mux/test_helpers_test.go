package mux

import "regexp"

func NewVTEmulator(width, height int) TerminalEmulator {
	return NewVTEmulatorWithScrollback(width, height, DefaultScrollbackLines)
}

var testANSIRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[()][0-9A-B]`)

func StripANSI(s string) string {
	return testANSIRe.ReplaceAllString(s, "")
}
