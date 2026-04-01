package termprofile

import (
	"fmt"
	"io"
	"strings"

	"github.com/muesli/termenv"
)

const EnvKey = "AMUX_COLOR_PROFILE"

type emptyEnviron struct{}

func (emptyEnviron) Environ() []string {
	return nil
}

func (emptyEnviron) Getenv(string) string {
	return ""
}

func Parse(value string) (termenv.Profile, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "truecolor":
		return termenv.TrueColor, true
	case "ansi256":
		return termenv.ANSI256, true
	case "ansi":
		return termenv.ANSI, true
	case "ascii":
		return termenv.Ascii, true
	default:
		return termenv.Ascii, false
	}
}

func Format(profile termenv.Profile) string {
	switch profile {
	case termenv.TrueColor:
		return "TrueColor"
	case termenv.ANSI256:
		return "ANSI256"
	case termenv.ANSI:
		return "ANSI"
	case termenv.Ascii:
		return "Ascii"
	default:
		return profile.Name()
	}
}

func Detect(output io.Writer, environ termenv.Environ, outputOpts ...termenv.OutputOption) termenv.Profile {
	if environ == nil {
		environ = emptyEnviron{}
	}

	opts := []termenv.OutputOption{termenv.WithEnvironment(environ)}
	opts = append(opts, outputOpts...)
	profile := termenv.NewOutput(output, opts...).EnvColorProfile()
	return fallbackProfile(profile, environ)
}

func DetectFromEnvironment(environ termenv.Environ) termenv.Profile {
	return Detect(io.Discard, environ, termenv.WithTTY(true))
}

func EnvEntry(environ termenv.Environ) string {
	return fmt.Sprintf("%s=%s", EnvKey, Format(DetectFromEnvironment(environ)))
}

func fallbackProfile(profile termenv.Profile, environ termenv.Environ) termenv.Profile {
	if profile != termenv.Ascii || envNoColor(environ) {
		return profile
	}
	if inherited, ok := Parse(environ.Getenv(EnvKey)); ok {
		return inherited
	}
	if strings.EqualFold(environ.Getenv("TERM"), "amux") {
		return termenv.ANSI256
	}
	return profile
}

func envNoColor(environ termenv.Environ) bool {
	if environ == nil {
		return false
	}
	if environ.Getenv("NO_COLOR") != "" {
		return true
	}
	if environ.Getenv("CLICOLOR") != "0" {
		return false
	}
	forced := environ.Getenv("CLICOLOR_FORCE")
	return forced == "" || forced == "0"
}
