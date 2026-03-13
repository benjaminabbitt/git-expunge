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
