package review

import (
	"testing"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
)

func mkManifest(t *testing.T) domain.Manifest {
	t.Helper()
	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: "hash-bin",
		Type:     domain.FindingTypeBinary,
		Path:     "bin/app",
		Commits:  []string{"c1"},
	})
	m.Add(&domain.Finding{
		BlobHash: "hash-sec",
		Type:     domain.FindingTypeSecret,
		Path:     ".env",
		Commits:  []string{"c2"},
	})
	m.Add(&domain.Finding{
		BlobHash: "hash-mid",
		Type:     domain.FindingTypeAdd,
		Path:     "middle.txt",
		Commits:  []string{"c3"},
	})
	return m
}

func TestSession_NewSession_SortsByPath(t *testing.T) {
	s := NewSession(mkManifest(t))
	got := s.Findings()
	if len(got) != 3 {
		t.Fatalf("expected 3 findings, got %d", len(got))
	}
	want := []string{".env", "bin/app", "middle.txt"}
	for i, w := range want {
		if got[i].Path != w {
			t.Errorf("findings[%d].Path = %q, want %q", i, got[i].Path, w)
		}
	}
}

func TestSession_NewSession_DirtyStartsFalse(t *testing.T) {
	s := NewSession(mkManifest(t))
	if s.Dirty() {
		t.Error("Dirty should be false on a freshly loaded session")
	}
}

func TestSession_Dirty_FlipsOnMutationAndClearsOnMarkSaved(t *testing.T) {
	s := NewSession(mkManifest(t))

	if _, err := s.ToggleAt(0); err != nil {
		t.Fatalf("ToggleAt: %v", err)
	}
	if !s.Dirty() {
		t.Error("Dirty should flip true after ToggleAt")
	}

	s.MarkSaved()
	if s.Dirty() {
		t.Error("Dirty should clear after MarkSaved")
	}

	s.MarkAllForPurge()
	if !s.Dirty() {
		t.Error("Dirty should flip true after MarkAllForPurge")
	}

	s.MarkSaved()
	s.ClearAllPurge()
	if !s.Dirty() {
		t.Error("Dirty should flip true after ClearAllPurge")
	}
}

func TestSession_ToggleAt_FlipsPurgeAndReturnsNewState(t *testing.T) {
	s := NewSession(mkManifest(t))

	first, err := s.ToggleAt(0)
	if err != nil {
		t.Fatalf("ToggleAt(0): %v", err)
	}
	if !first {
		t.Errorf("expected first toggle to return true, got false")
	}
	if !s.Findings()[0].Purge {
		t.Error("first finding should be marked Purge=true after toggle")
	}

	second, err := s.ToggleAt(0)
	if err != nil {
		t.Fatalf("ToggleAt(0) again: %v", err)
	}
	if second {
		t.Errorf("expected second toggle to return false, got true")
	}
}

func TestSession_ToggleAt_OutOfRange(t *testing.T) {
	s := NewSession(mkManifest(t))
	if _, err := s.ToggleAt(-1); err == nil {
		t.Error("expected error for negative index")
	}
	if _, err := s.ToggleAt(99); err == nil {
		t.Error("expected error for index past end")
	}
}

func TestSession_SetPurge(t *testing.T) {
	s := NewSession(mkManifest(t))

	if err := s.SetPurge("hash-bin", true); err != nil {
		t.Fatalf("SetPurge true: %v", err)
	}
	if !s.Manifest()["hash-bin"].Purge {
		t.Error("SetPurge(true) did not set the flag")
	}
	if !s.Dirty() {
		t.Error("SetPurge changing the value should mark dirty")
	}

	s.MarkSaved()
	if err := s.SetPurge("hash-bin", true); err != nil {
		t.Fatalf("SetPurge idempotent: %v", err)
	}
	if s.Dirty() {
		t.Error("SetPurge with no value change should NOT mark dirty")
	}

	if err := s.SetPurge("hash-missing", false); err == nil {
		t.Error("expected error for missing hash")
	}
}

func TestSession_ToggleByHash_FoundAndNotFound(t *testing.T) {
	s := NewSession(mkManifest(t))

	state, err := s.ToggleByHash("hash-bin")
	if err != nil {
		t.Fatalf("ToggleByHash(found): %v", err)
	}
	if !state {
		t.Errorf("expected purge=true after toggle")
	}
	if !s.Manifest()["hash-bin"].Purge {
		t.Error("manifest entry should reflect the toggle")
	}

	if _, err := s.ToggleByHash("hash-missing"); err == nil {
		t.Error("expected error for missing hash")
	}
}

func TestSession_MarkAllForPurge_AffectsEveryFinding(t *testing.T) {
	s := NewSession(mkManifest(t))
	s.MarkAllForPurge()
	for _, f := range s.Findings() {
		if !f.Purge {
			t.Errorf("finding %q not marked after MarkAllForPurge", f.Path)
		}
	}
	if s.Manifest().PurgeCount() != 3 {
		t.Errorf("expected PurgeCount=3, got %d", s.Manifest().PurgeCount())
	}
}

