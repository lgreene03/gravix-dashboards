package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LocalStore implements ObjectStore using the local filesystem.
type LocalStore struct {
	baseDir string
}

func NewLocalStore(baseDir string) (*LocalStore, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base dir: %w", err)
	}
	return &LocalStore{baseDir: baseDir}, nil
}

func (l *LocalStore) Put(ctx context.Context, key string, reader io.Reader) error {
	path := filepath.Join(l.baseDir, key)
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
	path := filepath.Join(l.baseDir, key)
	return os.Open(path)
}

func (l *LocalStore) Delete(ctx context.Context, key string) error {
	path := filepath.Join(l.baseDir, key)
	return os.Remove(path)
}

func (l *LocalStore) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	searchDir := filepath.Join(l.baseDir, prefix)

	err := filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
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
