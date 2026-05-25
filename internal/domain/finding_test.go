package domain

import "testing"

func TestManifest_Add(t *testing.T) {
	m := NewManifest()
	m.Add(&Finding{BlobHash: "h1", Path: "a"})
	if len(m) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(m))
	}
	if m["h1"].Path != "a" {
		t.Errorf("wrong path: %q", m["h1"].Path)
	}

	// Add same hash again with new commits — Add merges commits and
	// keeps the existing entry.
	m["h1"].Commits = []string{"c1"}
	m.Add(&Finding{BlobHash: "h1", Path: "different.path", Commits: []string{"c2"}})
	if m["h1"].Path != "a" {
		t.Errorf("expected Add to keep existing Path, got %q", m["h1"].Path)
	}
	if len(m["h1"].Commits) != 2 {
		t.Errorf("expected merged commits len=2, got %v", m["h1"].Commits)
	}
}

func TestManifest_Remove_PresentAndAbsent(t *testing.T) {
	m := NewManifest()
	m.Add(&Finding{BlobHash: "h1", Path: "a"})
	if !m.Remove("h1") {
		t.Error("Remove(present) should return true")
	}
	if _, ok := m["h1"]; ok {
		t.Error("entry should be gone")
	}
	if m.Remove("h-missing") {
		t.Error("Remove(missing) should return false")
	}
}

func TestManifest_Merge_AddsAndCountsNew(t *testing.T) {
	m := NewManifest()
	m.Add(&Finding{BlobHash: "h1", Path: "a", Commits: []string{"c1"}})

	other := NewManifest()
	other.Add(&Finding{BlobHash: "h1", Path: "a", Commits: []string{"c-new"}}) // existing
	other.Add(&Finding{BlobHash: "h2", Path: "b"})                              // new
	other.Add(&Finding{BlobHash: "h3", Path: "c"})                              // new

	added := m.Merge(other)
	if added != 2 {
		t.Errorf("expected added=2, got %d", added)
	}
	if len(m) != 3 {
		t.Errorf("expected merged manifest of len=3, got %d", len(m))
	}
	if len(m["h1"].Commits) != 2 {
		t.Errorf("Merge should preserve Add's commit union; got %v", m["h1"].Commits)
	}
}

func TestManifest_Merge_EmptyReturnsZero(t *testing.T) {
	m := NewManifest()
	m.Add(&Finding{BlobHash: "h1", Path: "a"})
	if added := m.Merge(nil); added != 0 {
		t.Errorf("Merge(nil) should return 0, got %d", added)
	}
	if added := m.Merge(NewManifest()); added != 0 {
		t.Errorf("Merge(empty) should return 0, got %d", added)
	}
}

func TestManifest_Blobs(t *testing.T) {
	m := NewManifest()
	m.Add(&Finding{BlobHash: "h1", Path: "a"})
	m.Add(&Finding{BlobHash: "h2", Path: "b"})
	got := m.Blobs()
	if len(got) != 2 {
		t.Fatalf("expected 2 blobs, got %d", len(got))
	}
	// order unspecified
	seen := map[string]bool{}
	for _, h := range got {
		seen[h] = true
	}
	if !seen["h1"] || !seen["h2"] {
		t.Errorf("expected blobs h1+h2, got %v", got)
	}
}

func TestFindingType_Constants(t *testing.T) {
	if FindingTypeBinary != "binary" {
		t.Errorf("FindingTypeBinary = %q", FindingTypeBinary)
	}
	if FindingTypeSecret != "secret" {
		t.Errorf("FindingTypeSecret = %q", FindingTypeSecret)
	}
	if FindingTypeLargeFile != "large_file" {
		t.Errorf("FindingTypeLargeFile = %q", FindingTypeLargeFile)
	}
	if FindingTypeAdd != "add" {
		t.Errorf("FindingTypeAdd = %q", FindingTypeAdd)
	}
}

func TestManifest_SafeBlobs_NoSharedPaths_AllSafe(t *testing.T) {
	m := NewManifest()
	m.Add(&Finding{BlobHash: "h1", Path: "secret.env"})
	m.Add(&Finding{BlobHash: "h2", Path: "creds.json"})

	allPaths := map[string][]string{
		"h1": {"secret.env"},
		"h2": {"creds.json"},
	}

	safe, skipped := m.SafeBlobs(allPaths)
	if len(safe) != 2 {
		t.Errorf("expected all 2 safe, got %d (skipped=%v)", len(safe), skipped)
	}
	if len(skipped) != 0 {
		t.Errorf("expected no skipped, got %v", skipped)
	}
}

func TestManifest_SafeBlobs_SharedPathNotInManifest_Skipped(t *testing.T) {
	m := NewManifest()
	m.Add(&Finding{BlobHash: "shared", Path: "a/secret.env"})
	// Same blob also appears at b/keep.env, which is NOT in the manifest.

	allPaths := map[string][]string{
		"shared": {"a/secret.env", "b/keep.env"},
	}

	safe, skipped := m.SafeBlobs(allPaths)
	if len(safe) != 0 {
		t.Errorf("expected 0 safe, got %v", safe)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped, got %d", len(skipped))
	}
	if skipped[0].BlobHash != "shared" {
		t.Errorf("wrong skipped hash: %q", skipped[0].BlobHash)
	}
	if len(skipped[0].UnmarkedPaths) != 1 || skipped[0].UnmarkedPaths[0] != "b/keep.env" {
		t.Errorf("wrong unmarked paths: %v", skipped[0].UnmarkedPaths)
	}
}

func TestManifest_SharedBlobWarnings_FlagsMultiPathBlobs(t *testing.T) {
	m := NewManifest()
	m.Add(&Finding{BlobHash: "shared", Path: "a/secret.env"})

	allPaths := map[string][]string{
		"shared": {"a/secret.env", "b/copy.env"},
	}

	got := m.SharedBlobWarnings(allPaths)
	if len(got) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(got))
	}
	if got[0].BlobHash != "shared" || got[0].PurgePath != "a/secret.env" {
		t.Errorf("wrong warning: %+v", got[0])
	}
	if len(got[0].AffectedPaths) != 1 || got[0].AffectedPaths[0] != "b/copy.env" {
		t.Errorf("wrong affected paths: %v", got[0].AffectedPaths)
	}
}
