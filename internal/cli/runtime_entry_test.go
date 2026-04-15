package cli

import "testing"

func TestDefaultRuntimeWiresRunSSHSession(t *testing.T) {
	t.Parallel()

	runtime := defaultRuntime("")
	if runtime.RunSSHSession == nil {
		t.Fatal("defaultRuntime() should wire RunSSHSession")
	}
}
