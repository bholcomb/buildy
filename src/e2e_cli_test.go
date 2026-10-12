package main

import (
	"strings"
	"testing"
)

// --version prints the version alone, outside any workspace: CI compares it with a release tag.
func TestE2EVersion(t *testing.T) {
	out := mustBuildy(t, t.TempDir(), "--version")
	if want := "buildy " + Version + " ("; !strings.HasPrefix(out, want) || strings.Count(out, "\n") != 1 {
		t.Errorf("buildy --version printed %q, want one line starting %q", out, want)
	}
}
