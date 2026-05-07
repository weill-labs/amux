package config

import (
	"reflect"
	"testing"
)

// FuzzParseConfig seeds the config parser with the accepted top-level tables,
// host tables, validation failures, legacy keybinding sections, type
// mismatches, quoted host names, duplicate transport preferences, long
// comments, and raw bytes. The seed corpus keeps CI focused on the TOML and
// amux validation branches; opt-in fuzzing mutates arbitrary config bytes
// around those examples.
func FuzzParseConfig(f *testing.F) {
	for _, seed := range [][]byte{
		nil,
		[]byte(""),
		[]byte(`
[hosts.lambda-a100]
type = "remote"
user = "ubuntu"
address = "150.136.64.231"
project_dir = "~/Project"
gpu = "A100"
color = "f38ba8"

[hosts.macbook]
type = "local"
`),
		[]byte("scrollback_lines = 2048\n"),
		[]byte(`
scrollback_lines = 2048

[hosts.local]
type = "local"
scrollback_lines = 1024

[hosts.builder]
type = "remote"
address = "builder.example"
`),
		[]byte("[debug]\npprof = true\n"),
		[]byte("[client]\nlocal_echo = \"always\"\nlocal_echo_style = \"underline\"\n"),
		[]byte("[client]\nlocal_echo = \"maybe\"\n"),
		[]byte("[client]\nlocal_echo_style = \"flashy\"\n"),
		[]byte("[theme]\nstatus_style = \"powerline\"\n"),
		[]byte("[theme]\nstatus_style = \"fancy\"\n"),
		[]byte("[theme]\nicons = \"unicode\"\n"),
		[]byte("[theme]\nicons = \"powerline\"\n"),
		[]byte("[theme]\nicons = \"\"\n"),
		[]byte("[transport]\npreference = [\" ssh \", \"\", \"ssh\", \"mosh\"]\n"),
		[]byte("scrollback_lines = 0\n"),
		[]byte("[hosts.local]\nscrollback_lines = 0\n"),
		[]byte("[keys]\nprefix = \"C-b\"\n"),
		[]byte("[keys.bind]\ns = \"split\"\n"),
		[]byte("scrollback_lines = \"many\"\n"),
		[]byte("[hosts.\"builder.example\"]\ntype = \"remote\"\naddress = \"127.0.0.1\"\n"),
		[]byte("[hosts.local]\ntype = \"local\"\ntype = \"remote\"\n"),
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
	if cfg.Hosts == nil {
		t.Fatal("parse returned nil Hosts map with nil error")
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

	preferences := cfg.TransportPreferences()
	for name, host := range cfg.Hosts {
		if _, err := ResolveScrollbackLines(host.ScrollbackLines); err != nil {
			t.Fatalf("host %q scrollback did not validate after parse: %v", name, err)
		}
		if host.Color == "" {
			t.Fatalf("host %q color is empty after parse", name)
		}
		if !reflect.DeepEqual(host.TransportPreference, preferences) {
			t.Fatalf("host %q transport preference = %v, want %v", name, host.TransportPreference, preferences)
		}
	}
}
