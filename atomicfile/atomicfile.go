package atomicfile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// Write creates parent dirs, skips write when content is unchanged,
// otherwise writes to a temp file in dir and renames to dir/name.
func Write(dir, name, content string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	final := filepath.Join(dir, name)
	newContent := []byte(content)
	existing, err := os.ReadFile(final)
	if err == nil {
		if bytes.Equal(existing, newContent) {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read existing %s: %w", final, err)
	}

	f, err := os.CreateTemp(dir, "."+name+".tmp.")
	if err != nil {
		return fmt.Errorf("createtemp in %s: %w", dir, err)
	}
	tmpPath := f.Name()

	if _, err := f.Write(newContent); err != nil {
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

	if err := os.Rename(tmpPath, final); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename to %s: %w", final, err)
	}
	return nil
}
