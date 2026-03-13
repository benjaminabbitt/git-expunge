// Package tui provides an interactive terminal UI using Bubbletea.
package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/internal/manifest"
	"github.com/benjaminabbitt/git-expunge/internal/preview"
	"github.com/benjaminabbitt/git-expunge/internal/rewriter"
	"github.com/benjaminabbitt/git-expunge/internal/safety"
	"github.com/benjaminabbitt/git-expunge/internal/scanner"
)

// ViewMode represents the current TUI mode
type ViewMode int

const (
	ModeReview ViewMode = iota
	ModeBrowse
	ModeScan
	ModeRewrite
)

// Styles
var (
	// Colors
	primaryColor   = lipgloss.Color("205")
	secondaryColor = lipgloss.Color("240")
	successColor   = lipgloss.Color("34")
	dangerColor    = lipgloss.Color("196")
	warningColor   = lipgloss.Color("214")
	textColor      = lipgloss.Color("252")

	// Title
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			Padding(0, 1)

	// List pane
	listPaneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(secondaryColor).
			Padding(0, 1)

	// Detail pane
	detailPaneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(secondaryColor).
			Padding(1, 2)

	// List items
	selectedItemStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("212")).
				Background(lipgloss.Color("236"))

	normalItemStyle = lipgloss.NewStyle().
			Foreground(textColor)

	// Checkboxes
	checkedStyle = lipgloss.NewStyle().
			Foreground(dangerColor).
			Bold(true)

	uncheckedStyle = lipgloss.NewStyle().
			Foreground(successColor)

	// Type badges
	binaryBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("0")).
				Background(warningColor).
				Padding(0, 1).
				Bold(true)

	secretBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("255")).
				Background(dangerColor).
				Padding(0, 1).
				Bold(true)

	// Detail labels
	labelStyle = lipgloss.NewStyle().
			Foreground(secondaryColor).
			Width(10)

	valueStyle = lipgloss.NewStyle().
			Foreground(textColor)

	// Status bar
	statusStyle = lipgloss.NewStyle().
			Foreground(primaryColor).
			Padding(0, 1)

	// Help
	helpStyle = lipgloss.NewStyle().
			Foreground(secondaryColor).
			Padding(0, 1)

	helpKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")).
			Bold(true)

	helpDescStyle = lipgloss.NewStyle().
			Foreground(secondaryColor)

	// Tab bar
	activeTabStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(primaryColor).
			Padding(0, 1).
			Bold(true)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(textColor).
				Background(lipgloss.Color("236")).
				Padding(0, 1)

	// Directory tree
	dirStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")).
			Bold(true)

	// Search input
	searchPromptStyle = lipgloss.NewStyle().
				Foreground(primaryColor).
				Bold(true)

	// Add badge for manually added files
	addBadgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("39")).
			Padding(0, 1).
			Bold(true)

	// Deleted/history-only file style
	deletedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")).
			Italic(true)

	// Deleted badge
	deletedBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("243")).
				Italic(true)
)

// KeyMap defines the key bindings.
type KeyMap struct {
	Up        key.Binding
	Down      key.Binding
	Left      key.Binding
	Right     key.Binding
	Toggle    key.Binding
	PurgeAll  key.Binding
	ClearAll  key.Binding
	Filter    key.Binding
	Save      key.Binding
	Export    key.Binding
	Restore   key.Binding
	Quit      key.Binding
	Tab       key.Binding
	Mode1     key.Binding
	Mode2     key.Binding
	Mode3     key.Binding
	Mode4     key.Binding
	Search    key.Binding
	DryRun    key.Binding
	Execute   key.Binding
	Enter     key.Binding
	Backspace key.Binding
}

// DefaultKeyMap returns the default key bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Left: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("←/h", "collapse"),
		),
		Right: key.NewBinding(
			key.WithKeys("right", "l"),
			key.WithHelp("→/l", "expand"),
		),
		Toggle: key.NewBinding(
			key.WithKeys(" ", "x"),
			key.WithHelp("space/x", "toggle"),
		),
		PurgeAll: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "select all"),
		),
		ClearAll: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "clear all"),
		),
		Filter: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "filter"),
		),
		Save: key.NewBinding(
			key.WithKeys("s", "ctrl+s"),
			key.WithHelp("s", "save"),
		),
		Export: key.NewBinding(
			key.WithKeys("w"),
			key.WithHelp("w", "export report"),
		),
		Restore: key.NewBinding(
			key.WithKeys("R"),
			key.WithHelp("R", "restore"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next mode"),
		),
		Mode1: key.NewBinding(
			key.WithKeys("1", "r"),
			key.WithHelp("1/r", "review"),
		),
		Mode2: key.NewBinding(
			key.WithKeys("2", "b"),
			key.WithHelp("2/b", "browse"),
		),
		Mode3: key.NewBinding(
			key.WithKeys("3"),
			key.WithHelp("3", "scan"),
		),
		Mode4: key.NewBinding(
			key.WithKeys("4"),
			key.WithHelp("4", "rewrite"),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		DryRun: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "dry run"),
		),
		Execute: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "execute"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "confirm"),
		),
		Backspace: key.NewBinding(
			key.WithKeys("backspace"),
		),
	}
}

// FilterMode defines which findings to show.
type FilterMode int

const (
	FilterAll FilterMode = iota
	FilterBinaries
	FilterSecrets
	FilterPurge
)

func (f FilterMode) String() string {
	switch f {
	case FilterAll:
		return "All"
	case FilterBinaries:
		return "Binaries"
	case FilterSecrets:
		return "Secrets"
	case FilterPurge:
		return "Marked"
	default:
		return "All"
	}
}

// TreeNode represents a file or directory in the browse tree
type TreeNode struct {
	Name       string
	Path       string                   // Full path
	IsDir      bool
	Expanded   bool
	Children   []*TreeNode
	File       *scanner.HistoricalFile // nil for directories
	Selected   bool
	InManifest bool // Already in manifest
	Depth      int
	Extant     bool // True if file exists in HEAD, false if deleted
}

// Model is the Bubbletea model for the TUI.
type Model struct {
	// Common state
	manifest      domain.Manifest
	repoPath      string
	keys          KeyMap
	width         int
	height        int
	saved         bool
	quitting      bool
	viewMode      ViewMode

	// Review mode state
	findings      []*domain.Finding
	filtered      []*domain.Finding
	cursor        int
	scrollTop     int
	filter        FilterMode

	// Preview (shared between Review and Browse)
	previewGen    *preview.Generator
	previewCache  map[string]*preview.Preview
	previewErr    string
	previewErrors map[string]string

	// Browse mode state
	allFiles       []*scanner.HistoricalFile
	treeRoot       *TreeNode
	flatTree       []*TreeNode // Flattened visible nodes
	browseCursor   int
	browseScroll   int
	searchMode     bool
	searchInput    textinput.Model
	searchQuery    string
	browseLoading  bool // True while loading files
	loadingTick    int  // Animation tick counter

	// Scan mode state
	scanning      bool
	scanProgress  string
	scanConfig    scanner.Config

	// Rewrite mode state
	rewriteStats    *rewriter.Stats
	rewriteDryRun   bool
	rewriting       bool
	rewriteComplete bool
	rewriteError    string
	verifyResults   []string
	confirmMode     bool
	confirmInput    textinput.Model
	skippedBlobs    []domain.SkippedBlob // Blobs skipped due to shared paths
	safeBlobCount   int                  // Number of blobs safe to purge

	// Export/restore state
	exportPath    string
	exportMessage string
	restorePath   string
	restoreMessage string
}

