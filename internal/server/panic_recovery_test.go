package server

import (
	"maps"
	"strings"
	"testing"
)

func TestHandleCommandPanicSendsError(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	// Inject a panicking command into this server's registry only —
	// no mutation of the shared package-level commandRegistry.
	const cmdName = "__test_panic__"
	srv.commands = maps.Clone(commandRegistry)
	srv.commands[cmdName] = func(ctx *CommandContext) {
		panic("boom")
	}

	// A panicking handler should return an error, not crash the session.
	res := runTestCommand(t, srv, sess, cmdName)
	if res.cmdErr == "" {
		t.Fatal("expected CmdErr from panicking handler, got empty string")
	}
	if !strings.Contains(res.cmdErr, "internal error") {
		t.Fatalf("CmdErr should mention internal error, got: %s", res.cmdErr)
	}

	// Session should still be alive — run a normal command to prove it.
	res = runTestCommand(t, srv, sess, "status")
	if res.cmdErr != "" {
		t.Fatalf("status command failed after panic recovery: %s", res.cmdErr)
	}
}

func TestCommandMutationPanicReturnsError(t *testing.T) {
	t.Parallel()

	sess := newSession("test-mutation-panic")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	// A panicking mutation closure should return an error result.
	res := sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		panic("mutation boom")
	})
	if res.err == nil {
		t.Fatal("expected error from panicking mutation, got nil")
	}
	if !strings.Contains(res.err.Error(), "internal error") {
		t.Fatalf("error should mention internal error, got: %s", res.err)
	}

	// Event loop should still be alive.
	res = sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		return commandMutationResult{output: "alive"}
	})
	if res.err != nil {
		t.Fatalf("subsequent mutation failed: %v", res.err)
	}
	if res.output != "alive" {
		t.Fatalf("expected output 'alive', got %q", res.output)
	}
}

func TestSessionQueryPanicReturnsError(t *testing.T) {
	t.Parallel()

	sess := newSession("test-query-panic")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	// A panicking query closure should return an error.
	_, err := enqueueSessionQuery(sess, func(sess *Session) (int, error) {
		panic("query boom")
	})
	if err == nil {
		t.Fatal("expected error from panicking query, got nil")
	}
	if !strings.Contains(err.Error(), "internal error") {
		t.Fatalf("error should mention internal error, got: %s", err)
	}

	// Event loop should still be alive.
	val, err := enqueueSessionQuery(sess, func(sess *Session) (string, error) {
		return "alive", nil
	})
	if err != nil {
		t.Fatalf("subsequent query failed: %v", err)
	}
	if val != "alive" {
		t.Fatalf("expected 'alive', got %q", val)
	}
}
