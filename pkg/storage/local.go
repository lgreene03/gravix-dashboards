package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalStore implements ObjectStore using the local filesystem.
type LocalStore struct {
	baseDir string
}

func NewLocalStore(baseDir string) (*LocalStore, error) {
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve base dir: %w", err)
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base dir: %w", err)
	}
	return &LocalStore{baseDir: abs}, nil
}

// sanitizeKey rejects keys that would escape the base directory via path traversal.
func (l *LocalStore) sanitizeKey(key string) (string, error) {
	cleaned := filepath.Clean(key)
	if strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("invalid key %q: path traversal not allowed", key)
	}
	full := filepath.Join(l.baseDir, cleaned)
	if !strings.HasPrefix(full, l.baseDir) {
		return "", fmt.Errorf("invalid key %q: resolves outside base directory", key)
	}
	return full, nil
}

func (l *LocalStore) Put(ctx context.Context, key string, reader io.Reader) error {
	path, err := l.sanitizeKey(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, reader); err != nil {
		return err
	}
	return f.Sync()
}

func (l *LocalStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	path, err := l.sanitizeKey(key)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (l *LocalStore) Delete(ctx context.Context, key string) error {
	path, err := l.sanitizeKey(key)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (l *LocalStore) Exists(ctx context.Context, key string) (bool, error) {
	path, err := l.sanitizeKey(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (l *LocalStore) List(ctx context.Context, prefix string) ([]string, error) {
	searchDir, err := l.sanitizeKey(prefix)
	if err != nil {
		return nil, err
	}
	var keys []string

	err = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == searchDir {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(l.baseDir, path)
		if err != nil {
			return err
		}
		keys = append(keys, rel)
		return nil
	})

	if err != nil {
		return nil, err
	}
	return keys, nil
}
