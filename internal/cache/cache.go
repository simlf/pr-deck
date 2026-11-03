// Package cache stores the latest snapshot so every view (TUI, strip, herdr
// tokens) reads the same data without hitting the APIs.
package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"

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

// Update applies fn to the cached snapshot under an exclusive lock, so the
// daemon and a board never overwrite each other's writes. fn returns the
// snapshot to save, or false to leave the file as it is.
func Update(path string, fn func(cur model.Snapshot, ok bool) (model.Snapshot, bool)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	cur, ok, _ := Load(path)
	next, save := fn(cur, ok)
	if !save {
		return nil
	}
	return Save(path, next)
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
