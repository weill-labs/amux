package render

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/config"
)

const (
	HideCursor = ansi.HideCursor
	ShowCursor = ansi.ShowCursor
	ClearAll   = ansi.EraseEntireScreen
	Reset      = ansi.ResetStyle

	MouseEnable  = ansi.SetModeMouseButtonEvent + ansi.SetModeMouseExtSgr
	MouseDisable = ansi.ResetModeMouseExtSgr + ansi.ResetModeMouseButtonEvent

	FocusEnable  = ansi.SetModeFocusEvent
	FocusDisable = ansi.ResetModeFocusEvent

	AltScreenEnter = ansi.SetModeAltScreenSaveCursor
	AltScreenExit  = ansi.ResetModeAltScreenSaveCursor
)

var (
	Bold   = ansi.SGR(ansi.BoldAttr)
	NoBold = ansi.SGR(ansi.NormalIntensityAttr)

	StrikeOn = ansi.SGR(ansi.StrikethroughAttr)

	DimFg      = foregroundSequence(config.DimColorHex)
	Surface0Bg = backgroundSequence(config.Surface0Hex)
	TextFg     = foregroundSequence(config.TextColorHex)
	GreenFg    = foregroundSequence(config.GreenHex)
	BlueFg     = foregroundSequence(config.BlueHex)
	YellowFg   = foregroundSequence(config.YellowHex)
	RedFg      = foregroundSequence(config.RedHex)

	KittyKeyboardEnable  = ansi.PushKittyKeyboard(1)
	KittyKeyboardDisable = ansi.DisableKittyKeyboard

	ResetTitle = ansi.SetIconNameWindowTitle("")
)

func foregroundSequence(hex string) string {
	return ansi.NewStyle().ForegroundColor(hexToColor(hex)).String()
}

func backgroundSequence(hex string) string {
	return ansi.NewStyle().BackgroundColor(hexToColor(hex)).String()
}

// writeCursorTo writes a cursor-position escape directly into buf.
func writeCursorTo(buf *strings.Builder, row, col int) {
	buf.WriteString(ansi.CursorPosition(col, row))
}
