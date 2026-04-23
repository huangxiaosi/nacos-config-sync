package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write creates parent dirs, writes content to a temp file in dir, then renames to dir/name.
func Write(dir, name, content string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	f, err := os.CreateTemp(dir, "."+name+".tmp.")
	if err != nil {
		return fmt.Errorf("createtemp in %s: %w", dir, err)
	}
	tmpPath := f.Name()

	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}

	final := filepath.Join(dir, name)
	if err := os.Rename(tmpPath, final); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename to %s: %w", final, err)
	}
	return nil
}
