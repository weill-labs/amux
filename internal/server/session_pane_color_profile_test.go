package server

import "testing"

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
