package render

// skipANSISequence advances past an ANSI escape sequence starting at s[i].
// Returns the index of the first byte after the sequence.
// If s[i] is not ESC (\033), returns i unchanged.
func skipANSISequence(s string, i int) int {
	if i >= len(s) || s[i] != '\033' || i+1 >= len(s) {
		return i
	}
	next := s[i+1]

	// CSI: \033[ params final_byte
	if next == '[' {
		j := i + 2
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3F {
			j++
		}
		if j < len(s) {
			return j + 1 // skip final byte
		}
		return j
	}

	// OSC: \033] ... BEL(\007) or ST(\033\\)
	if next == ']' {
		j := i + 2
		for j < len(s) {
			if s[j] == '\007' {
				return j + 1
			}
			if s[j] == '\033' && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return j
	}

	// Other ESC sequences (charset designation etc.) — ESC + one byte
	return i + 2
}

// CSIParams returns the parameter string and final byte of a CSI sequence
// starting at s[i] (which must be the '[' after ESC). Returns ("", 0, i)
// if the sequence is malformed. On success returns (params, finalByte, endIndex).
func CSIParams(s string, i int) (string, byte, int) {
	j := i
	for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3F {
		j++
	}
	if j >= len(s) {
		return "", 0, i
	}
	return s[i:j], s[j], j + 1
}
