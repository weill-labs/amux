package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/config"
)

type catppuccinMochaLipGlossPalette struct {
	surface0 lipgloss.Color
	text     lipgloss.Color
	dim      lipgloss.Color
	blue     lipgloss.Color
	green    lipgloss.Color
	yellow   lipgloss.Color
	red      lipgloss.Color
}

func newCatppuccinMochaLipGlossPalette() catppuccinMochaLipGlossPalette {
	return catppuccinMochaLipGlossPalette{
		surface0: hexToLipGlossColor(config.Surface0Hex),
		text:     hexToLipGlossColor(config.TextColorHex),
		dim:      hexToLipGlossColor(config.DimColorHex),
		blue:     hexToLipGlossColor(config.BlueHex),
		green:    hexToLipGlossColor(config.GreenHex),
		yellow:   hexToLipGlossColor(config.YellowHex),
		red:      hexToLipGlossColor(config.RedHex),
	}
}

type statusBarStyles struct {
	background    lipgloss.Style
	title         lipgloss.Style
	dim           lipgloss.Style
	active        lipgloss.Style
	activeBold    lipgloss.Style
	focused       lipgloss.Style
	idle          lipgloss.Style
	busy          lipgloss.Style
	warning       lipgloss.Style
	success       lipgloss.Style
	error         lipgloss.Style
	completedMeta lipgloss.Style
}

func newStatusBarStyles(accentHex string) statusBarStyles {
	return newStatusBarStylesWithBg(accentHex, false)
}

func newStatusBarStylesWithBg(accentHex string, pressed bool) statusBarStyles {
	palette := newCatppuccinMochaLipGlossPalette()
	bgColor := palette.surface0
	if pressed {
		bgColor = hexToLipGlossColor(config.Surface1Hex)
	}
	base := lipgloss.NewStyle().
		Inline(true).
		ColorWhitespace(true).
		Background(bgColor)
	active := base.Foreground(hexToLipGlossColor(accentHex))
	dim := base.Foreground(palette.dim)
	busy := base.Foreground(palette.text)

	return statusBarStyles{
		background:    base,
		title:         busy.Bold(true),
		dim:           dim,
		active:        active,
		activeBold:    active.Bold(true),
		focused:       base.Foreground(palette.blue).Bold(true),
		idle:          dim,
		busy:          busy,
		warning:       base.Foreground(palette.yellow),
		success:       base.Foreground(palette.green),
		error:         base.Foreground(palette.red),
		completedMeta: dim.Strikethrough(true),
	}
}

func (s statusBarStyles) pane(role paneStatusSegmentRole) lipgloss.Style {
	switch role {
	case paneStatusSegmentPane:
		return s.active
	case paneStatusSegmentPaneBold:
		return s.activeBold
	case paneStatusSegmentDim:
		return s.idle
	case paneStatusSegmentText:
		return s.busy
	case paneStatusSegmentYellow:
		return s.warning
	case paneStatusSegmentGreen:
		return s.success
	case paneStatusSegmentRed:
		return s.error
	case paneStatusSegmentCompletedMeta:
		return s.completedMeta
	default:
		return s.background
	}
}

func (s statusBarStyles) windowTab(window WindowInfo) lipgloss.Style {
	if window.IsActive {
		return s.focused
	}
	return s.busy
}

func renderStyledText(style lipgloss.Style, text string) string {
	return renderStyledTextWithProfile(style, text, defaultColorProfile)
}

func renderStyledTextWithProfile(style lipgloss.Style, text string, profile termenv.Profile) string {
	if text == "" {
		return ""
	}
	return styleANSIWithProfile(style, profile) + text + Reset
}

func writeStyledText(buf *strings.Builder, style lipgloss.Style, text string) {
	writeStyledTextWithProfile(buf, style, text, defaultColorProfile)
}

func writeStyledTextWithProfile(buf *strings.Builder, style lipgloss.Style, text string, profile termenv.Profile) {
	if text == "" {
		return
	}
	buf.WriteString(renderStyledTextWithProfile(style, text, profile))
}

func styleANSI(style lipgloss.Style) string {
	return styleANSIWithProfile(style, defaultColorProfile)
}

func styleANSIWithProfile(style lipgloss.Style, profile termenv.Profile) string {
	var buf strings.Builder
	buf.WriteString(Reset)

	if bg := lipGlossColorHex(style.GetBackground()); bg != "" {
		buf.WriteString(bgHexSequence(bg, profile))
	}
	if fg := lipGlossColorHex(style.GetForeground()); fg != "" {
		buf.WriteString(fgHexSequence(fg, profile))
	}
	if style.GetBold() {
		buf.WriteString(Bold)
	}
	if style.GetStrikethrough() {
		buf.WriteString(StrikeOn)
	}

	return buf.String()
}

func lipGlossColorHex(c lipgloss.TerminalColor) string {
	if c == nil {
		return ""
	}
	return strings.TrimPrefix(fmt.Sprint(c), "#")
}
