package mux

import (
	"testing"
)

func strPtr(s string) *string { return &s }

func TestAmuxMetaScanner(t *testing.T) {
	t.Parallel()

	t.Run("complete sequence", func(t *testing.T) {
		t.Parallel()
		var s AmuxMetaScanner
		data := FormatMetaSequence(MetaUpdate{PR: strPtr("42")})
		results := s.Scan(data)
		if len(results) != 1 {
			t.Fatalf("got %d results, want 1", len(results))
		}
		if results[0].PR == nil || *results[0].PR != "42" {
			t.Errorf("PR = %v, want '42'", results[0].PR)
		}
		if results[0].Task != nil {
			t.Errorf("Task should be nil (not set)")
		}
	})

	t.Run("partial across reads", func(t *testing.T) {
		t.Parallel()
		var s AmuxMetaScanner
		data := FormatMetaSequence(MetaUpdate{Task: strPtr("build")})
		mid := len(data) / 2
		r1 := s.Scan(data[:mid])
		if len(r1) != 0 {
			t.Fatalf("partial should return 0 results, got %d", len(r1))
		}
		r2 := s.Scan(data[mid:])
		if len(r2) != 1 {
			t.Fatalf("got %d results, want 1", len(r2))
		}
		if r2[0].Task == nil || *r2[0].Task != "build" {
			t.Errorf("Task = %v, want 'build'", r2[0].Task)
		}
	})

	t.Run("multiple sequences in one chunk", func(t *testing.T) {
		t.Parallel()
		var s AmuxMetaScanner
		d1 := FormatMetaSequence(MetaUpdate{PR: strPtr("1")})
		d2 := FormatMetaSequence(MetaUpdate{PR: strPtr("2")})
		data := append(d1, d2...)
		results := s.Scan(data)
		if len(results) != 2 {
			t.Fatalf("got %d results, want 2", len(results))
		}
		if *results[0].PR != "1" || *results[1].PR != "2" {
			t.Errorf("PRs = %v, %v", *results[0].PR, *results[1].PR)
		}
	})

	t.Run("malformed JSON skipped", func(t *testing.T) {
		t.Parallel()
		var s AmuxMetaScanner
		bad := []byte("\x1b]999;amux-meta;{bad json\x07")
		good := FormatMetaSequence(MetaUpdate{PR: strPtr("ok")})
		data := append(bad, good...)
		results := s.Scan(data)
		if len(results) != 1 {
			t.Fatalf("got %d results, want 1 (skip bad)", len(results))
		}
		if *results[0].PR != "ok" {
			t.Errorf("PR = %v, want 'ok'", *results[0].PR)
		}
	})

	t.Run("clear via empty string", func(t *testing.T) {
		t.Parallel()
		var s AmuxMetaScanner
		data := FormatMetaSequence(MetaUpdate{PR: strPtr("")})
		results := s.Scan(data)
		if len(results) != 1 {
			t.Fatalf("got %d results, want 1", len(results))
		}
		if results[0].PR == nil {
			t.Fatal("PR should be non-nil (set to empty)")
		}
		if *results[0].PR != "" {
			t.Errorf("PR = %q, want empty string", *results[0].PR)
		}
	})

	t.Run("embedded in other output", func(t *testing.T) {
		t.Parallel()
		var s AmuxMetaScanner
		meta := FormatMetaSequence(MetaUpdate{Branch: strPtr("main")})
		data := append([]byte("some terminal output\r\n"), meta...)
		data = append(data, []byte("more output\r\n")...)
		results := s.Scan(data)
		if len(results) != 1 {
			t.Fatalf("got %d results, want 1", len(results))
		}
		if *results[0].Branch != "main" {
			t.Errorf("Branch = %v, want 'main'", *results[0].Branch)
		}
	})
}
