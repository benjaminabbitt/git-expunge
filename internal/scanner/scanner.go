// Package scanner provides detection of binaries and secrets in git repositories.
package scanner

import (
	"runtime"
	"sync"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
)

// RepoWalker abstracts repository walking for testing.
type RepoWalker interface {
	Walk(handler BlobHandler) error
}

// WalkerFactory creates RepoWalkers for a given path.
type WalkerFactory func(repoPath string) (RepoWalker, error)

// DefaultWalkerFactory creates real git walkers.
func DefaultWalkerFactory(repoPath string) (RepoWalker, error) {
	return NewWalker(repoPath)
}

// Config holds scanner configuration.
type Config struct {
	// ScanSecrets enables secret scanning.
	ScanSecrets bool

	// ScanBinaries enables binary detection.
	ScanBinaries bool

	// SizeThreshold is the minimum file size for binary detection (bytes).
	SizeThreshold int64

	// Workers is the number of parallel workers for scanning.
	// Defaults to number of CPUs.
	Workers int

	// ProgressFunc is called periodically with scan progress.
	ProgressFunc func(blobsScanned, findingsCount int)
}

// DefaultConfig returns default scanner configuration.
func DefaultConfig() Config {
	return Config{
		ScanSecrets:   true,
		ScanBinaries:  true,
		SizeThreshold: 100 * 1024, // 100KB
		Workers:       runtime.NumCPU(),
	}
}

// Scanner orchestrates the detection of binaries and secrets.
type Scanner struct {
	config         Config
	binaryDetector *BinaryDetector
	secretDetector *SecretDetector
	walkerFactory  WalkerFactory
}

// New creates a new Scanner with the given configuration.
func New(config Config) *Scanner {
	s := &Scanner{
		config:         config,
		binaryDetector: NewBinaryDetector(config.SizeThreshold),
		walkerFactory:  DefaultWalkerFactory,
	}

	// Initialize secret detector if secret scanning is enabled
	if config.ScanSecrets {
		detector, err := NewSecretDetector()
		if err == nil {
			s.secretDetector = detector
		}
		// If secret detector fails to initialize, we continue without it
		// This allows scanning to proceed with binary detection only
	}

	return s
}

// WithWalkerFactory sets a custom walker factory (for testing).
func (s *Scanner) WithWalkerFactory(factory WalkerFactory) *Scanner {
	s.walkerFactory = factory
	return s
}

// Scan scans a repository and returns a manifest of findings.
func (s *Scanner) Scan(repoPath string) (domain.Manifest, error) {
	walker, err := s.walkerFactory(repoPath)
	if err != nil {
		return nil, err
	}

	// Use parallel scanning
	workers := s.config.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	return s.scanParallel(walker, workers)
}

// scanParallel uses a worker pool for parallel blob processing.
// Architecture:
//   - Single producer goroutine walks the repo and sends unique blobs to blobChan
//   - Multiple worker goroutines receive from blobChan (Go guarantees each blob
//     goes to exactly one worker) and process them in parallel
//   - Single collector goroutine receives findings and builds the manifest
func (s *Scanner) scanParallel(walker RepoWalker, numWorkers int) (domain.Manifest, error) {
	// Channels for fan-out (blobs) and fan-in (findings)
	blobChan := make(chan *BlobInfo, numWorkers*2)
	resultChan := make(chan []*domain.Finding, numWorkers*2)
	errChan := make(chan error, 1)

	// Start worker pool - each worker receives unique blobs from the channel
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.worker(blobChan, resultChan)
		}()
	}

	// Close result channel when all workers are done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Producer: walk repository and send blobs to workers
	// The walker already deduplicates blobs, so each blob is sent exactly once
	go func() {
		err := walker.Walk(func(blob *BlobInfo) error {
			blobChan <- blob
			return nil
		})
		close(blobChan) // Signal workers to exit when all blobs are sent
		if err != nil {
			select {
			case errChan <- err:
			default:
			}
		}
	}()

	// Collector: gather results into manifest
	manifest := domain.NewManifest()
	blobsScanned := 0

	for findings := range resultChan {
		for _, f := range findings {
			manifest.Add(f)
		}
		blobsScanned++

		// Report progress
		if s.config.ProgressFunc != nil && blobsScanned%100 == 0 {
			s.config.ProgressFunc(blobsScanned, len(manifest))
		}
	}

	// Final progress report
	if s.config.ProgressFunc != nil {
		s.config.ProgressFunc(blobsScanned, len(manifest))
	}

	// Check for walk errors
	select {
	case err := <-errChan:
		return nil, err
	default:
	}

	return manifest, nil
}

// worker processes blobs from the channel and sends findings to results.
func (s *Scanner) worker(blobs <-chan *BlobInfo, results chan<- []*domain.Finding) {
	for blob := range blobs {
		var findings []*domain.Finding

		// Check for binary
		if s.config.ScanBinaries {
			if finding := s.binaryDetector.Detect(blob); finding != nil {
				findings = append(findings, finding)
			}
		}

		// Check for secrets
		if s.config.ScanSecrets && s.secretDetector != nil {
			secretFindings := s.secretDetector.Detect(blob)
			findings = append(findings, secretFindings...)
		}

		results <- findings
	}
}
