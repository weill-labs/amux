package remote

import (
	"errors"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func windowLayout(windows ...proto.WindowSnapshot) *proto.LayoutSnapshot {
	return &proto.LayoutSnapshot{Windows: windows}
}

func win(id uint32, name string, index int) proto.WindowSnapshot {
	return proto.WindowSnapshot{ID: id, Name: name, Index: index}
}

func TestResolveWindowFromLayout(t *testing.T) {
	t.Parallel()

	layout := windowLayout(
		win(10, "amux", 1),
		win(20, "orca", 2),
		win(30, "orca", 3), // duplicate name → ambiguous by name
	)

	tests := []struct {
		name     string
		layout   *proto.LayoutSnapshot
		ref      string
		wantID   uint32
		wantKind ResolveWindowErrorKind
	}{
		{name: "by unique name", layout: layout, ref: "amux", wantID: 10},
		{name: "by index", layout: layout, ref: "2", wantID: 20},
		{name: "by index last", layout: layout, ref: "3", wantID: 30},
		{name: "ambiguous name", layout: layout, ref: "orca", wantKind: ResolveWindowAmbiguous},
		{name: "missing name", layout: layout, ref: "ghost", wantKind: ResolveWindowNotFound},
		{name: "index out of range", layout: layout, ref: "9", wantKind: ResolveWindowNotFound},
		{name: "nil layout", layout: nil, ref: "amux", wantKind: ResolveWindowNotFound},
		{name: "no windows", layout: &proto.LayoutSnapshot{}, ref: "amux", wantKind: ResolveWindowNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveWindowFromLayout(tt.layout, tt.ref)
			if tt.wantKind != "" {
				var rwErr *ResolveWindowError
				if !errors.As(err, &rwErr) {
					t.Fatalf("expected ResolveWindowError, got %v", err)
				}
				if rwErr.Kind != tt.wantKind {
					t.Fatalf("kind = %q, want %q", rwErr.Kind, tt.wantKind)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ID != tt.wantID {
				t.Fatalf("window ID = %d, want %d", got.ID, tt.wantID)
			}
		})
	}
}
