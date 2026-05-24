package domain

import (
	"testing"
)

func TestManifest_Add(t *testing.T) {
	m := NewManifest()

	// Add first finding
	m.Add(&Finding{
		BlobHash: "hash1",
		Type:     FindingTypeBinary,
		Path:     "bin/app",
		Commits:  []string{"c1"},
	})

	if len(m) != 1 {
		t.Errorf("expected 1 finding, got %d", len(m))
	}

	// Add second finding
	m.Add(&Finding{
		BlobHash: "hash2",
		Type:     FindingTypeSecret,
		Path:     ".env",
		Commits:  []string{"c2"},
	})

	if len(m) != 2 {
		t.Errorf("expected 2 findings, got %d", len(m))
	}

	// Add duplicate - should merge commits
	m.Add(&Finding{
		BlobHash: "hash1",
		Type:     FindingTypeBinary,
		Path:     "bin/app",
		Commits:  []string{"c3"},
	})

	if len(m) != 2 {
		t.Errorf("expected 2 findings after duplicate, got %d", len(m))
	}

	if len(m["hash1"].Commits) != 2 {
		t.Errorf("expected 2 commits merged, got %d", len(m["hash1"].Commits))
	}
}

func TestManifest_PurgeCount(t *testing.T) {
	m := NewManifest()

	m.Add(&Finding{BlobHash: "h1", Purge: false})
	m.Add(&Finding{BlobHash: "h2", Purge: true})
	m.Add(&Finding{BlobHash: "h3", Purge: true})

	if count := m.PurgeCount(); count != 2 {
		t.Errorf("expected PurgeCount=2, got %d", count)
	}
}

func TestManifest_BlobsToPurge(t *testing.T) {
	m := NewManifest()

	m.Add(&Finding{BlobHash: "keep1", Purge: false})
	m.Add(&Finding{BlobHash: "purge1", Purge: true})
	m.Add(&Finding{BlobHash: "purge2", Purge: true})
	m.Add(&Finding{BlobHash: "keep2", Purge: false})

	blobs := m.BlobsToPurge()

	if len(blobs) != 2 {
		t.Errorf("expected 2 blobs to purge, got %d", len(blobs))
	}

	// Check that purged blobs are in the list
	found := make(map[string]bool)
	for _, b := range blobs {
		found[b] = true
	}

	if !found["purge1"] || !found["purge2"] {
		t.Errorf("expected purge1 and purge2 in list, got %v", blobs)
	}
}

func TestFindingType_Constants(t *testing.T) {
	if FindingTypeBinary != "binary" {
		t.Errorf("expected FindingTypeBinary='binary', got %s", FindingTypeBinary)
	}
	if FindingTypeSecret != "secret" {
		t.Errorf("expected FindingTypeSecret='secret', got %s", FindingTypeSecret)
	}
}

// fixtures for the new mutation methods
func threeFindings() Manifest {
	m := NewManifest()
	m.Add(&Finding{BlobHash: "hash-a", Type: FindingTypeBinary, Path: "a.bin"})
	m.Add(&Finding{BlobHash: "hash-b", Type: FindingTypeSecret, Path: "b.env"})
	m.Add(&Finding{BlobHash: "hash-c", Type: FindingTypeAdd, Path: "c.txt"})
	return m
}

func TestManifest_Remove_PresentAndAbsent(t *testing.T) {
	m := threeFindings()

	if !m.Remove("hash-b") {
		t.Error("Remove(present) should return true")
	}
	if _, ok := m["hash-b"]; ok {
		t.Error("Remove should delete the entry")
	}
	if len(m) != 2 {
		t.Errorf("expected len=2 after remove, got %d", len(m))
	}

	if m.Remove("hash-missing") {
		t.Error("Remove(missing) should return false")
	}
}

func TestManifest_Toggle_FlipsPurge(t *testing.T) {
	m := threeFindings()

	state, err := m.Toggle("hash-a")
	if err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	if !state || !m["hash-a"].Purge {
		t.Error("Toggle should set Purge=true on first call")
	}

	state, err = m.Toggle("hash-a")
	if err != nil {
		t.Fatalf("Toggle (second call): %v", err)
	}
	if state || m["hash-a"].Purge {
		t.Error("Toggle should set Purge=false on second call")
	}

	if _, err := m.Toggle("hash-missing"); err == nil {
		t.Error("Toggle(missing) should error")
	}
}

func TestManifest_SetPurge(t *testing.T) {
	m := threeFindings()

	changed, err := m.SetPurge("hash-a", true)
	if err != nil {
		t.Fatalf("SetPurge true: %v", err)
	}
	if !changed {
		t.Error("SetPurge should report changed=true on first set")
	}
	if !m["hash-a"].Purge {
		t.Error("SetPurge did not set the flag")
	}

	changed, err = m.SetPurge("hash-a", true)
	if err != nil {
		t.Fatalf("SetPurge idempotent: %v", err)
	}
	if changed {
		t.Error("SetPurge should report changed=false when value is unchanged")
	}

	if _, err := m.SetPurge("hash-missing", false); err == nil {
		t.Error("SetPurge(missing) should error")
	}
}

func TestManifest_MarkAllForPurge(t *testing.T) {
	m := threeFindings()
	m.MarkAllForPurge()

	for hash, f := range m {
		if !f.Purge {
			t.Errorf("entry %s not marked after MarkAllForPurge", hash)
		}
	}
	if m.PurgeCount() != 3 {
		t.Errorf("expected PurgeCount=3, got %d", m.PurgeCount())
	}
}

func TestManifest_ClearAllPurge(t *testing.T) {
	m := threeFindings()
	m.MarkAllForPurge()
	m.ClearAllPurge()

	for hash, f := range m {
		if f.Purge {
			t.Errorf("entry %s still marked after ClearAllPurge", hash)
		}
	}
	if m.PurgeCount() != 0 {
		t.Errorf("expected PurgeCount=0, got %d", m.PurgeCount())
	}
}

func TestManifest_Merge_AddsAndCountsNewEntries(t *testing.T) {
	m := threeFindings()

	other := NewManifest()
	other.Add(&Finding{BlobHash: "hash-a", Path: "a.bin", Commits: []string{"c-new"}}) // existing
	other.Add(&Finding{BlobHash: "hash-d", Path: "d.bin"})                              // new
	other.Add(&Finding{BlobHash: "hash-e", Path: "e.bin"})                              // new

	added := m.Merge(other)
	if added != 2 {
		t.Errorf("expected added=2 (new entries only), got %d", added)
	}
	if len(m) != 5 {
		t.Errorf("expected len=5 after merge, got %d", len(m))
	}

	// Commit-merge from Add semantics should kick in for hash-a.
	if len(m["hash-a"].Commits) == 0 {
		t.Error("Merge should preserve Add's commit-merging behavior")
	}
}

func TestManifest_Merge_EmptyOtherReturnsZero(t *testing.T) {
	m := threeFindings()
	if added := m.Merge(nil); added != 0 {
		t.Errorf("Merge(nil) should return 0, got %d", added)
	}
	if added := m.Merge(NewManifest()); added != 0 {
		t.Errorf("Merge(empty) should return 0, got %d", added)
	}
}
