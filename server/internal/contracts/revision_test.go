package contracts

import "testing"

func TestSourceRevisionIsStable(t *testing.T) {
	// Pinned value: a change here breaks every stored and in-flight revision.
	const want = "26f89dcefd3415cb12f87f793cb958bd26470771b742ed88645ec98afc9d4f73"
	base := SourceRevision("mov-synthetic-0001", 734167, 1761523200000000000)
	if base != want {
		t.Fatalf("SourceRevision = %s, want %s", base, want)
	}
	for name, other := range map[string]string{
		"file ID": SourceRevision("mov-synthetic-0002", 734167, 1761523200000000000),
		"size":    SourceRevision("mov-synthetic-0001", 734168, 1761523200000000000),
		"mtime":   SourceRevision("mov-synthetic-0001", 734167, 1761523200000000001),
	} {
		if other == base {
			t.Errorf("changing the %s kept the revision", name)
		}
	}
}
