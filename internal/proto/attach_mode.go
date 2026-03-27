package proto

// AttachMode controls whether an attached client participates in session size
// negotiation. The zero value preserves the legacy interactive default so
// older clients remain compatible with newer servers.
type AttachMode uint8

const (
	AttachModeDefault AttachMode = iota
	AttachModeInteractive
	AttachModeNonInteractive
)

func (m AttachMode) IsInteractive() bool {
	return m != AttachModeNonInteractive
}
