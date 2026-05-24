package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
)

func createTestManifest() domain.Manifest {
	m := domain.NewManifest()
	m.Add(&domain.Finding{
		BlobHash: "abc123def456",
		Type:     domain.FindingTypeBinary,
		Path:     "bin/app",
		Size:     1024,
		MimeType: "application/octet-stream",
		Commits:  []string{"c1"},
		Purge:    false,
	})
	m.Add(&domain.Finding{
		BlobHash: "def456abc123",
		Type:     domain.FindingTypeSecret,
		Path:     ".env",
		Rule:     "aws-access-key",
		Commits:  []string{"c2"},
		Purge:    false,
	})
	return m
}

func TestNew(t *testing.T) {
	m := createTestManifest()
	model := New(m, "", ModeReview)

	if len(model.session.Findings()) != 2 {
		t.Errorf("expected 2 findings, got %d", len(model.session.Findings()))
	}
	if model.cursor != 0 {
		t.Error("expected cursor at 0")
	}
	if model.filter != FilterAll {
		t.Error("expected filter to be FilterAll")
	}
}

func TestModel_Init(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)
	cmd := model.Init()
	if cmd != nil {
		t.Error("Init should return nil")
	}
}

func TestModel_Update_Navigation(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)

	// Move down
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newModel.(Model)
	if model.cursor != 1 {
		t.Errorf("expected cursor 1 after down, got %d", model.cursor)
	}

	// Try to move down past end (should stay at 1)
	newModel, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = newModel.(Model)
	if model.cursor != 1 {
		t.Errorf("expected cursor to stay at 1, got %d", model.cursor)
	}

	// Move up
	newModel, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = newModel.(Model)
	if model.cursor != 0 {
		t.Errorf("expected cursor 0 after up, got %d", model.cursor)
	}

	// Try to move up past start (should stay at 0)
	newModel, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = newModel.(Model)
	if model.cursor != 0 {
		t.Errorf("expected cursor to stay at 0, got %d", model.cursor)
	}
}

func TestModel_Update_Toggle(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)

	// Toggle first item
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newModel.(Model)
	if !model.filtered[0].Purge {
		t.Error("expected first item to be marked for purge")
	}
	if model.WasSaved() {
		t.Error("expected saved to be false after change")
	}

	// Toggle again
	newModel, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newModel.(Model)
	if model.filtered[0].Purge {
		t.Error("expected first item to not be marked for purge")
	}
}

func TestModel_Update_PurgeAll(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)

	// Press 'a' to purge all
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = newModel.(Model)

	for _, f := range model.filtered {
		if !f.Purge {
			t.Error("expected all items to be marked for purge")
		}
	}
}

func TestModel_Update_ClearAll(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)

	// First purge all
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = newModel.(Model)

	// Then clear all
	newModel, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	model = newModel.(Model)

	for _, f := range model.filtered {
		if f.Purge {
			t.Error("expected all items to have purge cleared")
		}
	}
}

// TestModel_Update_ClearAll_ClearsHiddenByFilter pins down the contract that
// "clear all" empties the queue across the whole manifest, not just the
// currently visible subset. Previously the handler iterated m.filtered so
// items hidden by an active filter (e.g. FilterBinaries) silently kept
// Purge=true.
func TestModel_Update_ClearAll_ClearsHiddenByFilter(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)

	// Mark every finding for purge while no filter hides anything.
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = newModel.(Model)

	// Switch to a filter that only shows binaries; the secret in
	// createTestManifest() is now hidden.
	model.filter = FilterBinaries
	model.applyFilter()
	if len(model.filtered) != 1 {
		t.Fatalf("setup: expected 1 binary visible, got %d", len(model.filtered))
	}

	// Press 'c'.
	newModel, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	model = newModel.(Model)

	// Every finding in the manifest should now be cleared, including the
	// hidden secret.
	if got := model.session.Manifest().PurgeCount(); got != 0 {
		t.Errorf("expected manifest PurgeCount=0 after clear-all, got %d", got)
	}
	for _, f := range model.session.Findings() {
		if f.Purge {
			t.Errorf("finding %q still marked for purge after clear-all (filter was %q)",
				f.Path, model.filter)
		}
	}
}

