package server

import "testing"

func TestRemoteCommandContextFinalizeDisconnect(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	ctx := remoteCommandContext{&CommandContext{Sess: sess}}
	res := ctx.FinalizeDisconnect("gpu")
	if res.Mutate == nil {
		t.Fatal("FinalizeDisconnect() should return a mutation result")
	}

	got := res.Mutate()
	if got.Err != nil {
		t.Fatalf("FinalizeDisconnect() mutate error = %v", got.Err)
	}
	if got.Output != "Disconnected from gpu\n" {
		t.Fatalf("FinalizeDisconnect() output = %q, want %q", got.Output, "Disconnected from gpu\n")
	}
	if !got.BroadcastLayout {
		t.Fatal("FinalizeDisconnect() should broadcast layout updates")
	}
}
