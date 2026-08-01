package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func New(root string) (*Disk, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("failed to create root directory: %w", err)
	}

	// Stat to ensure it's a directory, not a file that was created by MkdirAll
	if info, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("failed to stat root directory: %w", err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("root path is not a directory: %s", root)
	}

	return &Disk{root: root}, nil
}

type Disk struct {
	root string
}

func (d *Disk) Put(ctx context.Context, blob []byte) (string, error) {
	hash := sha256.Sum256(blob)
	ref := hex.EncodeToString(hash[:])

	// Path is root/ref[0:2]/ref[2:4]/ref
	dirPath := filepath.Join(d.root, ref[:2], ref[2:4])
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory %q: %w", dirPath, err)
	}

	filePath := filepath.Join(dirPath, ref)

	// Check if file already exists to ensure idempotency
	if _, err := os.Stat(filePath); err == nil {
		return ref, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("failed to stat file %q: %w", filePath, err)
	}

	// Atomic-ish write: write to temp file then rename
	tmpPath := filePath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file %q: %w", tmpPath, err)
	}

	if _, err := f.Write(blob); err != nil {
		closeErr := f.Close()
		os.Remove(tmpPath)
		if closeErr != nil {
			return "", fmt.Errorf("failed to write to temp file %q: %v; additionally failed to close temp file: %w", tmpPath, err, closeErr)
		}
		return "", fmt.Errorf("failed to write to temp file %q: %w", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to close temp file %q: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to rename temp file %q to %q: %w", tmpPath, filePath, err)
	}

	return ref, nil
}

func (d *Disk) Get(ctx context.Context, ref string) ([]byte, error) {
	filePath := filepath.Join(d.root, ref[:2], ref[2:4], ref)

	data, err := os.ReadFile(filePath)
	if err != nil {
		// Return the error as-is to allow os.IsNotExist checks on the caller's side.
		return nil, err
	}

	return data, nil
}
