// Package review provides the shared in-memory backend used by the TUI and
// CLI review interfaces. A Session is a thin wrapper around domain.Manifest
// that adds two things UIs need but the data layer doesn't owe them:
//
//  1. A stably sorted findings view (Findings), rebuilt on structural change.
//  2. A dirty flag flipped by every mutation, cleared by MarkSaved.
//
// Every Session mutation method is delegate-plus-bookkeeping: the actual
// work lives on domain.Manifest. If a Session method's body grows beyond
// "call Manifest, flip dirty, maybe rebuild," the logic belongs on
// Manifest, not here.
package review

import (
	"fmt"
	"sort"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
)

// Session is the unified review backend. It retains a reference to the
// caller's manifest map (no copy) so mutations made through Session are
// visible to whoever later writes the manifest to disk.
//
// Session is not safe for concurrent use; UIs run it single-threaded.
type Session struct {
	manifest domain.Manifest
	findings []*domain.Finding
	dirty    bool
}

// NewSession wraps an existing manifest. A nil manifest is treated as an
// empty one. The findings view is sorted by Path so callers get a stable
// ordering across runs.
func NewSession(m domain.Manifest) *Session {
	if m == nil {
		m = domain.NewManifest()
	}
	s := &Session{manifest: m}
	s.rebuild()
	return s
}

// Manifest returns the underlying map. Callers may pass it to manifest.WriteJSON
// or to the rewriter; mutations to the returned map are visible to the
// Session but bypass dirty tracking, so prefer the explicit Session
// methods for queue changes.
func (s *Session) Manifest() domain.Manifest { return s.manifest }

// Findings returns the sorted view. The slice is owned by the Session; do
// not mutate it directly. Pointer values inside are shared with the
// manifest, so mutating a Finding's Purge field through this slice is
// equivalent to mutating it through the manifest map.
func (s *Session) Findings() []*domain.Finding { return s.findings }

// Len reports the number of findings.
func (s *Session) Len() int { return len(s.findings) }

// Dirty reports whether any mutation has occurred since the last
// MarkSaved (or session creation).
func (s *Session) Dirty() bool { return s.dirty }

// MarkSaved clears the dirty flag — call it after a successful disk write.
func (s *Session) MarkSaved() { s.dirty = false }

// ToggleAt flips the Purge flag of the finding at the given index in the
// sorted view. This is the only mutation method that genuinely uses the
// sorted view (cursor-based UIs); all other mutations key off blob hash.
func (s *Session) ToggleAt(index int) (bool, error) {
	if index < 0 || index >= len(s.findings) {
		return false, fmt.Errorf("review: ToggleAt index %d out of range [0,%d)", index, len(s.findings))
	}
	return s.ToggleByHash(s.findings[index].BlobHash)
}

// ToggleByHash flips the Purge flag of the finding with the given hash.
func (s *Session) ToggleByHash(hash string) (bool, error) {
	state, err := s.manifest.Toggle(hash)
	if err == nil {
		s.dirty = true
	}
	return state, err
}

// SetPurge sets the Purge flag to a specific value. Marks the session
// dirty only if the value actually changed (delegates the change check
// to Manifest.SetPurge).
func (s *Session) SetPurge(hash string, purge bool) error {
	changed, err := s.manifest.SetPurge(hash, purge)
	if err == nil && changed {
		s.dirty = true
	}
	return err
}

// MarkAllForPurge marks every finding for purge.
func (s *Session) MarkAllForPurge() {
	s.manifest.MarkAllForPurge()
	s.dirty = true
}

// ClearAllPurge clears the Purge flag on every finding.
func (s *Session) ClearAllPurge() {
	s.manifest.ClearAllPurge()
	s.dirty = true
}

// AddFinding inserts a finding (delegating Add's merge-on-collision
// behavior) and rebuilds the sorted view only when the structure changed.
func (s *Session) AddFinding(f *domain.Finding) {
	if f == nil {
		return
	}
	_, existed := s.manifest[f.BlobHash]
	s.manifest.Add(f)
	s.dirty = true
	if !existed {
		s.rebuild()
	}
}

// RemoveByHash drops the entry and rebuilds the sorted view. Returns
// whether anything was removed.
func (s *Session) RemoveByHash(hash string) bool {
	if !s.manifest.Remove(hash) {
		return false
	}
	s.dirty = true
	s.rebuild()
	return true
}

// Merge folds another manifest in. Rebuilds the sorted view only if at
// least one new entry was inserted.
func (s *Session) Merge(other domain.Manifest) {
	added := s.manifest.Merge(other)
	if added > 0 || len(other) > 0 {
		s.dirty = true
	}
	if added > 0 {
		s.rebuild()
	}
}

// rebuild refreshes the sorted findings view from the manifest. Called
// after any structural change (insert/delete). Pure mutations to existing
// Finding fields (Purge toggles) don't require a rebuild because the
// pointers in s.findings already alias the manifest entries.
func (s *Session) rebuild() {
	out := make([]*domain.Finding, 0, len(s.manifest))
	for _, f := range s.manifest {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	s.findings = out
}
