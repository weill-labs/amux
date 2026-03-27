package test

import "strings"

func isCommandConnectError(out string) bool {
	return strings.Contains(out, "connecting to server:")
}
