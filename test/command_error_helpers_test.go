package test

import "strings"

func isCommandConnectError(out string) bool {
	return strings.Contains(out, "connecting to server:")
}

func isCaptureUnavailable(out string) bool {
	return isCommandConnectError(out) ||
		strings.Contains(out, "amux capture: EOF") ||
		strings.Contains(out, "no client attached")
}
