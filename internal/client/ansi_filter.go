package client

import (
	"strings"

	"github.com/weill-labs/amux/internal/proto"
)

// filterRenderedANSI strips escape sequences the attached client did not
// negotiate support for, while preserving visible text content.
func filterRenderedANSI(rendered string, caps proto.ClientCapabilities) string {
	if caps.Hyperlinks {
		return rendered
	}
	return stripOSC8Hyperlinks(rendered)
}

// stripOSC8Hyperlinks removes OSC 8 hyperlink open/close sequences while
// leaving the linked text intact.
func stripOSC8Hyperlinks(s string) string {
	var out strings.Builder
	out.Grow(len(s))

	for i := 0; i < len(s); {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == ']' {
			end, payload, ok := parseOSCSequence(s, i)
			if ok {
				if strings.HasPrefix(payload, "8;") {
					i = end
					continue
				}
				out.WriteString(s[i:end])
				i = end
				continue
			}
		}

		out.WriteByte(s[i])
		i++
	}

	return out.String()
}

// parseOSCSequence parses one OSC sequence starting at s[i]=='\033' and
// s[i+1]==']'. It returns the end index (exclusive), payload, and whether a
// complete OSC terminator was found.
func parseOSCSequence(s string, i int) (int, string, bool) {
	j := i + 2
	for j < len(s) {
		if s[j] == '\007' {
			return j + 1, s[i+2 : j], true
		}
		if s[j] == '\033' && j+1 < len(s) && s[j+1] == '\\' {
			return j + 2, s[i+2 : j], true
		}
		j++
	}
	return 0, "", false
}
