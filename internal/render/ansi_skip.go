package render

import (
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

var ansiParserPool = sync.Pool{
	New: func() any {
		return ansi.NewParser()
	},
}

func decodeANSISequence(s string, i int) (ansi.Cmd, ansi.Params, int, bool) {
	if i < 0 || i >= len(s) {
		return 0, nil, 0, false
	}

	parser := ansiParserPool.Get().(*ansi.Parser)
	defer ansiParserPool.Put(parser)

	parser.Reset()
	_, _, n, _ := ansi.DecodeSequence(s[i:], ansi.NormalState, parser)
	if n <= 0 {
		return 0, nil, 0, false
	}

	return ansi.Cmd(parser.Command()), parser.Params(), n, true
}

// skipANSISequence advances past an ANSI escape sequence starting at s[i].
// Returns the index of the first byte after the sequence.
// If s[i] is not ESC (\033), returns i unchanged.
func skipANSISequence(s string, i int) int {
	if i >= len(s) || s[i] != '\033' {
		return i
	}
	_, _, n, ok := decodeANSISequence(s, i)
	if !ok {
		return i
	}
	return i + n
}

// CSIParams returns the parameter string and final byte of a CSI sequence
// starting at s[i] (which must be the byte after '['). On success it returns
// (params, finalByte, endIndex) where endIndex is one past the final byte.
// Truncated sequences preserve any parameters collected before parsing stopped.
func CSIParams(s string, i int) (string, byte, int) {
	start := i - 2
	if i < 2 || start < 0 || s[start] != '\033' || s[start+1] != '[' {
		return "", 0, i
	}
	cmd, params, n, ok := decodeANSISequence(s, start)
	if !ok {
		return "", 0, i
	}
	return csiParamString(cmd, params), cmd.Final(), start + n
}

func csiParamString(cmd ansi.Cmd, params ansi.Params) string {
	var buf strings.Builder

	if prefix := cmd.Prefix(); prefix != 0 {
		buf.WriteByte(prefix)
	}

	for i, param := range params {
		if i > 0 {
			if params[i-1].HasMore() {
				buf.WriteByte(':')
			} else {
				buf.WriteByte(';')
			}
		}
		if value := param.Param(-1); value >= 0 {
			buf.WriteString(strconv.Itoa(value))
		}
	}

	if inter := cmd.Intermediate(); inter != 0 {
		buf.WriteByte(inter)
	}

	return buf.String()
}
