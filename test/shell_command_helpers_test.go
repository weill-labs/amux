package test

import "strings"

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func nestedAmuxCommand(binPath, session string, args ...string) string {
	parts := make([]string, 0, len(args)+3)
	parts = append(parts, shellSingleQuote(binPath), "-s", shellSingleQuote(session))
	for _, arg := range args {
		parts = append(parts, shellSingleQuote(arg))
	}
	return strings.Join(parts, " ")
}
