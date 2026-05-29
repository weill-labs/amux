package render

import (
	"strings"

	"github.com/muesli/termenv"
)

// textInputChrome converts a TextInputOverlay into the shared dialogChrome.
// The input is shown on a "> " line; the footer advertises the confirm/cancel keys.
func textInputChrome(overlay *TextInputOverlay) dialogChrome {
	return dialogChrome{
		title:     overlay.Title,
		showQuery: true,
		query:     overlay.Input,
		footer:    []footerHint{{key: "enter", label: "confirm"}, {key: "esc", label: "cancel"}},
	}
}

func buildTextInputOverlayCells(g *ScreenGrid, overlay *TextInputOverlay) {
	if overlay == nil {
		return
	}
	textInputChrome(overlay).place(g, defaultDialogStyles())
}

func renderTextInputOverlay(buf *strings.Builder, width, height int, overlay *TextInputOverlay) {
	renderTextInputOverlayWithProfile(buf, width, height, overlay, defaultColorProfile)
}

func renderTextInputOverlayWithProfile(buf *strings.Builder, width, height int, overlay *TextInputOverlay, profile termenv.Profile) {
	if overlay == nil {
		return
	}
	emitDialogChrome(buf, width, height, textInputChrome(overlay), profile)
}
