package render

import (
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestNormalizeTrackedStatusDefaultsUnknown(t *testing.T) {
	t.Parallel()

	if got := normalizeTrackedStatus(""); got != proto.TrackedStatusUnknown {
		t.Fatalf("normalizeTrackedStatus(\"\") = %q, want unknown", got)
	}
	if got := normalizeTrackedStatus(proto.TrackedStatusCompleted); got != proto.TrackedStatusCompleted {
		t.Fatalf("normalizeTrackedStatus(completed) = %q, want completed", got)
	}
}

func TestPaneStatusMetadataSegmentsTruncatesLongFirstItem(t *testing.T) {
	t.Parallel()

	got := paneStatusMetadataSegments([]paneStatusMetadataItem{
		{text: "#123456", status: proto.TrackedStatusCompleted},
	}, 5)
	want := []paneStatusMetadataSegment{
		{text: "#123", status: proto.TrackedStatusCompleted},
		{text: "…"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paneStatusMetadataSegments() = %#v, want %#v", got, want)
	}
}

func TestAvailableMetadataWidthReturnsZeroWithoutMetadata(t *testing.T) {
	t.Parallel()

	pd := &statusPaneData{name: "pane-1"}
	if got := availableMetadataWidth(40, pd, false); got != 0 {
		t.Fatalf("availableMetadataWidth() = %d, want 0", got)
	}
}
