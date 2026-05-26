package config

import (
	"testing"
)

// FuzzParseConfig seeds the config parser with the accepted top-level tables,
// validation failures, legacy keybinding sections, type mismatches, long
// comments, and raw bytes. The seed corpus keeps CI focused on the TOML and
// amux validation branches; opt-in fuzzing mutates arbitrary config bytes
// around those examples.
func FuzzParseConfig(f *testing.F) {
	for _, seed := range [][]byte{
		nil,
		[]byte(""),
		[]byte("scrollback_lines = 2048\n"),
		[]byte("[debug]\npprof = true\n"),
		[]byte("[client]\nlocal_echo = \"always\"\nlocal_echo_style = \"underline\"\n"),
		[]byte("[client]\nlocal_echo = \"maybe\"\n"),
		[]byte("[client]\nlocal_echo_style = \"flashy\"\n"),
		[]byte("[theme]\nstatus_style = \"powerline\"\n"),
		[]byte("[theme]\nstatus_style = \"fancy\"\n"),
		[]byte("[theme]\nicons = \"unicode\"\n"),
		[]byte("[theme]\nicons = \"powerline\"\n"),
		[]byte("[theme]\nicons = \"\"\n"),
		[]byte("scrollback_lines = 0\n"),
		[]byte("[keys]\nprefix = \"C-b\"\n"),
		[]byte("[keys.bind]\ns = \"split\"\n"),
		[]byte("scrollback_lines = \"many\"\n"),
		[]byte("#00000000000000000000000000000000000000000000000000000000000000IIIIIIIIIIIIIIIIIIIIIIII"),
		[]byte("["),
		[]byte{0xff, 0x00, 't', 'o', 'm', 'l'},
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		cfg, err := parseConfig(data)
		if err != nil {
			if cfg != nil {
				t.Fatalf("parse returned config %+v with error %v, want nil config on error", cfg, err)
			}
			return
		}
		assertParsedConfigValid(t, cfg)
	})
}

func assertParsedConfigValid(t *testing.T, cfg *Config) {
	t.Helper()

	if cfg == nil {
		t.Fatal("parse returned nil config with nil error")
	}
	if _, err := ResolveScrollbackLines(cfg.ScrollbackLines); err != nil {
		t.Fatalf("global scrollback did not validate after parse: %v", err)
	}
	if _, err := ResolveLocalEchoMode(cfg.Client.LocalEcho); err != nil {
		t.Fatalf("local_echo did not validate after parse: %v", err)
	}
	if _, err := ResolveLocalEchoStyle(cfg.Client.LocalEchoStyle); err != nil {
		t.Fatalf("local_echo_style did not validate after parse: %v", err)
	}
	if _, err := ResolveStatusStyle(cfg.Theme.StatusStyle); err != nil {
		t.Fatalf("status_style did not validate after parse: %v", err)
	}
	if _, err := ResolveThemeIcons(cfg.Theme.Icons); err != nil {
		t.Fatalf("theme.icons did not validate after parse: %v", err)
	}
}
