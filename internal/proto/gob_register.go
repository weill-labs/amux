package proto

import (
	"encoding/gob"
	"image/color"

	"github.com/charmbracelet/x/ansi"
)

func init() {
	// Register concrete color types so gob can encode/decode the
	// color.Color interface fields in StyledLine.Cells.
	gob.Register(ansi.BasicColor(0))
	gob.Register(ansi.IndexedColor(0))
	gob.Register(ansi.RGBColor{})
	gob.Register(color.RGBA{})
	gob.Register(LayoutSnapshot{})
	gob.Register(WindowSnapshot{})
	gob.Register(PaneSnapshot{})
	gob.Register(CellSnapshot{})
}
