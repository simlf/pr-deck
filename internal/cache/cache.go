// Package cache stores the latest snapshot so every view (TUI, strip, herdr
// tokens) reads the same data without hitting the APIs.
package cache

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/simlf/pr-deck/internal/model"
)

// SnapshotPath is the snapshot file inside dir.
func SnapshotPath(dir string) string { return filepath.Join(dir, "snapshot.json") }

// Save writes snap atomically.
func Save(path string, snap model.Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "snapshot-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Load reads a snapshot; ok is false when none exists yet.
func Load(path string) (model.Snapshot, bool, error) {
	var s model.Snapshot
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, false, err
	}
	return s, true, nil
}
