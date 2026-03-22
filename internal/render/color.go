package render

import (
	"fmt"
	"math"
	"strconv"

	"github.com/weill-labs/amux/internal/config"
)

// Status bar background blend ratios.
const (
	statusBarActiveBgBlend   = 0.25
	statusBarInactiveBgBlend = 0.12
)

// statusBarBgHex returns the blended background hex for a pane status bar.
// Both the ANSI and cell-grid renderers call this to guarantee identical output.
func statusBarBgHex(paneColor string, active bool) string {
	if active {
		return blendHex(paneColor, config.Surface0Hex, statusBarActiveBgBlend)
	}
	return blendHex(paneColor, config.Surface0Hex, statusBarInactiveBgBlend)
}

// blendHex mixes two 6-digit lowercase hex colors channel-by-channel.
// ratio=0.0 returns bg, ratio=1.0 returns fg. Clamps ratio to [0,1].
// Invalid or empty fg falls back to bg unchanged.
// Uses math.Round for deterministic rounding.
func blendHex(fg, bg string, ratio float64) string {
	if len(fg) < 6 {
		return bg
	}
	if ratio <= 0 {
		return bg
	}
	if ratio >= 1 {
		return fg
	}
	fgR, err1 := strconv.ParseUint(fg[0:2], 16, 8)
	fgG, err2 := strconv.ParseUint(fg[2:4], 16, 8)
	fgB, err3 := strconv.ParseUint(fg[4:6], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return bg
	}
	bgR, err4 := strconv.ParseUint(bg[0:2], 16, 8)
	bgG, err5 := strconv.ParseUint(bg[2:4], 16, 8)
	bgB, err6 := strconv.ParseUint(bg[4:6], 16, 8)
	if err4 != nil || err5 != nil || err6 != nil {
		return bg
	}
	r := uint8(math.Round(float64(bgR) + float64(int64(fgR)-int64(bgR))*ratio))
	g := uint8(math.Round(float64(bgG) + float64(int64(fgG)-int64(bgG))*ratio))
	b := uint8(math.Round(float64(bgB) + float64(int64(fgB)-int64(bgB))*ratio))
	return fmt.Sprintf("%02x%02x%02x", r, g, b)
}
