package proto

import (
	"encoding/gob"
	"image/color"

	"github.com/charmbracelet/x/ansi"
)

func init() {
	// Register concrete color types so gob can encode/decode the
	// color.Color interface fields in uv.Style (used by StyledLine.Cells).
	// Without these registrations, gob encoding of MsgTypePaneHistory
	// messages containing styled cells fails with
	// "type not registered for interface".
	gob.Register(ansi.BasicColor(0))
	gob.Register(ansi.IndexedColor(0))
	gob.Register(ansi.TrueColor(0))
	gob.Register(ansi.RGBColor{})
	gob.Register(color.RGBA{})
}
