// Package scanner provides detection of binaries, secrets, oversized
// blobs, and gitignored-by-current-rules paths in git repositories. Each
// detector covers an orthogonal axis; multiple detectors share the same
// history walk where possible (gitignored runs as a separate pass
// because it needs the unique-path set, not per-blob content).
package scanner

import (
	"runtime"
	"sync"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/benjaminabbitt/git-expunge/internal/gitquery"
	"github.com/benjaminabbitt/git-expunge/internal/retroignore"
)

// RepoWalker abstracts repository walking for testing.
type RepoWalker interface {
	Walk(handler gitquery.BlobHandler) error
}

// WalkerFactory creates RepoWalkers for a given path.
type WalkerFactory func(repoPath string) (RepoWalker, error)

// DefaultWalkerFactory creates real git walkers.
func DefaultWalkerFactory(repoPath string) (RepoWalker, error) {
	return gitquery.NewWalker(repoPath)
}

// GitignoreScanner is the function signature for the post-walk
// gitignored pass. It walks history once on its own and emits a manifest
// of blobs whose paths match the repository's current gitignore rules.
// The default implementation calls retroignore.BuildManifest; tests can
// substitute a mock via WithGitignoreScanner.
type GitignoreScanner func(repoPath string, existing domain.Manifest) (domain.Manifest, error)

// Config holds scanner configuration. Each ScanX flag is orthogonal:
// disabling one detector does not disable the others.
type Config struct {
	// ScanSecrets enables the gitleaks-based secret detector.
	ScanSecrets bool

	// ScanBinaries enables MIME/magic-byte binary detection (any size).
	ScanBinaries bool

	// ScanLargeFiles enables size-only large-file detection (any MIME).
	ScanLargeFiles bool

	// ScanGitignored enables a post-walk pass that flags blobs whose
	// paths would be ignored under the repository's current gitignore
	// rules.
	ScanGitignored bool

	// SizeThreshold is the minimum blob size (bytes) for the
	// LargeFileDetector. Has no effect on the other detectors.
	SizeThreshold int64

	// Workers is the number of parallel workers for the per-blob
	// detection phase. Defaults to runtime.NumCPU().
	Workers int

	// ProgressFunc is called periodically with scan progress.
	ProgressFunc func(blobsScanned, findingsCount int)
}

// DefaultConfig returns the safe default scanner configuration: only the
// high-confidence detectors (secrets, gitignored) are on. Binary and
// large-file detection are noisy on real repositories and require the
// caller to opt in. SizeThreshold is still set so callers that flip
// ScanLargeFiles on get a sane default.
func DefaultConfig() Config {
	return Config{
		ScanSecrets:    true,
		ScanBinaries:   false,
		ScanLargeFiles: false,
		ScanGitignored: true,
		SizeThreshold:  100 * 1024, // 100KB
		Workers:        runtime.NumCPU(),
	}
}

// Scanner orchestrates the detectors against a repository.
type Scanner struct {
	config            Config
	binaryDetector    *BinaryDetector
	largeFileDetector *LargeFileDetector
	secretDetector    *SecretDetector
	walkerFactory     WalkerFactory
	gitignoreScanner  GitignoreScanner
}

// New creates a new Scanner with the given configuration.
func New(config Config) *Scanner {
	s := &Scanner{
		config:            config,
		binaryDetector:    NewBinaryDetector(),
		largeFileDetector: NewLargeFileDetector(config.SizeThreshold),
		walkerFactory:     DefaultWalkerFactory,
		gitignoreScanner:  retroignore.BuildManifest,
	}

	if config.ScanSecrets {
		detector, err := NewSecretDetector()
		if err == nil {
			s.secretDetector = detector
		}
		// If secret detector fails to initialize we continue with the
		// other detectors rather than aborting the run.
	}

	return s
}

// WithWalkerFactory sets a custom walker factory (for testing).
func (s *Scanner) WithWalkerFactory(factory WalkerFactory) *Scanner {
	s.walkerFactory = factory
	return s
}

// WithGitignoreScanner substitutes the post-walk gitignored pass (for
// testing).
func (s *Scanner) WithGitignoreScanner(scan GitignoreScanner) *Scanner {
	s.gitignoreScanner = scan
	return s
}

// Scan runs every enabled detector against the repository and returns
// the merged manifest of findings.
func (s *Scanner) Scan(repoPath string) (domain.Manifest, error) {
	manifest := domain.NewManifest()

	// Per-blob detectors (binaries, secrets, large) share a single
	// history walk.
	if s.config.ScanBinaries || s.config.ScanSecrets || s.config.ScanLargeFiles {
		walker, err := s.walkerFactory(repoPath)
		if err != nil {
			return nil, err
		}

		workers := s.config.Workers
		if workers <= 0 {
			workers = runtime.NumCPU()
		}

		walkResult, err := s.scanParallel(walker, workers)
		if err != nil {
			return nil, err
		}
		manifest.Merge(walkResult)
	}

	// Gitignored is a separate pass: it needs per-(path, blob) tuples,
	// not per-blob.
	if s.config.ScanGitignored {
		gi, err := s.gitignoreScanner(repoPath, nil)
		if err != nil {
			return nil, err
		}
		manifest.Merge(gi)
	}

	return manifest, nil
}

// scanParallel uses a worker pool for parallel blob processing.
// Architecture:
//   - Single producer goroutine walks the repo and sends unique blobs to blobChan
//   - Multiple worker goroutines receive from blobChan (Go guarantees each blob
//     goes to exactly one worker) and process them in parallel
//   - Single collector goroutine receives findings and builds the manifest
func (s *Scanner) scanParallel(walker RepoWalker, numWorkers int) (domain.Manifest, error) {
	blobChan := make(chan *gitquery.BlobInfo, numWorkers*2)
	resultChan := make(chan []*domain.Finding, numWorkers*2)
	errChan := make(chan error, 1)

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.worker(blobChan, resultChan)
		}()
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	go func() {
		err := walker.Walk(func(blob *gitquery.BlobInfo) error {
			blobChan <- blob
			return nil
		})
		close(blobChan)
		if err != nil {
			select {
			case errChan <- err:
			default:
			}
		}
	}()

	manifest := domain.NewManifest()
	blobsScanned := 0

	for findings := range resultChan {
		for _, f := range findings {
			manifest.Add(f)
		}
		blobsScanned++

		if s.config.ProgressFunc != nil && blobsScanned%100 == 0 {
			s.config.ProgressFunc(blobsScanned, len(manifest))
		}
	}

	if s.config.ProgressFunc != nil {
		s.config.ProgressFunc(blobsScanned, len(manifest))
	}

	select {
	case err := <-errChan:
		return nil, err
	default:
	}

	return manifest, nil
}

// worker processes blobs from the channel and sends findings to results.
func (s *Scanner) worker(blobs <-chan *gitquery.BlobInfo, results chan<- []*domain.Finding) {
	for blob := range blobs {
		var findings []*domain.Finding

		if s.config.ScanBinaries {
			if finding := s.binaryDetector.Detect(blob); finding != nil {
				findings = append(findings, finding)
			}
		}

		if s.config.ScanLargeFiles {
			if finding := s.largeFileDetector.Detect(blob); finding != nil {
				findings = append(findings, finding)
			}
		}

		if s.config.ScanSecrets && s.secretDetector != nil {
			secretFindings := s.secretDetector.Detect(blob)
			findings = append(findings, secretFindings...)
		}

		results <- findings
	}
}
