package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/termprofile"
)

type stubLaunchEnviron map[string]string

func (e stubLaunchEnviron) Environ() []string {
	env := make([]string, 0, len(e))
	for key, value := range e {
		env = append(env, key+"="+value)
	}
	return env
}

func (e stubLaunchEnviron) Getenv(key string) string {
	return e[key]
}

func TestPaneLaunchColorProfileUsesAttachingClient(t *testing.T) {
	t.Parallel()

	sess := newSession("test-pane-launch-color-profile-attach")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	attaching := &clientConn{colorProfile: "ANSI256"}
	if got := sess.paneLaunchColorProfile(attaching); got != "ANSI256" {
		t.Fatalf("paneLaunchColorProfile(attaching) = %q, want %q", got, "ANSI256")
	}
}

func TestPaneLaunchColorProfileFallsBackToEffectiveClient(t *testing.T) {
	t.Parallel()

	sess := newSession("test-pane-launch-color-profile-fallback")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	cc := &clientConn{colorProfile: "TrueColor"}
	sess.ensureClientManager().setClientsForTest(cc)

	if got := sess.paneLaunchColorProfile(nil); got != "TrueColor" {
		t.Fatalf("paneLaunchColorProfile(nil) = %q, want %q", got, "TrueColor")
	}
}

func TestPaneLaunchColorProfileFallsBackToSessionLaunchProfile(t *testing.T) {
	t.Parallel()

	sess := newSession("test-pane-launch-color-profile-session")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)
	sess.launchColorProfile = "TrueColor"

	if got := sess.paneLaunchColorProfile(nil); got != "TrueColor" {
		t.Fatalf("paneLaunchColorProfile(nil) = %q, want %q", got, "TrueColor")
	}
}

func TestSessionLaunchColorProfileIgnoresLauncherNoColor(t *testing.T) {
	t.Parallel()

	got := sessionLaunchColorProfile(stubLaunchEnviron{
		termprofile.EnvKey: "TrueColor",
		"NO_COLOR":         "1",
	})
	if got != "TrueColor" {
		t.Fatalf("sessionLaunchColorProfile() = %q, want %q", got, "TrueColor")
	}
}