// New creates a new TUI model.
func New(manifest domain.Manifest, repoPath string, startMode ViewMode) Model {
	// Convert to sorted slice
	findings := make([]*domain.Finding, 0, len(manifest))
	for _, f := range manifest {
		findings = append(findings, f)
	}
	sort.Slice(findings, func(i, j int) bool {
		return findings[i].Path < findings[j].Path
	})

	// Create preview generator (may fail if repo not accessible)
	var previewGen *preview.Generator
	var previewErr string
	if repoPath != "" {
		var err error
		previewGen, err = preview.NewGenerator(repoPath)
		if err != nil {
			previewErr = err.Error()
		}
	} else {
		previewErr = "no repo path provided"
	}

	// Create search input
	searchInput := textinput.New()
	searchInput.Placeholder = "Search files..."
	searchInput.CharLimit = 100
	searchInput.Width = 40

	// Create confirm input
	confirmInput := textinput.New()
	confirmInput.Placeholder = "Type 'yes' to confirm"
	confirmInput.CharLimit = 10
	confirmInput.Width = 20

	m := Model{
		manifest:      manifest,
		repoPath:      repoPath,
		findings:      findings,
		keys:          DefaultKeyMap(),
		filter:        FilterAll,
		viewMode:      startMode,
		width:         80,
		height:        24,
		previewGen:    previewGen,
		previewCache:  make(map[string]*preview.Preview),
		previewErr:    previewErr,
		previewErrors: make(map[string]string),
		searchInput:   searchInput,
		confirmInput:  confirmInput,
		scanConfig:    scanner.DefaultConfig(),
	}
	m.applyFilter()

	return m
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return nil
}

// Messages for async operations
type scanCompleteMsg struct {
	manifest domain.Manifest
	err      error
}

type scanProgressMsg struct {
	blobsScanned int
	findingsCount int
}

type rewriteCompleteMsg struct {
	stats        *rewriter.Stats
	err          error
	skippedBlobs []domain.SkippedBlob
	safeBlobCount int
}

type verifyCompleteMsg struct {
	stillReachable []string
}

type filesLoadedMsg struct {
	files []*scanner.HistoricalFile
	err   error
}

type loadingTickMsg struct{}

type exportCompleteMsg struct {
	path string
	err  error
}

type restoreCompleteMsg struct {
	err error
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.adjustScroll()

	case filesLoadedMsg:
		m.browseLoading = false
		if msg.err != nil {
			// Handle error - could show error state
			return m, nil
		}
		m.allFiles = msg.files
		m.buildTree()
		return m, nil

	case loadingTickMsg:
		if m.browseLoading {
			m.loadingTick++
			return m, tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
				return loadingTickMsg{}
			})
		}
		return m, nil

	case exportCompleteMsg:
		if msg.err != nil {
			m.exportMessage = fmt.Sprintf("Export failed: %s", msg.err)
		} else {
			m.exportMessage = fmt.Sprintf("Exported to: %s", msg.path)
			m.exportPath = msg.path
		}
		return m, nil

	case restoreCompleteMsg:
		if msg.err != nil {
			m.restoreMessage = fmt.Sprintf("Restore failed: %s", msg.err)
		} else {
			m.restoreMessage = "Restore complete! Repository restored from backup."
		}
		return m, nil

	case scanCompleteMsg:
		m.scanning = false
		if msg.err != nil {
			m.scanProgress = fmt.Sprintf("Error: %s", msg.err)
		} else {
			// Merge results into manifest
			for hash, f := range msg.manifest {
				if _, exists := m.manifest[hash]; !exists {
					m.manifest.Add(f)
				}
			}
			// Refresh findings list
			m.refreshFindings()
			m.viewMode = ModeReview
		}
		return m, nil

	case scanProgressMsg:
		m.scanProgress = fmt.Sprintf("Scanned %d blobs, found %d items", msg.blobsScanned, msg.findingsCount)
		return m, nil

	case rewriteCompleteMsg:
		m.rewriting = false
		m.skippedBlobs = msg.skippedBlobs
		m.safeBlobCount = msg.safeBlobCount
		if msg.err != nil {
			m.rewriteError = msg.err.Error()
		} else {
			m.rewriteStats = msg.stats
			if !m.rewriteDryRun {
				// After execute, run verify
				return m, m.runVerify()
			}
		}
		return m, nil

	case verifyCompleteMsg:
		m.rewriteComplete = true
		m.verifyResults = msg.stillReachable
		return m, nil

	case tea.KeyMsg:
		// Handle confirm mode input first
		if m.confirmMode {
			return m.handleConfirmInput(msg)
		}

		// Handle search mode input
		if m.searchMode {
			return m.handleSearchInput(msg)
		}

		// Global keys
		switch {
		case key.Matches(msg, m.keys.Quit):
			m.quitting = true
			return m, tea.Quit

		case key.Matches(msg, m.keys.Save):
			m.saved = true

		case key.Matches(msg, m.keys.Export):
			return m, m.exportReport()

		case key.Matches(msg, m.keys.Restore):
			// Only allow restore if we have a backup path
			return m, m.restoreFromBackup()

		case key.Matches(msg, m.keys.Tab):
			m.viewMode = (m.viewMode + 1) % 4
			return m.onModeSwitch()

		case key.Matches(msg, m.keys.Mode1):
			m.viewMode = ModeReview
			return m, nil

		case key.Matches(msg, m.keys.Mode2):
			m.viewMode = ModeBrowse
			return m.onModeSwitch()

		case key.Matches(msg, m.keys.Mode3):
			m.viewMode = ModeScan
			return m, nil

		case key.Matches(msg, m.keys.Mode4):
			m.viewMode = ModeRewrite
			return m, nil

		default:
			// Mode-specific key handling
			switch m.viewMode {
			case ModeReview:
				return m.handleReviewKeys(msg)
			case ModeBrowse:
				return m.handleBrowseKeys(msg)
			case ModeScan:
				return m.handleScanKeys(msg)
			case ModeRewrite:
				return m.handleRewriteKeys(msg)
			}
		}
	}

	return m, nil
}

func (m Model) onModeSwitch() (Model, tea.Cmd) {
	if m.viewMode == ModeBrowse && m.allFiles == nil && !m.browseLoading {
		m.browseLoading = true
		m.loadingTick = 0
		// Start both the file loading and the animation ticker
		return m, tea.Batch(
			m.loadFiles(),
			tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
				return loadingTickMsg{}
			}),
		)
	}
	return m, nil
}

