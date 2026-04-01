package render

import (
	"fmt"
	"strconv"

	"github.com/charmbracelet/colorprofile"
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
	if from == nil {
		return uv.StyleDiff(nil, &convertedTo)
	}

	convertedFrom := uv.ConvertStyle(*from, uvColorProfile(profile))
	return uv.StyleDiff(&convertedFrom, &convertedTo)
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
