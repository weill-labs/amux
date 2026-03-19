package server

import "testing"

func stopCrashCheckpointLoop(t *testing.T, sess *Session) {
	t.Helper()

	if sess.crashCheckpointStop != nil {
		close(sess.crashCheckpointStop)
		<-sess.crashCheckpointDone
		sess.crashCheckpointStop = nil
		sess.crashCheckpointDone = nil
	}
}

func stopSessionBackgroundLoops(t *testing.T, sess *Session) {
	t.Helper()

	if sess.sessionEventStop != nil {
		close(sess.sessionEventStop)
		<-sess.sessionEventDone
		sess.sessionEventStop = nil
		sess.sessionEventDone = nil
	}
	stopCrashCheckpointLoop(t, sess)
}
