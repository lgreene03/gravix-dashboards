package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const (
	maxRetries     = 3
	baseRetryDelay = 500 * time.Millisecond
)

// s3API abstracts the S3 client methods used by S3Store, enabling mock injection for tests.
type s3API interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// S3Store implements ObjectStore using AWS S3 (or MinIO).
type S3Store struct {
	client s3API
	bucket string
}

func NewS3Store(ctx context.Context, endpoint, region, bucket, accessKey, secretKey string) (*S3Store, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load SDK config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true // Required for MinIO
	})

	return &S3Store{
		client: client,
		bucket: bucket,
	}, nil
}

// NewS3StoreWithClient creates an S3Store with a provided client (for testing).
func NewS3StoreWithClient(client s3API, bucket string) *S3Store {
	return &S3Store{client: client, bucket: bucket}
}

// retryWithBackoff retries the given function up to maxRetries times with exponential backoff + jitter.
// It stops early if the context is cancelled.
func retryWithBackoff(ctx context.Context, operation string, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// Don't retry on context cancellation
		if ctx.Err() != nil {
			return lastErr
		}

		if attempt < maxRetries {
			delay := time.Duration(float64(baseRetryDelay) * math.Pow(2, float64(attempt)))
			// Add jitter: 0.5x to 1.5x the delay
			jitter := time.Duration(float64(delay) * (0.5 + rand.Float64()))
			slog.Warn("s3 operation failed, retrying",
			"operation", operation,
			"attempt", attempt+1,
			"max_retries", maxRetries+1,
			"backoff", jitter.String(),
			"error", lastErr,
		)

			select {
			case <-ctx.Done():
				return lastErr
			case <-time.After(jitter):
			}
		}
	}
	return fmt.Errorf("S3 %s failed after %d attempts: %w", operation, maxRetries+1, lastErr)
}

func (s *S3Store) Put(ctx context.Context, key string, reader io.Reader) error {
	// Buffer the reader so we can retry (reader may be consumed on first attempt)
	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("failed to read data for upload: %w", err)
	}

	return retryWithBackoff(ctx, "Put", func() error {
		_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
			Body:   bytes.NewReader(data),
		})
		return err
	})
}

// storageClassToS3 maps our StorageClass to S3 storage class types.
func storageClassToS3(class StorageClass) types.StorageClass {
	switch class {
	case StorageClassWarm:
		return types.StorageClassStandardIa
	case StorageClassCold:
		return types.StorageClassGlacier
	default:
		return types.StorageClassStandard
	}
}

func (s *S3Store) PutWithStorageClass(ctx context.Context, key string, reader io.Reader, class StorageClass) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("failed to read data for upload: %w", err)
	}

	sc := storageClassToS3(class)
	return retryWithBackoff(ctx, "PutWithStorageClass", func() error {
		_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:       aws.String(s.bucket),
			Key:          aws.String(key),
			Body:         bytes.NewReader(data),
			StorageClass: sc,
		})
		return err
	})
}

func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	var result io.ReadCloser
	err := retryWithBackoff(ctx, "Get", func() error {
		out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			return err
		}
		result = out.Body
		return nil
	})
	return result, err
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	return retryWithBackoff(ctx, "Delete", func() error {
		_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
		})
		return err
	})
}

func (s *S3Store) Exists(ctx context.Context, key string) (bool, error) {
	var exists bool
	err := retryWithBackoff(ctx, "Exists", func() error {
		_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			// Check if it's a "not found" error — don't retry these
			var nsk *types.NotFound
			if errors.As(err, &nsk) {
				exists = false
				return nil // Not an error, just doesn't exist
			}
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchKey") {
				exists = false
				return nil
			}
			return err
		}
		exists = true
		return nil
	})
	return exists, err
}

func (s *S3Store) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	err := retryWithBackoff(ctx, "List", func() error {
		keys = nil // Reset on retry
		paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
			Bucket: aws.String(s.bucket),
			Prefix: aws.String(prefix),
		})

		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return err
			}
			for _, obj := range page.Contents {
				keys = append(keys, *obj.Key)
			}
		}
		return nil
	})
	return keys, err
}
