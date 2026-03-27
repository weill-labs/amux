package proto

// AttachMode controls whether an attached client participates in session size
// negotiation. The zero value preserves the legacy interactive default so
// older clients remain compatible with newer servers.
type AttachMode uint8

const (
	// AttachModeDefault exists for wire compatibility with older clients that
	// omitted the field entirely. New code should use AttachModeInteractive or
	// AttachModeNonInteractive explicitly.
	AttachModeDefault AttachMode = iota
	AttachModeInteractive
	AttachModeNonInteractive
)

func (m AttachMode) IsInteractive() bool {
	return m != AttachModeNonInteractive
}