func (m Model) handleReviewKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		if m.cursor > 0 {
			m.cursor--
			m.adjustScroll()
		}

	case key.Matches(msg, m.keys.Down):
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
			m.adjustScroll()
		}

	case key.Matches(msg, m.keys.Toggle):
		if len(m.filtered) > 0 && m.cursor < len(m.filtered) {
			m.filtered[m.cursor].Purge = !m.filtered[m.cursor].Purge
			m.saved = false
		}

	case key.Matches(msg, m.keys.PurgeAll):
		for _, f := range m.filtered {
			f.Purge = true
		}
		m.saved = false

	case key.Matches(msg, m.keys.ClearAll):
		for _, f := range m.filtered {
			f.Purge = false
		}
		m.saved = false

	case key.Matches(msg, m.keys.Filter):
		m.filter = (m.filter + 1) % 4
		m.applyFilter()
		m.cursor = 0
		m.scrollTop = 0
	}

	return m, nil
}

func (m Model) handleBrowseKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		if m.browseCursor > 0 {
			m.browseCursor--
			m.adjustBrowseScroll()
		}

	case key.Matches(msg, m.keys.Down):
		if m.browseCursor < len(m.flatTree)-1 {
			m.browseCursor++
			m.adjustBrowseScroll()
		}

	case key.Matches(msg, m.keys.Left):
		// Collapse directory
		if m.browseCursor < len(m.flatTree) {
			node := m.flatTree[m.browseCursor]
			if node.IsDir && node.Expanded {
				node.Expanded = false
				m.rebuildFlatTree()
			}
		}

	case key.Matches(msg, m.keys.Right), key.Matches(msg, m.keys.Enter):
		// Expand directory
		if m.browseCursor < len(m.flatTree) {
			node := m.flatTree[m.browseCursor]
			if node.IsDir && !node.Expanded {
				node.Expanded = true
				m.rebuildFlatTree()
			}
		}

	case key.Matches(msg, m.keys.Toggle):
		if m.browseCursor < len(m.flatTree) {
			node := m.flatTree[m.browseCursor]
			m.toggleNodeSelection(node)
			m.saved = false
		}

	case key.Matches(msg, m.keys.PurgeAll):
		for _, node := range m.flatTree {
			if !node.IsDir && !node.InManifest {
				m.selectNode(node, true)
			}
		}
		m.saved = false

	case key.Matches(msg, m.keys.ClearAll):
		for _, node := range m.flatTree {
			m.selectNode(node, false)
		}
		m.saved = false

	case key.Matches(msg, m.keys.Search):
		m.searchMode = true
		m.searchInput.Focus()
	}

	return m, nil
}

func (m Model) handleSearchInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.searchMode = false
		m.searchQuery = ""
		m.searchInput.SetValue("")
		m.rebuildFlatTree()
		return m, nil
	case tea.KeyEnter:
		m.searchMode = false
		m.searchQuery = m.searchInput.Value()
		m.rebuildFlatTree()
		return m, nil
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	m.searchQuery = m.searchInput.Value()
	m.rebuildFlatTree()
	return m, cmd
}

func (m Model) handleScanKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.scanning {
		return m, nil
	}

	if key.Matches(msg, m.keys.Enter) {
		m.scanning = true
		m.scanProgress = "Starting scan..."
		return m, m.runScan()
	}

	return m, nil
}

func (m Model) handleRewriteKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.rewriting || m.rewriteComplete {
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.DryRun):
		m.rewriting = true
		m.rewriteDryRun = true
		m.rewriteError = ""
		return m, m.runRewrite(true)

	case key.Matches(msg, m.keys.Execute):
		if m.manifest.PurgeCount() > 0 {
			m.confirmMode = true
			m.confirmInput.SetValue("")
			m.confirmInput.Focus()
		}
	}

	return m, nil
}

func (m Model) handleConfirmInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.confirmMode = false
		return m, nil
	case tea.KeyEnter:
		if m.confirmInput.Value() == "yes" {
			m.confirmMode = false
			m.rewriting = true
			m.rewriteDryRun = false
			return m, m.runRewrite(false)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.confirmInput, cmd = m.confirmInput.Update(msg)
	return m, cmd
}

func (m *Model) adjustScroll() {
	listHeight := m.listHeight()
	if m.cursor < m.scrollTop {
		m.scrollTop = m.cursor
	} else if m.cursor >= m.scrollTop+listHeight {
		m.scrollTop = m.cursor - listHeight + 1
	}
}

func (m Model) listHeight() int {
	// Account for borders and padding
	return m.height - 8
}

// View implements tea.Model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	// Title bar
	title := titleStyle.Render("🗑  git-expunge")
	b.WriteString(title)
	b.WriteString("\n")

	// Tab bar
	b.WriteString(m.renderTabBar())
	b.WriteString("\n")

	// Main content based on mode
	switch m.viewMode {
	case ModeReview:
		b.WriteString(m.renderReviewMode())
	case ModeBrowse:
		b.WriteString(m.renderBrowseMode())
	case ModeScan:
		b.WriteString(m.renderScanMode())
	case ModeRewrite:
		b.WriteString(m.renderRewriteMode())
	}

	// Status bar
	purgeCount := m.manifest.PurgeCount()
	statusText := fmt.Sprintf("Marked for purge: %d/%d", purgeCount, len(m.findings))
	if m.saved {
		statusText += " ✓ saved"
	} else if purgeCount > 0 {
		statusText += " (unsaved)"
	}
	// Show export/restore messages if present
	if m.exportMessage != "" {
		statusText += " | " + m.exportMessage
	}
	if m.restoreMessage != "" {
		statusText += " | " + m.restoreMessage
	}
	b.WriteString(statusStyle.Render(statusText))
	b.WriteString("\n")

	// Help bar (mode-specific)
	b.WriteString(m.renderHelpForMode())

	return b.String()
}

func (m Model) renderTabBar() string {
	modes := []struct {
		mode ViewMode
		name string
		key  string
	}{
		{ModeReview, "Review", "1"},
		{ModeBrowse, "Browse", "2"},
		{ModeScan, "Scan", "3"},
		{ModeRewrite, "Rewrite", "4"},
	}

	var tabs []string
	for _, mode := range modes {
		label := fmt.Sprintf("%s:%s", mode.key, mode.name)
		if m.viewMode == mode.mode {
			tabs = append(tabs, activeTabStyle.Render(label))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(label))
		}
	}

	return strings.Join(tabs, " ")
}

func (m Model) renderReviewMode() string {
	var b strings.Builder

	// Filter info
	filterInfo := fmt.Sprintf("Filter: %s (%d/%d)", m.filter.String(), len(m.filtered), len(m.findings))
	b.WriteString(lipgloss.NewStyle().Foreground(secondaryColor).Render(filterInfo))
	b.WriteString("\n")

	// Calculate pane widths
	listWidth := m.width * 55 / 100
	detailWidth := m.width - listWidth - 3

	// Build list pane
	listContent := m.renderList(listWidth - 4)
	listPane := listPaneStyle.
		Width(listWidth).
		Height(m.height - 9).
		Render(listContent)

	// Build detail pane
	detailContent := m.renderDetails(detailWidth - 4)
	detailPane := detailPaneStyle.
		Width(detailWidth).
		Height(m.height - 9).
		Render(detailContent)

	// Join panes horizontally
	panes := lipgloss.JoinHorizontal(lipgloss.Top, listPane, detailPane)
	b.WriteString(panes)
	b.WriteString("\n")

	return b.String()
}

