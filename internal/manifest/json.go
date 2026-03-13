// Package manifest handles manifest file operations.
package manifest

import (
	"encoding/json"

	"github.com/benjaminabbitt/git-expunge/internal/domain"
	"github.com/spf13/afero"
)

// Fs is the filesystem used by the manifest package. Defaults to the OS filesystem.
var Fs afero.Fs = afero.NewOsFs()

// WriteJSON writes a manifest to a JSON file.
func WriteJSON(manifest domain.Manifest, path string) error {
	return WriteJSONWithFs(Fs, manifest, path)
}

// WriteJSONWithFs writes a manifest to a JSON file using the provided filesystem.
func WriteJSONWithFs(fs afero.Fs, manifest domain.Manifest, path string) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return afero.WriteFile(fs, path, data, 0644)
}

// ReadJSON reads a manifest from a JSON file.
func ReadJSON(path string) (domain.Manifest, error) {
	return ReadJSONWithFs(Fs, path)
}

// ReadJSONWithFs reads a manifest from a JSON file using the provided filesystem.
func ReadJSONWithFs(fs afero.Fs, path string) (domain.Manifest, error) {
	data, err := afero.ReadFile(fs, path)
	if err != nil {
		return nil, err
	}

	var manifest domain.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}

	return manifest, nil
}