// TestModel_Update_PurgeAll_MarksHiddenByFilter mirrors the previous test
// for purge-all: it should mark every finding, not just the visible ones.
func TestModel_Update_PurgeAll_MarksHiddenByFilter(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)

	model.filter = FilterBinaries
	model.applyFilter()
	if len(model.filtered) != 1 {
		t.Fatalf("setup: expected 1 binary visible, got %d", len(model.filtered))
	}

	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = newModel.(Model)

	if got := model.session.Manifest().PurgeCount(); got != len(model.session.Findings()) {
		t.Errorf("expected every finding marked for purge, got PurgeCount=%d/%d",
			got, len(model.session.Findings()))
	}
}

func TestModel_Update_Filter(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)

	// Press 'f' to cycle filter
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	model = newModel.(Model)
	if model.filter != FilterBinaries {
		t.Errorf("expected FilterBinaries, got %d", model.filter)
	}
	if len(model.filtered) != 1 {
		t.Errorf("expected 1 binary, got %d", len(model.filtered))
	}

	// Press 'f' again
	newModel, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	model = newModel.(Model)
	if model.filter != FilterSecrets {
		t.Errorf("expected FilterSecrets, got %d", model.filter)
	}
	if len(model.filtered) != 1 {
		t.Errorf("expected 1 secret, got %d", len(model.filtered))
	}

	// Press 'f' again for FilterPurge
	newModel, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	model = newModel.(Model)
	if model.filter != FilterPurge {
		t.Errorf("expected FilterPurge, got %d", model.filter)
	}
	if len(model.filtered) != 0 {
		t.Errorf("expected 0 purged items, got %d", len(model.filtered))
	}

	// Press 'f' again to cycle back to FilterAll
	newModel, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	model = newModel.(Model)
	if model.filter != FilterAll {
		t.Errorf("expected FilterAll, got %d", model.filter)
	}
}

func TestModel_Update_Save(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)

	// Press 's' to save
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = newModel.(Model)
	if !model.WasSaved() {
		t.Error("expected saved to be true")
	}
}


func TestModel_Update_Quit(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)

	// Press 'q' to quit on a model with no unsaved changes -> exits immediately.
	newModel, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model = newModel.(Model)

	if !model.quitting {
		t.Error("expected quitting to be true")
	}
	if cmd == nil {
		t.Error("expected quit command")
	}
	if model.quitConfirmMode {
		t.Error("clean model should not enter quit-confirm mode")
	}
}

// TestModel_Quit_WhenDirty_PromptsInsteadOfExiting pins down that pressing
// 'q' after a mutation does NOT silently exit — it opens a confirm prompt
// so changes are not lost.
func TestModel_Quit_WhenDirty_PromptsInsteadOfExiting(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)

	// Make a change (toggle the first finding's purge state).
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newModel.(Model)
	if model.WasSaved() {
		t.Fatalf("setup: model should be dirty after toggle")
	}

	newModel, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model = newModel.(Model)

	if model.quitting {
		t.Errorf("dirty quit must NOT set quitting; should open prompt instead")
	}
	if cmd != nil {
		t.Errorf("dirty quit must NOT return a tea.Quit command; got %v", cmd)
	}
	if !model.quitConfirmMode {
		t.Errorf("dirty quit should open quitConfirmMode")
	}
}

// TestModel_QuitPrompt_YesSavesAndExits — pressing y/enter at the prompt
// flushes m.saved=true and quits.
func TestModel_QuitPrompt_YesSavesAndExits(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newModel.(Model)
	newModel, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model = newModel.(Model)
	if !model.quitConfirmMode {
		t.Fatalf("setup: expected quit prompt to be open")
	}

	newModel, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = newModel.(Model)

	if !model.WasSaved() {
		t.Error("y at quit prompt should mark saved=true")
	}
	if !model.quitting {
		t.Error("y at quit prompt should set quitting=true")
	}
	if cmd == nil {
		t.Error("y at quit prompt should return tea.Quit")
	}
	if model.quitConfirmMode {
		t.Error("y at quit prompt should close the prompt")
	}
}

