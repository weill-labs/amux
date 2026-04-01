package render

import (
	"fmt"
	"image/color"
	"strconv"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/muesli/termenv"
)

const defaultColorProfile = termenv.TrueColor

func uvColorProfile(profile termenv.Profile) colorprofile.Profile {
	switch profile {
	case termenv.TrueColor:
		return colorprofile.TrueColor
	case termenv.ANSI256:
		return colorprofile.ANSI256
	case termenv.ANSI:
		return colorprofile.ANSI
	case termenv.Ascii:
		return colorprofile.ASCII
	default:
		return colorprofile.TrueColor
	}
}

func styleDiffWithProfile(from *uv.Style, to uv.Style, profile termenv.Profile) string {
	convertedTo := uv.ConvertStyle(to, uvColorProfile(profile))
	baseTo := convertedTo
	baseTo.Underline = uv.UnderlineNone
	baseTo.UnderlineColor = nil
	if from == nil {
		baseDiff := uv.StyleDiff(nil, &baseTo)
		return baseDiff + underlineDiffSequence(nil, convertedTo, baseDiff == ansi.ResetStyle)
	}

	convertedFrom := uv.ConvertStyle(*from, uvColorProfile(profile))
	baseFrom := convertedFrom
	baseFrom.Underline = uv.UnderlineNone
	baseFrom.UnderlineColor = nil

	baseDiff := uv.StyleDiff(&baseFrom, &baseTo)
	return baseDiff + underlineDiffSequence(&convertedFrom, convertedTo, baseDiff == ansi.ResetStyle)
}

func underlineDiffSequence(from *uv.Style, to uv.Style, baseReset bool) string {
	var currentUnderline uv.Underline
	var currentUnderlineColor color.Color
	if !baseReset && from != nil {
		currentUnderline = from.Underline
		currentUnderlineColor = from.UnderlineColor
	}

	var seq ansi.Style
	if currentUnderline != to.Underline {
		seq = seq.UnderlineStyle(to.Underline)
	}
	if !underlineColorEqual(currentUnderlineColor, to.UnderlineColor) {
		seq = seq.UnderlineColor(to.UnderlineColor)
	}
	if len(seq) == 0 {
		return ""
	}
	return seq.String()
}

func underlineColorEqual(a, b color.Color) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func colorSequence(hex string, bg bool, profile termenv.Profile) string {
	if len(hex) < 6 {
		return ""
	}
	if profile == defaultColorProfile {
		return trueColorSequence(hex, bg)
	}

	seq := profile.Color("#" + hex).Sequence(bg)
	if seq == "" {
		return ""
	}
	return "\033[" + seq + "m"
}

func trueColorSequence(hex string, bg bool) string {
	r, _ := strconv.ParseUint(hex[0:2], 16, 8)
	g, _ := strconv.ParseUint(hex[2:4], 16, 8)
	b, _ := strconv.ParseUint(hex[4:6], 16, 8)
	prefix := 38
	if bg {
		prefix = 48
	}
	return fmt.Sprintf("\033[%d;2;%d;%d;%dm", prefix, r, g, b)
}

func fgHexSequence(hex string, profile termenv.Profile) string {
	return colorSequence(hex, false, profile)
}

func bgHexSequence(hex string, profile termenv.Profile) string {
	return colorSequence(hex, true, profile)
}
