package storage

import (
	"context"
	"io"
)

// ObjectStore defines the interface for interacting with object storage (Local, S3, MinIO, etc.)
type ObjectStore interface {
	Put(ctx context.Context, key string, reader io.Reader) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}
