package storage

import (
	"context"
	"io"
)

// StorageClass represents the storage tier for an object.
type StorageClass string

const (
	StorageClassStandard StorageClass = "standard"
	StorageClassWarm     StorageClass = "warm"
	StorageClassCold     StorageClass = "cold"
)

// ObjectStore defines the interface for interacting with object storage (Local, S3, MinIO, etc.)
type ObjectStore interface {
	Put(ctx context.Context, key string, reader io.Reader) error
	// PutWithStorageClass uploads an object with a specific storage class tier.
	// Local storage ignores the class. S3: warm→STANDARD_IA, cold→GLACIER.
	PutWithStorageClass(ctx context.Context, key string, reader io.Reader, class StorageClass) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
	Exists(ctx context.Context, key string) (bool, error)
}