// TestModel_QuitPrompt_NoDiscardsAndExits — pressing n at the prompt exits
// without flipping saved (changes are intentionally dropped).
func TestModel_QuitPrompt_NoDiscardsAndExits(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newModel.(Model)
	newModel, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model = newModel.(Model)

	newModel, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = newModel.(Model)

	if model.WasSaved() {
		t.Error("n at quit prompt must NOT mark saved=true")
	}
	if !model.quitting {
		t.Error("n at quit prompt should set quitting=true")
	}
	if cmd == nil {
		t.Error("n at quit prompt should return tea.Quit")
	}
}

// TestModel_QuitPrompt_EscCancels — Esc at the prompt aborts the quit and
// leaves the user in the editor.
func TestModel_QuitPrompt_EscCancels(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newModel.(Model)
	newModel, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model = newModel.(Model)

	newModel, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = newModel.(Model)

	if model.quitting {
		t.Error("Esc at quit prompt must NOT set quitting")
	}
	if cmd != nil {
		t.Error("Esc at quit prompt must NOT return tea.Quit")
	}
	if model.quitConfirmMode {
		t.Error("Esc at quit prompt should close the prompt")
	}
}

func TestModel_Update_WindowSize(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)

	newModel, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = newModel.(Model)
	if model.width != 80 {
		t.Errorf("expected width 80, got %d", model.width)
	}
	if model.height != 24 {
		t.Errorf("expected height 24, got %d", model.height)
	}
}

func TestModel_View(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)
	model.width = 80
	model.height = 24

	view := model.View()

	if !strings.Contains(view, "git-expunge") {
		t.Error("expected title in view")
	}
	if !strings.Contains(view, "Filter:") {
		t.Error("expected filter status in view")
	}
}

func TestModel_View_Quitting(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)
	model.quitting = true

	view := model.View()
	if view != "" {
		t.Error("expected empty view when quitting")
	}
}

func TestModel_GetManifest(t *testing.T) {
	m := createTestManifest()
	model := New(m, "", ModeReview)

	got := model.GetManifest()
	if len(got) != len(m) {
		t.Errorf("expected %d findings, got %d", len(m), len(got))
	}
}

func TestModel_WasSaved(t *testing.T) {
	model := New(createTestManifest(), "", ModeReview)

	// A freshly loaded model is treated as clean: nothing the user did,
	// nothing to lose. WasSaved should reflect that.
	if !model.WasSaved() {
		t.Error("expected WasSaved to be true on a freshly loaded manifest")
	}

	// Mutate via the toggle handler so we hit the real saved=false path.
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = newModel.(Model)
	if model.WasSaved() {
		t.Error("expected WasSaved to be false after a mutation")
	}

	// Pressing 's' explicitly marks saved again.
	newModel, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = newModel.(Model)
	if !model.WasSaved() {
		t.Error("expected WasSaved to be true after pressing s")
	}
}

func TestDefaultKeyMap(t *testing.T) {
	km := DefaultKeyMap()

	// Just verify keys are initialized
	if km.Up.Keys() == nil {
		t.Error("expected Up key to be initialized")
	}
	if km.Down.Keys() == nil {
		t.Error("expected Down key to be initialized")
	}
	if km.Toggle.Keys() == nil {
		t.Error("expected Toggle key to be initialized")
	}
}


func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{500, "500 bytes"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}

	for _, tt := range tests {
		result := formatSize(tt.bytes)
		if result != tt.expected {
			t.Errorf("formatSize(%d) = %s, expected %s", tt.bytes, result, tt.expected)
		}
	}
}

func TestModel_EmptyManifest(t *testing.T) {
	model := New(domain.NewManifest(), "", ModeReview)

	if len(model.session.Findings()) != 0 {
		t.Error("expected empty findings")
	}

	model.width = 80
	model.height = 24
	view := model.View()
	if !strings.Contains(view, "No findings") {
		t.Error("expected empty findings message")
	}
}

func TestModel_ToggleEmptyFiltered(t *testing.T) {
	model := New(domain.NewManifest(), "", ModeReview)

	// Toggle should not panic with empty list
	newModel, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	_ = newModel.(Model)
	// Just checking it doesn't panic
}
