package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteJSON writes data as pretty-printed JSON to a temporary file in the same directory
// as targetPath, sets the file permissions to perm, flushes via Sync(), closes the file, and
// atomically renames it to targetPath. This prevents corrupted or partially-written files on crash.
func AtomicWriteJSON(targetPath string, data any, perm os.FileMode) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	base := filepath.Base(targetPath)
	tmpFile, err := os.CreateTemp(dir, fmt.Sprintf(".%s-*.tmp", base))
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	encoder := json.NewEncoder(tmpFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("encode json: %w", err)
	}

	if err := tmpFile.Chmod(perm); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("atomic rename %s to %s: %w", tmpName, targetPath, err)
	}

	return nil
}

// ReadJSON opens the file at path and decodes its JSON content into out.
// If the file does not exist, os.ErrNotExist is returned directly or unwrappable via errors.Is.
func ReadJSON(path string, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(out); err != nil {
		return fmt.Errorf("decode json from %s: %w", path, err)
	}

	return nil
}