func TestSession_ClearAllPurge_AffectsEveryFinding(t *testing.T) {
	s := NewSession(mkManifest(t))
	s.MarkAllForPurge()
	s.MarkSaved() // ensure ClearAllPurge flips dirty again
	s.ClearAllPurge()
	for _, f := range s.Findings() {
		if f.Purge {
			t.Errorf("finding %q still marked after ClearAllPurge", f.Path)
		}
	}
	if s.Manifest().PurgeCount() != 0 {
		t.Errorf("expected PurgeCount=0, got %d", s.Manifest().PurgeCount())
	}
}

func TestSession_AddFinding_MergesCommitsForExistingHash(t *testing.T) {
	s := NewSession(mkManifest(t))

	s.AddFinding(&domain.Finding{
		BlobHash: "hash-bin",
		Type:     domain.FindingTypeBinary,
		Path:     "bin/app",
		Commits:  []string{"c-new"},
	})

	merged := s.Manifest()["hash-bin"]
	if merged == nil {
		t.Fatal("hash-bin disappeared after AddFinding")
	}
	if len(merged.Commits) != 2 {
		t.Errorf("expected merged commit list of length 2, got %v", merged.Commits)
	}
	gotCommits := map[string]bool{}
	for _, c := range merged.Commits {
		gotCommits[c] = true
	}
	if !gotCommits["c1"] || !gotCommits["c-new"] {
		t.Errorf("expected commits {c1, c-new}, got %v", merged.Commits)
	}
}

func TestSession_AddFinding_NewHashRebuildsSortedFindings(t *testing.T) {
	s := NewSession(mkManifest(t))
	s.AddFinding(&domain.Finding{
		BlobHash: "hash-a",
		Type:     domain.FindingTypeAdd,
		Path:     "aaa.txt",
	})
	got := s.Findings()
	if len(got) != 4 || got[0].Path != ".env" || got[1].Path != "aaa.txt" {
		t.Errorf("expected sorted findings with aaa.txt second, got paths=%v",
			pathsOf(got))
	}
}

func TestSession_RemoveByHash_DropsEntryAndRebuilds(t *testing.T) {
	s := NewSession(mkManifest(t))
	ok := s.RemoveByHash("hash-mid")
	if !ok {
		t.Error("RemoveByHash should return true for existing hash")
	}
	if _, present := s.Manifest()["hash-mid"]; present {
		t.Error("manifest still contains removed hash")
	}
	for _, f := range s.Findings() {
		if f.BlobHash == "hash-mid" {
			t.Error("Findings slice still contains removed entry")
		}
	}
	if ok := s.RemoveByHash("hash-mid"); ok {
		t.Error("RemoveByHash should return false for missing hash")
	}
}

func TestSession_Merge_BulkAddsAndRebuildsOnce(t *testing.T) {
	s := NewSession(mkManifest(t))

	incoming := domain.NewManifest()
	incoming.Add(&domain.Finding{BlobHash: "hash-z", Type: domain.FindingTypeBinary, Path: "z.bin"})
	incoming.Add(&domain.Finding{BlobHash: "hash-aa", Type: domain.FindingTypeBinary, Path: "aa.bin"})

	s.Merge(incoming)

	if !s.Dirty() {
		t.Error("Merge should mark session dirty")
	}
	got := s.Findings()
	if len(got) != 5 {
		t.Fatalf("expected 5 findings after merge, got %d (%v)", len(got), pathsOf(got))
	}
	// Sorted: .env, aa.bin, bin/app, middle.txt, z.bin
	wantOrder := []string{".env", "aa.bin", "bin/app", "middle.txt", "z.bin"}
	for i, w := range wantOrder {
		if got[i].Path != w {
			t.Errorf("findings[%d].Path = %q, want %q (full: %v)",
				i, got[i].Path, w, pathsOf(got))
		}
	}
}

func TestSession_Manifest_ReturnsSameUnderlyingMap(t *testing.T) {
	// Go maps are reference types, so identity is checked by mutating
	// through one alias and observing via the other. Callers (rewriter,
	// JSON writer) rely on this — they pass a manifest in and expect
	// later disk writes to see Session's mutations.
	m := mkManifest(t)
	s := NewSession(m)

	s.Manifest()["new-hash"] = &domain.Finding{
		BlobHash: "new-hash", Path: "new.txt", Type: domain.FindingTypeAdd,
	}
	if _, ok := m["new-hash"]; !ok {
		t.Error("Session.Manifest() must alias the same map the caller passed in")
	}
}

func pathsOf(fs []*domain.Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Path
	}
	return out
}
