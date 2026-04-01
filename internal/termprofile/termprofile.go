package termprofile

import (
	"fmt"
	"io"
	"strings"

	"github.com/muesli/termenv"
)

const EnvKey = "AMUX_COLOR_PROFILE"

var profileNames = map[termenv.Profile]string{
	termenv.TrueColor: "TrueColor",
	termenv.ANSI256:   "ANSI256",
	termenv.ANSI:      "ANSI",
	termenv.Ascii:     "Ascii",
}

var profileValues = map[string]termenv.Profile{
	"truecolor": termenv.TrueColor,
	"ansi256":   termenv.ANSI256,
	"ansi":      termenv.ANSI,
	"ascii":     termenv.Ascii,
}

type emptyEnviron struct{}

func (emptyEnviron) Environ() []string {
	return nil
}

func (emptyEnviron) Getenv(string) string {
	return ""
}

func Parse(value string) (termenv.Profile, bool) {
	profile, ok := profileValues[strings.ToLower(strings.TrimSpace(value))]
	if !ok {
		return termenv.Ascii, false
	}
	return profile, true
}

func Format(profile termenv.Profile) string {
	if name, ok := profileNames[profile]; ok {
		return name
	}
	return profile.Name()
}

func Detect(output io.Writer, environ termenv.Environ, outputOpts ...termenv.OutputOption) termenv.Profile {
	if environ == nil {
		environ = emptyEnviron{}
	}
	if inherited, ok := inheritedProfile(environ); ok {
		return inherited
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
	if profile != termenv.Ascii {
		return profile
	}
	if inherited, ok := inheritedProfile(environ); ok {
		return inherited
	}
	return profile
}

func inheritedProfile(environ termenv.Environ) (termenv.Profile, bool) {
	if envNoColor(environ) {
		return termenv.Ascii, true
	}
	if inherited, ok := Parse(environ.Getenv(EnvKey)); ok {
		return inherited, true
	}
	if strings.EqualFold(environ.Getenv("TERM"), "amux") {
		return termenv.ANSI256, true
	}
	return termenv.Ascii, false
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
