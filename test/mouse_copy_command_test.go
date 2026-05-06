package test

import (
	"strings"
	"testing"
)

func TestMouseCopyTargetCommandHidesTargetLiteral(t *testing.T) {
	t.Parallel()

	const target = "hello from mouse"
	cmd := mouseCopyTargetCommand(target, 3)

	if strings.Contains(cmd, target) {
		t.Fatalf("command %q should not contain target literal %q", cmd, target)
	}
}