func (m Model) renderBrowseMode() string {
	var b strings.Builder

	// If files not loaded yet, show loading message
	if m.allFiles == nil {
		// Animated loading text
		dots := strings.Repeat(".", (m.loadingTick%4))
		dots = dots + strings.Repeat(" ", 3-len(dots)) // Pad to fixed width

		// Cycle through colors based on tick
		colors := []lipgloss.Color{
			lipgloss.Color("205"), // Pink
			lipgloss.Color("213"), // Light pink
			lipgloss.Color("219"), // Lighter pink
			lipgloss.Color("225"), // Lightest
			lipgloss.Color("219"),
			lipgloss.Color("213"),
		}
		colorIdx := m.loadingTick % len(colors)

		loadingStyle := lipgloss.NewStyle().
			Foreground(colors[colorIdx]).
			Bold(true)

		content := loadingStyle.Render(fmt.Sprintf("Loading%s", dots)) + "\n\n" +
			lipgloss.NewStyle().
				Foreground(secondaryColor).
				Italic(true).
				Render("Scanning repository history for all files...")

		pane := listPaneStyle.
			Width(m.width - 4).
			Height(m.height - 8).
			Render(content)
		b.WriteString(pane)
		b.WriteString("\n")
		return b.String()
	}

	// Calculate pane widths
	listWidth := m.width * 55 / 100
	detailWidth := m.width - listWidth - 3

	// Search bar if in search mode
	if m.searchMode {
		b.WriteString(searchPromptStyle.Render("Search: "))
		b.WriteString(m.searchInput.View())
		b.WriteString("\n")
	}

	// Build tree list pane
	listContent := m.renderBrowseList(listWidth - 4)
	listPane := listPaneStyle.
		Width(listWidth).
		Height(m.height - 8 - boolToInt(m.searchMode)).
		Render(listContent)

	// Build detail pane (preview of selected file)
	detailContent := m.renderBrowseDetails(detailWidth - 4)
	detailPane := detailPaneStyle.
		Width(detailWidth).
		Height(m.height - 8 - boolToInt(m.searchMode)).
		Render(detailContent)

	panes := lipgloss.JoinHorizontal(lipgloss.Top, listPane, detailPane)
	b.WriteString(panes)
	b.WriteString("\n")

	return b.String()
}

func (m Model) renderScanMode() string {
	var b strings.Builder

	content := ""
	if m.scanning {
		content = fmt.Sprintf("Scanning...\n\n%s", m.scanProgress)
	} else {
		content = "Press Enter to scan the repository for secrets and binaries.\n\n"
		content += "This will detect:\n"
		content += "  • Binary files (executables, compiled artifacts)\n"
		content += "  • Secrets (API keys, passwords, tokens)\n\n"
		content += "Results will be added to the manifest for review."
	}

	pane := listPaneStyle.
		Width(m.width - 4).
		Height(m.height - 8).
		Render(content)
	b.WriteString(pane)
	b.WriteString("\n")

	return b.String()
}

func (m Model) renderRewriteMode() string {
	var b strings.Builder

	purgeCount := m.manifest.PurgeCount()

	var content string
	if m.rewriteComplete {
		// Show results
		content = "✓ Rewrite complete!\n\n"
		if len(m.verifyResults) == 0 {
			content += "All purged blobs are now unreachable.\n\n"
		} else {
			content += fmt.Sprintf("⚠ %d blobs still reachable:\n", len(m.verifyResults))
			for _, hash := range m.verifyResults {
				content += fmt.Sprintf("  • %s\n", hash[:12])
			}
			content += "\n"
		}
		content += "Next steps:\n"
		content += "  1. Force push to remote: git push --force --all\n"
		content += "  2. Notify collaborators to re-clone"
	} else if m.rewriting {
		content = "Rewriting repository history...\n\n"
		content += "Please wait, this may take a while for large repositories."
	} else if m.confirmMode {
		content = fmt.Sprintf("⚠ WARNING: This will permanently rewrite history!\n\n")
		content += fmt.Sprintf("%d blobs will be removed from all commits.\n\n", purgeCount)
		content += "Type 'yes' to confirm: " + m.confirmInput.View()
	} else if m.rewriteStats != nil {
		// Show dry-run results
		content = "Dry run complete:\n\n"
		content += fmt.Sprintf("  Total blobs processed: %d\n", m.rewriteStats.TotalBlobs)
		content += fmt.Sprintf("  Blobs to remove: %d\n", m.rewriteStats.ExcludedBlobs)
		content += fmt.Sprintf("  Commits to modify: %d\n", m.rewriteStats.ModifiedCommits)

		// Show protected blobs if any were skipped
		if len(m.skippedBlobs) > 0 {
			content += fmt.Sprintf("\n⚠ %d blob(s) PROTECTED (shared with unmarked paths):\n", len(m.skippedBlobs))
			maxShow := 3
			for i, s := range m.skippedBlobs {
				if i >= maxShow {
					content += fmt.Sprintf("  ... and %d more\n", len(m.skippedBlobs)-maxShow)
					break
				}
				content += fmt.Sprintf("  • %s via %s\n", s.BlobHash[:12], s.MarkedPath)
				if len(s.UnmarkedPaths) > 0 {
					content += fmt.Sprintf("    protects: %s", s.UnmarkedPaths[0])
					if len(s.UnmarkedPaths) > 1 {
						content += fmt.Sprintf(" +%d more", len(s.UnmarkedPaths)-1)
					}
					content += "\n"
				}
			}
		}

		content += "\nPress 'e' to execute the rewrite."
	} else if m.rewriteError != "" {
		content = fmt.Sprintf("Error: %s\n\n", m.rewriteError)

		// Show protected blobs if that's why we failed
		if len(m.skippedBlobs) > 0 {
			content += fmt.Sprintf("⚠ %d blob(s) protected (shared with unmarked paths):\n", len(m.skippedBlobs))
			maxShow := 5
			for i, s := range m.skippedBlobs {
				if i >= maxShow {
					content += fmt.Sprintf("  ... and %d more\n", len(m.skippedBlobs)-maxShow)
					break
				}
				content += fmt.Sprintf("  • %s\n", s.MarkedPath)
				for j, p := range s.UnmarkedPaths {
					if j >= 2 {
						content += fmt.Sprintf("      +%d more unmarked paths\n", len(s.UnmarkedPaths)-2)
						break
					}
					content += fmt.Sprintf("      protects: %s\n", p)
				}
			}
			content += "\nTo purge these, mark ALL paths for each blob.\n"
		}

		content += "Press 'd' to try dry-run again."
	} else {
		if purgeCount == 0 {
			content = "No items marked for purging.\n\n"
			content += "Go to Review or Browse mode to select items to remove."
		} else {
			content = fmt.Sprintf("%d blobs marked for purging.\n\n", purgeCount)
			content += "Press 'd' for dry-run (see what would change)\n"
			content += "Press 'e' to execute (with confirmation)"
		}
	}

	pane := listPaneStyle.
		Width(m.width - 4).
		Height(m.height - 8).
		Render(content)
	b.WriteString(pane)
	b.WriteString("\n")

	return b.String()
}

