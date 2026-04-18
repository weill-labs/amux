package config

import (
	"reflect"
	"testing"
)

func TestHostTransportDefaultsToSSH(t *testing.T) {
	t.Parallel()

	cfg := &Config{}
	if got := cfg.HostTransport("builder"); got != "ssh" {
		t.Fatalf("HostTransport(%q) = %q, want ssh", "builder", got)
	}
}

func TestHostTransportUsesConfiguredValue(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Hosts: map[string]Host{
			"builder": {Transport: "ssh"},
		},
	}
	if got := cfg.HostTransport("builder"); got != "ssh" {
		t.Fatalf("HostTransport(%q) = %q, want ssh", "builder", got)
	}
}

func TestTransportPreferencesDefaultToMoshThenSSH(t *testing.T) {
	t.Parallel()

	cfg := &Config{}
	want := []string{"mosh", "ssh"}
	if got := cfg.TransportPreferences(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TransportPreferences() = %v, want %v", got, want)
	}
}

func TestTransportPreferencesUseConfiguredOrder(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Transport: TransportConfig{
			Preference: []string{"ssh", "mosh"},
		},
	}
	want := []string{"ssh", "mosh"}
	if got := cfg.TransportPreferences(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TransportPreferences() = %v, want %v", got, want)
	}
}

func TestTransportPreferencesNormalizeConfiguredOrder(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Transport: TransportConfig{
			Preference: []string{" ssh ", "", "ssh", "mosh", "mosh"},
		},
	}
	want := []string{"ssh", "mosh"}
	if got := cfg.TransportPreferences(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TransportPreferences() = %v, want %v", got, want)
	}
}