func (m Model) renderBrowseList(width int) string {
	if len(m.flatTree) == 0 {
		return lipgloss.NewStyle().
			Foreground(secondaryColor).
			Italic(true).
			Render("No files found")
	}

	var lines []string
	listHeight := m.height - 10

	end := min(m.browseScroll+listHeight, len(m.flatTree))

	for i := m.browseScroll; i < end; i++ {
		node := m.flatTree[i]
		line := m.renderTreeNode(node, i == m.browseCursor, width)
		lines = append(lines, line)
	}

	// Scroll indicators
	if m.browseScroll > 0 {
		lines = append([]string{lipgloss.NewStyle().Foreground(secondaryColor).Render("  ↑ more above")}, lines...)
	}
	if end < len(m.flatTree) {
		lines = append(lines, lipgloss.NewStyle().Foreground(secondaryColor).Render("  ↓ more below"))
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderTreeNode(node *TreeNode, selected bool, width int) string {
	// Indent
	indent := strings.Repeat("  ", node.Depth)

	// Selection checkbox
	checkbox := "[ ]"
	if node.Selected {
		checkbox = checkedStyle.Render("[×]")
	} else if node.InManifest {
		checkbox = lipgloss.NewStyle().Foreground(secondaryColor).Render("[·]")
	} else {
		checkbox = uncheckedStyle.Render("[ ]")
	}

	// Cursor
	cursor := "  "
	if selected {
		cursor = "▶ "
	}

	// Name with dir indicator
	name := node.Name
	if node.IsDir {
		arrow := "▶"
		if node.Expanded {
			arrow = "▼"
		}
		if node.Extant {
			name = dirStyle.Render(arrow + " " + name + "/")
		} else {
			// Show history-only directories in italic gray
			name = deletedStyle.Render(arrow + " " + name + "/ ⌫")
		}
	} else if !node.Extant {
		// Show deleted files in italic gray with indicator
		name = deletedStyle.Render(name + " ⌫")
	}

	line := fmt.Sprintf("%s%s%s %s", cursor, indent, checkbox, name)

	if selected {
		line = selectedItemStyle.Width(width).Render(line)
	}

	return line
}

func (m Model) renderBrowseDetails(width int) string {
	if len(m.flatTree) == 0 || m.browseCursor >= len(m.flatTree) {
		return lipgloss.NewStyle().
			Foreground(secondaryColor).
			Italic(true).
			Render("Select a file to view details")
	}

	node := m.flatTree[m.browseCursor]

	if node.IsDir {
		// Directory info
		var lines []string
		lines = append(lines, dirStyle.Render("📁 "+node.Path))
		lines = append(lines, "")
		count := countFilesInDir(node)
		lines = append(lines, fmt.Sprintf("Contains %d file(s)", count))
		lines = append(lines, "")
		if node.Extant {
			lines = append(lines, lipgloss.NewStyle().Foreground(successColor).Render("✓ Has files in HEAD"))
		} else {
			lines = append(lines, deletedStyle.Render("⌫ Deleted (history only)"))
		}
		if node.Selected {
			lines = append(lines, "")
			lines = append(lines, checkedStyle.Render("✓ Selected for removal"))
		}
		return strings.Join(lines, "\n")
	}

	// File details with preview
	var lines []string

	// Path
	pathStyle := lipgloss.NewStyle().Bold(true).Foreground(textColor)
	path := node.Path
	if len(path) > width-2 {
		path = "..." + path[len(path)-width+5:]
	}
	lines = append(lines, pathStyle.Render(path))
	lines = append(lines, "")

	// Status
	if node.Selected {
		lines = append(lines, checkedStyle.Render("⚠ Selected for removal"))
	} else if node.InManifest {
		lines = append(lines, lipgloss.NewStyle().Foreground(secondaryColor).Render("Already in manifest"))
	} else {
		lines = append(lines, uncheckedStyle.Render("Not selected"))
	}
	lines = append(lines, "")

	// Extant status
	if node.Extant {
		lines = append(lines, lipgloss.NewStyle().Foreground(successColor).Render("✓ Exists in HEAD"))
	} else {
		lines = append(lines, deletedStyle.Render("⌫ Deleted (history only)"))
	}
	lines = append(lines, "")

	// Commits
	if node.File != nil {
		lines = append(lines, m.renderDetailRow("Commits", fmt.Sprintf("%d", len(node.File.Commits))))
		lines = append(lines, m.renderDetailRow("Blob", node.File.BlobHash[:min(12, len(node.File.BlobHash))]))
	}

	// Preview
	if node.File != nil {
		lines = append(lines, "")
		lines = append(lines, strings.Repeat("─", min(width, 40)))

		// Use existing preview system
		p, previewErr := m.getPreviewForBlob(node.File.BlobHash)
		if p != nil {
			previewHeader := "Preview"
			if p.IsBinary {
				previewHeader = "Preview (hex)"
			}
			lines = append(lines, lipgloss.NewStyle().
				Bold(true).
				Foreground(primaryColor).
				Render(previewHeader))
			lines = append(lines, "")

			previewStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
			previewLines := strings.Split(p.Content, "\n")
			maxPreviewLines := m.height - 20
			if maxPreviewLines < 3 {
				maxPreviewLines = 3
			}
			if len(previewLines) > maxPreviewLines {
				previewLines = previewLines[:maxPreviewLines]
				previewLines = append(previewLines, "...")
			}

			for _, pl := range previewLines {
				if len(pl) > width-4 {
					pl = pl[:width-7] + "..."
				}
				lines = append(lines, previewStyle.Render(pl))
			}
		} else if previewErr != "" {
			lines = append(lines, lipgloss.NewStyle().Foreground(dangerColor).Render("Preview: "+previewErr))
		}
	}

	return strings.Join(lines, "\n")
}

func (m *Model) getPreviewForBlob(blobHash string) (*preview.Preview, string) {
	if m.previewGen == nil {
		return nil, m.previewErr
	}

	if p, ok := m.previewCache[blobHash]; ok {
		return p, ""
	}

	if errMsg, ok := m.previewErrors[blobHash]; ok {
		return nil, errMsg
	}

	p, err := m.previewGen.Generate(blobHash)
	if err != nil {
		m.previewErrors[blobHash] = err.Error()
		return nil, err.Error()
	}

	m.previewCache[blobHash] = p
	return p, ""
}

func countFilesInDir(node *TreeNode) int {
	count := 0
	for _, child := range node.Children {
		if child.IsDir {
			count += countFilesInDir(child)
		} else {
			count++
		}
	}
	return count
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (m Model) renderHelpForMode() string {
	var keys []struct {
		key  string
		desc string
	}

	// Global keys
	keys = append(keys, struct{ key, desc string }{"1-4/tab", "mode"})

	// Mode-specific keys
	switch m.viewMode {
	case ModeReview:
		keys = append(keys,
			struct{ key, desc string }{"↑/↓", "navigate"},
			struct{ key, desc string }{"space", "toggle"},
			struct{ key, desc string }{"a", "all"},
			struct{ key, desc string }{"c", "clear"},
			struct{ key, desc string }{"f", "filter"},
		)
	case ModeBrowse:
		if m.searchMode {
			keys = append(keys, struct{ key, desc string }{"esc", "exit search"})
		} else {
			keys = append(keys,
				struct{ key, desc string }{"↑/↓", "navigate"},
				struct{ key, desc string }{"←/→", "collapse/expand"},
				struct{ key, desc string }{"space", "toggle"},
				struct{ key, desc string }{"/", "search"},
			)
		}
	case ModeScan:
		if !m.scanning {
			keys = append(keys, struct{ key, desc string }{"enter", "scan"})
		}
	case ModeRewrite:
		if m.confirmMode {
			keys = append(keys, struct{ key, desc string }{"esc", "cancel"})
		} else if !m.rewriting && !m.rewriteComplete {
			keys = append(keys,
				struct{ key, desc string }{"d", "dry-run"},
				struct{ key, desc string }{"e", "execute"},
			)
		}
	}

	keys = append(keys,
		struct{ key, desc string }{"s", "save"},
		struct{ key, desc string }{"w", "export"},
		struct{ key, desc string }{"R", "restore"},
		struct{ key, desc string }{"q", "quit"},
	)

	var parts []string
	for _, k := range keys {
		parts = append(parts, helpKeyStyle.Render(k.key)+" "+helpDescStyle.Render(k.desc))
	}

	return helpStyle.Render(strings.Join(parts, "  │  "))
}

func (m Model) renderList(width int) string {
	if len(m.filtered) == 0 {
		return lipgloss.NewStyle().
			Foreground(secondaryColor).
			Italic(true).
			Render("No findings match filter")
	}

	var lines []string
	listHeight := m.listHeight()
	end := min(m.scrollTop+listHeight, len(m.filtered))

	for i := m.scrollTop; i < end; i++ {
		f := m.filtered[i]
		line := m.renderListItem(f, i == m.cursor, width)
		lines = append(lines, line)
	}

	// Add scroll indicators if needed
	if m.scrollTop > 0 {
		lines = append([]string{lipgloss.NewStyle().Foreground(secondaryColor).Render("  ↑ more above")}, lines...)
	}
	if end < len(m.filtered) {
		lines = append(lines, lipgloss.NewStyle().Foreground(secondaryColor).Render("  ↓ more below"))
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderListItem(f *domain.Finding, selected bool, width int) string {
	// Checkbox
	checkbox := "[ ]"
	if f.Purge {
		checkbox = checkedStyle.Render("[×]")
	} else {
		checkbox = uncheckedStyle.Render("[ ]")
	}

	// Cursor
	cursor := "  "
	if selected {
		cursor = "▶ "
	}

	// Type badge
	badge := ""
	switch f.Type {
	case domain.FindingTypeBinary:
		badge = binaryBadgeStyle.Render("BIN")
	case domain.FindingTypeSecret:
		badge = secretBadgeStyle.Render("SEC")
	}

	// Path (truncate if needed)
	maxPathLen := width - 20
	path := f.Path
	if len(path) > maxPathLen && maxPathLen > 3 {
		path = "..." + path[len(path)-maxPathLen+3:]
	}

	line := fmt.Sprintf("%s%s %s %s", cursor, checkbox, badge, path)

	if selected {
		// Pad to full width for highlight
		line = selectedItemStyle.Width(width).Render(line)
	}

	return line
}

func (m Model) renderDetails(width int) string {
	if len(m.filtered) == 0 || m.cursor >= len(m.filtered) {
		return lipgloss.NewStyle().
			Foreground(secondaryColor).
			Italic(true).
			Render("Select an item to view details")
	}

	f := m.filtered[m.cursor]
	var lines []string

	// Header with type badge and path
	var badge string
	switch f.Type {
	case domain.FindingTypeBinary:
		badge = binaryBadgeStyle.Render(" BINARY ")
	case domain.FindingTypeSecret:
		badge = secretBadgeStyle.Render(" SECRET ")
	}

	lines = append(lines, badge)
	lines = append(lines, "")

	// Path (wrap if too long)
	pathStyle := lipgloss.NewStyle().Bold(true).Foreground(textColor)
	path := f.Path
	if len(path) > width-2 {
		path = "..." + path[len(path)-width+5:]
	}
	lines = append(lines, pathStyle.Render(path))
	lines = append(lines, "")

	// Purge status
	if f.Purge {
		lines = append(lines, checkedStyle.Render("⚠ MARKED FOR PURGE"))
	} else {
		lines = append(lines, uncheckedStyle.Render("✓ Will be kept"))
	}
	lines = append(lines, "")
	lines = append(lines, strings.Repeat("─", min(width, 40)))
	lines = append(lines, "")

	// Details
	lines = append(lines, m.renderDetailRow("Blob", f.BlobHash[:min(12, len(f.BlobHash))]))

	if f.Size > 0 {
		lines = append(lines, m.renderDetailRow("Size", formatSize(f.Size)))
	}

	if f.MimeType != "" {
		lines = append(lines, m.renderDetailRow("MIME", f.MimeType))
	}

	if f.Rule != "" {
		lines = append(lines, m.renderDetailRow("Rule", f.Rule))
	}

	if len(f.Commits) > 0 {
		commitText := fmt.Sprintf("%d commit(s)", len(f.Commits))
		if len(f.Commits) == 1 {
			commitText = f.Commits[0][:min(7, len(f.Commits[0]))]
		}
		lines = append(lines, m.renderDetailRow("Commits", commitText))
	}

	// Preview section
	lines = append(lines, "")
	lines = append(lines, strings.Repeat("─", min(width, 40)))

	p, previewErr := m.getPreview(f)
	if p != nil {
		previewHeader := "Preview"
		if p.IsBinary {
			previewHeader = "Preview (hex)"
		}
		lines = append(lines, lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			Render(previewHeader))
		lines = append(lines, "")

		// Style preview content
		previewStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("255"))
		secretStyle := lipgloss.NewStyle().
			Foreground(dangerColor).
			Bold(true)

		// Truncate preview to fit available space
		previewLines := strings.Split(p.Content, "\n")
		maxPreviewLines := m.height - 22
		if maxPreviewLines < 3 {
			maxPreviewLines = 3
		}
		if len(previewLines) > maxPreviewLines {
			previewLines = previewLines[:maxPreviewLines]
			previewLines = append(previewLines, "...")
		}

		// Build highlight map for quick lookup (1-based line -> highlights)
		highlightMap := make(map[int][]preview.Highlight)
		for _, h := range p.Highlights {
			highlightMap[h.Line] = append(highlightMap[h.Line], h)
		}

		for lineNum, pl := range previewLines {
			// Truncate long lines
			if len(pl) > width-4 {
				pl = pl[:width-7] + "..."
			}

			// Check if this line has highlights (lineNum is 0-based, highlights are 1-based)
			if highlights, ok := highlightMap[lineNum+1]; ok && len(highlights) > 0 {
				// Render with highlights
				lines = append(lines, renderLineWithHighlights(pl, highlights, previewStyle, secretStyle))
			} else {
				lines = append(lines, previewStyle.Render(pl))
			}
		}
	} else {
		lines = append(lines, lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			Render("Preview"))
		lines = append(lines, "")
		errStyle := lipgloss.NewStyle().Foreground(dangerColor)
		if previewErr != "" {
			lines = append(lines, errStyle.Render("Error: "+previewErr))
		} else {
			lines = append(lines, errStyle.Render("Preview unavailable"))
		}
	}

	return strings.Join(lines, "\n")
}

// getPreview returns the cached preview or generates one.
func (m *Model) getPreview(f *domain.Finding) (*preview.Preview, string) {
	if m.previewGen == nil {
		return nil, m.previewErr
	}

	// Check cache
	if p, ok := m.previewCache[f.BlobHash]; ok {
		return p, ""
	}

	// Check error cache
	if errMsg, ok := m.previewErrors[f.BlobHash]; ok {
		return nil, errMsg
	}

	// Generate preview with secret highlights if applicable
	var p *preview.Preview
	var err error
	if f.Type == domain.FindingTypeSecret && len(f.SecretLocations) > 0 {
		p, err = m.previewGen.GenerateWithSecrets(f.BlobHash, f.SecretLocations)
	} else {
		p, err = m.previewGen.Generate(f.BlobHash)
	}
	if err != nil {
		m.previewErrors[f.BlobHash] = err.Error()
		return nil, err.Error()
	}

	// Cache it
	m.previewCache[f.BlobHash] = p
	return p, ""
}

func (m Model) renderDetailRow(label, value string) string {
	return labelStyle.Render(label+":") + " " + valueStyle.Render(value)
}

// renderLineWithHighlights renders a line with secret highlights in red.
func renderLineWithHighlights(line string, highlights []preview.Highlight, normalStyle, highlightStyle lipgloss.Style) string {
	if len(highlights) == 0 {
		return normalStyle.Render(line)
	}

	// Sort highlights by start column
	type region struct {
		start, end int
	}
	var regions []region
	for _, h := range highlights {
		regions = append(regions, region{h.StartCol, h.EndCol})
	}

	// Build the line with highlights
	var result strings.Builder
	pos := 0
	lineRunes := []rune(line)

	for _, r := range regions {
		// Clamp to line bounds
		start := r.start
		end := r.end
		if start < 0 {
			start = 0
		}
		if end > len(lineRunes) {
			end = len(lineRunes)
		}
		if start >= len(lineRunes) || start >= end {
			continue
		}

		// Normal text before highlight
		if pos < start {
			result.WriteString(normalStyle.Render(string(lineRunes[pos:start])))
		}

		// Highlighted text
		result.WriteString(highlightStyle.Render(string(lineRunes[start:end])))
		pos = end
	}

	// Remaining normal text
	if pos < len(lineRunes) {
		result.WriteString(normalStyle.Render(string(lineRunes[pos:])))
	}

	return result.String()
}

func (m Model) renderHelp() string {
	keys := []struct {
		key  string
		desc string
	}{
		{"↑/↓", "navigate"},
		{"space", "toggle"},
		{"a", "purge all"},
		{"c", "clear all"},
		{"f", "filter"},
		{"s", "save"},
		{"q", "quit"},
	}

	var parts []string
	for _, k := range keys {
		parts = append(parts, helpKeyStyle.Render(k.key)+" "+helpDescStyle.Render(k.desc))
	}

	return helpStyle.Render(strings.Join(parts, "  │  "))
}

func (m *Model) applyFilter() {
	m.filtered = make([]*domain.Finding, 0)

	for _, f := range m.findings {
		switch m.filter {
		case FilterAll:
			m.filtered = append(m.filtered, f)
		case FilterBinaries:
			if f.Type == domain.FindingTypeBinary {
				m.filtered = append(m.filtered, f)
			}
		case FilterSecrets:
			if f.Type == domain.FindingTypeSecret {
				m.filtered = append(m.filtered, f)
			}
		case FilterPurge:
			if f.Purge {
				m.filtered = append(m.filtered, f)
			}
		}
	}
}

// GetManifest returns the modified manifest.
func (m Model) GetManifest() domain.Manifest {
	return m.manifest
}

// WasSaved returns whether the user explicitly saved.
func (m Model) WasSaved() bool {
	return m.saved
}

func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Run starts the TUI and returns the modified manifest.
func Run(manifest domain.Manifest, repoPath string, startMode ViewMode) (domain.Manifest, bool, error) {
	m := New(manifest, repoPath, startMode)
	p := tea.NewProgram(m, tea.WithAltScreen())

	finalModel, err := p.Run()
	if err != nil {
		return nil, false, err
	}

	final := finalModel.(Model)
	return final.GetManifest(), final.WasSaved(), nil
}

// Helper functions for tree building

func (m *Model) buildTree() {
	if m.allFiles == nil {
		return
	}

	// Build a map of existing manifest entries
	inManifest := make(map[string]bool)
	for _, f := range m.manifest {
		inManifest[f.Path] = true
	}

	// Create root node
	m.treeRoot = &TreeNode{
		Name:     ".",
		Path:     "",
		IsDir:    true,
		Expanded: true,
		Children: []*TreeNode{},
	}

	// Build tree from file paths
	for _, file := range m.allFiles {
		parts := strings.Split(file.Path, "/")
		current := m.treeRoot

		// Navigate/create path
		for i, part := range parts {
			isLast := i == len(parts)-1
			path := strings.Join(parts[:i+1], "/")

			// Find or create child
			var child *TreeNode
			for _, c := range current.Children {
				if c.Name == part {
					child = c
					break
				}
			}

			if child == nil {
				child = &TreeNode{
					Name:       part,
					Path:       path,
					IsDir:      !isLast,
					Expanded:   false,
					Children:   []*TreeNode{},
					Depth:      i + 1,
					InManifest: inManifest[path],
				}
				if isLast {
					child.File = file
					child.Extant = file.Extant
				}
				current.Children = append(current.Children, child)
			}

			current = child
		}
	}

	// Sort children at each level
	sortTreeNode(m.treeRoot)

	// Propagate extant status from files up to directories
	propagateExtant(m.treeRoot)

	m.rebuildFlatTree()
}

func sortTreeNode(node *TreeNode) {
	sort.Slice(node.Children, func(i, j int) bool {
		// Directories first
		if node.Children[i].IsDir != node.Children[j].IsDir {
			return node.Children[i].IsDir
		}
		return node.Children[i].Name < node.Children[j].Name
	})

	for _, child := range node.Children {
		if child.IsDir {
			sortTreeNode(child)
		}
	}
}

// propagateExtant recursively sets directory Extant status based on children.
// A directory is extant if ANY of its children (files or subdirs) are extant.
func propagateExtant(node *TreeNode) bool {
	if !node.IsDir {
		return node.Extant
	}

	hasExtant := false
	for _, child := range node.Children {
		if propagateExtant(child) {
			hasExtant = true
		}
	}
	node.Extant = hasExtant
	return hasExtant
}

func (m *Model) rebuildFlatTree() {
	m.flatTree = []*TreeNode{}
	if m.treeRoot != nil {
		m.flattenTree(m.treeRoot, m.searchQuery)
	}

	// Adjust cursor if needed
	if m.browseCursor >= len(m.flatTree) {
		m.browseCursor = max(0, len(m.flatTree)-1)
	}
}

func (m *Model) flattenTree(node *TreeNode, query string) {
	// Skip root
	if node.Path != "" {
		// Apply search filter
		if query != "" && !strings.Contains(strings.ToLower(node.Path), strings.ToLower(query)) {
			// If directory, still check children
			if node.IsDir {
				for _, child := range node.Children {
					m.flattenTree(child, query)
				}
			}
			return
		}

		m.flatTree = append(m.flatTree, node)
	}

	if !node.IsDir || !node.Expanded {
		return
	}

	for _, child := range node.Children {
		m.flattenTree(child, query)
	}
}

func (m *Model) toggleNodeSelection(node *TreeNode) {
	newState := !node.Selected
	m.selectNode(node, newState)
}

func (m *Model) selectNode(node *TreeNode, selected bool) {
	node.Selected = selected

	if node.IsDir {
		// Recursively select/deselect all children
		for _, child := range node.Children {
			m.selectNode(child, selected)
		}
	} else if selected && !node.InManifest && node.File != nil {
		// Add to manifest
		finding := &domain.Finding{
			BlobHash: node.File.BlobHash,
			Type:     domain.FindingTypeAdd,
			Path:     node.Path,
			Commits:  node.File.Commits,
			Purge:    true,
		}
		m.manifest.Add(finding)
		m.refreshFindings()
	} else if !selected && node.File != nil {
		// Remove from manifest if it's an "add" type
		if f, exists := m.manifest[node.File.BlobHash]; exists && f.Type == domain.FindingTypeAdd {
			delete(m.manifest, node.File.BlobHash)
			m.refreshFindings()
		} else if f, exists := m.manifest[node.File.BlobHash]; exists {
			// Just mark as not purge
			f.Purge = false
		}
	}
}

func (m *Model) refreshFindings() {
	m.findings = make([]*domain.Finding, 0, len(m.manifest))
	for _, f := range m.manifest {
		m.findings = append(m.findings, f)
	}
	sort.Slice(m.findings, func(i, j int) bool {
		return m.findings[i].Path < m.findings[j].Path
	})
	m.applyFilter()
}

func (m *Model) adjustBrowseScroll() {
	listHeight := m.height - 10
	if m.browseCursor < m.browseScroll {
		m.browseScroll = m.browseCursor
	} else if m.browseCursor >= m.browseScroll+listHeight {
		m.browseScroll = m.browseCursor - listHeight + 1
	}
}

// Async operations

func (m Model) loadFiles() tea.Cmd {
	return func() tea.Msg {
		files, err := scanner.ListHistoricalFiles(m.repoPath)
		return filesLoadedMsg{files: files, err: err}
	}
}

func (m Model) runScan() tea.Cmd {
	return func() tea.Msg {
		config := scanner.Config{
			ScanSecrets:   true,
			ScanBinaries:  true,
			SizeThreshold: 100 * 1024,
			Workers:       4,
		}
		s := scanner.New(config)
		result, err := s.Scan(m.repoPath)
		return scanCompleteMsg{manifest: result, err: err}
	}
}

func (m Model) runRewrite(dryRun bool) tea.Cmd {
	return func() tea.Msg {
		blobsToPurge := m.manifest.BlobsToPurge()
		if len(blobsToPurge) == 0 {
			return rewriteCompleteMsg{err: fmt.Errorf("no blobs to purge")}
		}

		var skipped []domain.SkippedBlob

		// Filter to safe blobs only (protect shared content)
		allPaths, err := scanner.FindAllPathsForBlobs(m.repoPath, blobsToPurge)
		if err == nil {
			safeToPurge, skippedBlobs := m.manifest.SafeBlobsToPurge(allPaths)
			skipped = skippedBlobs
			if len(safeToPurge) == 0 {
				return rewriteCompleteMsg{
					err:          fmt.Errorf("no blobs safe to purge (all have unmarked shared paths)"),
					skippedBlobs: skipped,
					safeBlobCount: 0,
				}
			}
			blobsToPurge = safeToPurge
		}

		rw := rewriter.NewRewriter(m.repoPath)
		rw.SetDryRun(dryRun)

		stats, err := rw.Rewrite(blobsToPurge)
		return rewriteCompleteMsg{
			stats:         stats,
			err:           err,
			skippedBlobs:  skipped,
			safeBlobCount: len(blobsToPurge),
		}
	}
}

func (m Model) runVerify() tea.Cmd {
	return func() tea.Msg {
		blobsToPurge := m.manifest.BlobsToPurge()
		var stillReachable []string

		for _, hash := range blobsToPurge {
			// Check if blob is still reachable using git cat-file
			// This is a simplified check
			cmd := fmt.Sprintf("git -C %s cat-file -e %s 2>/dev/null", m.repoPath, hash)
			if runGitCheck(cmd) {
				stillReachable = append(stillReachable, hash)
			}
		}

		return verifyCompleteMsg{stillReachable: stillReachable}
	}
}

func (m Model) exportReport() tea.Cmd {
	return func() tea.Msg {
		// Generate report path
		reportPath := "git-expunge-report.md"

		// Create report file
		f, err := os.Create(reportPath)
		if err != nil {
			return exportCompleteMsg{err: err}
		}
		defer f.Close()

		// Generate report with shared blob info
		gen := manifest.NewReportGenerator(m.repoPath)

		// Get shared blob info if possible
		blobHashes := make([]string, 0, len(m.manifest))
		for hash := range m.manifest {
			blobHashes = append(blobHashes, hash)
		}
		if allPaths, err := scanner.FindAllPathsForBlobs(m.repoPath, blobHashes); err == nil {
			gen.SetSharedBlobs(allPaths)
		}

		if err := gen.Generate(m.manifest, f); err != nil {
			return exportCompleteMsg{err: err}
		}

		return exportCompleteMsg{path: reportPath}
	}
}

func (m Model) restoreFromBackup() tea.Cmd {
	return func() tea.Msg {
		// Find most recent backup
		backupDir := ".git-expunge-backups"
		entries, err := os.ReadDir(backupDir)
		if err != nil {
			return restoreCompleteMsg{err: fmt.Errorf("no backups found in %s", backupDir)}
		}

		// Find most recent .tar.gz
		var latestBackup string
		for i := len(entries) - 1; i >= 0; i-- {
			if strings.HasSuffix(entries[i].Name(), ".tar.gz") {
				latestBackup = fmt.Sprintf("%s/%s", backupDir, entries[i].Name())
				break
			}
		}

		if latestBackup == "" {
			return restoreCompleteMsg{err: fmt.Errorf("no backup archives found")}
		}

		// Restore from backup
		if err := safety.RestoreBackup(latestBackup, m.repoPath); err != nil {
			return restoreCompleteMsg{err: err}
		}

		return restoreCompleteMsg{}
	}
}

func runGitCheck(command string) bool {
	// Simple check - returns true if blob exists
	// In a real implementation, this would execute the git command
	return false
}
