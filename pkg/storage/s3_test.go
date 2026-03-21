package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// ─── Mock S3 Client ───

type mockS3Client struct {
	putObjectFn    func(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	getObjectFn    func(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	deleteObjectFn func(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	headObjectFn   func(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	listObjectsFn  func(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

func (m *mockS3Client) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if m.putObjectFn != nil {
		return m.putObjectFn(ctx, params, optFns...)
	}
	return &s3.PutObjectOutput{}, nil
}

func (m *mockS3Client) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.getObjectFn != nil {
		return m.getObjectFn(ctx, params, optFns...)
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(nil))}, nil
}

func (m *mockS3Client) DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	if m.deleteObjectFn != nil {
		return m.deleteObjectFn(ctx, params, optFns...)
	}
	return &s3.DeleteObjectOutput{}, nil
}

func (m *mockS3Client) HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	if m.headObjectFn != nil {
		return m.headObjectFn(ctx, params, optFns...)
	}
	return &s3.HeadObjectOutput{}, nil
}

func (m *mockS3Client) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if m.listObjectsFn != nil {
		return m.listObjectsFn(ctx, params, optFns...)
	}
	return &s3.ListObjectsV2Output{}, nil
}

// ─── mockAPIError implements smithy.APIError ───

type mockAPIError struct {
	code    string
	message string
}

func (e *mockAPIError) Error() string   { return e.message }
func (e *mockAPIError) ErrorCode() string   { return e.code }
func (e *mockAPIError) ErrorMessage() string { return e.message }
func (e *mockAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultServer }

// ─── Put Tests ───

func TestS3Store_Put(t *testing.T) {
	var captured *s3.PutObjectInput
	mock := &mockS3Client{
		putObjectFn: func(ctx context.Context, params *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			captured = params
			return &s3.PutObjectOutput{}, nil
		},
	}

	store := NewS3StoreWithClient(mock, "test-bucket")
	err := store.Put(context.Background(), "data/file.jsonl", bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	if captured == nil {
		t.Fatal("PutObject was not called")
	}
	if aws.ToString(captured.Bucket) != "test-bucket" {
		t.Errorf("bucket = %q, want test-bucket", aws.ToString(captured.Bucket))
	}
	if aws.ToString(captured.Key) != "data/file.jsonl" {
		t.Errorf("key = %q, want data/file.jsonl", aws.ToString(captured.Key))
	}

	body, _ := io.ReadAll(captured.Body)
	if string(body) != "hello" {
		t.Errorf("body = %q, want hello", string(body))
	}
}

func TestS3Store_Put_Error(t *testing.T) {
	mock := &mockS3Client{
		putObjectFn: func(ctx context.Context, params *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			return nil, fmt.Errorf("network error")
		},
	}

	store := NewS3StoreWithClient(mock, "test-bucket")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately to avoid retry delays

	err := store.Put(ctx, "key", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Fatal("expected error from Put")
	}
}

// ─── Get Tests ───

func TestS3Store_Get(t *testing.T) {
	mock := &mockS3Client{
		getObjectFn: func(ctx context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			if aws.ToString(params.Bucket) != "test-bucket" {
				t.Errorf("bucket = %q, want test-bucket", aws.ToString(params.Bucket))
			}
			if aws.ToString(params.Key) != "my/key.txt" {
				t.Errorf("key = %q, want my/key.txt", aws.ToString(params.Key))
			}
			return &s3.GetObjectOutput{
				Body: io.NopCloser(bytes.NewReader([]byte("file content"))),
			}, nil
		},
	}

	store := NewS3StoreWithClient(mock, "test-bucket")
	rc, err := store.Get(context.Background(), "my/key.txt")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer rc.Close()

	data, _ := io.ReadAll(rc)
	if string(data) != "file content" {
		t.Errorf("content = %q, want %q", string(data), "file content")
	}
}

func TestS3Store_Get_Error(t *testing.T) {
	mock := &mockS3Client{
		getObjectFn: func(ctx context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return nil, fmt.Errorf("access denied")
		},
	}

	store := NewS3StoreWithClient(mock, "test-bucket")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.Get(ctx, "key")
	if err == nil {
		t.Fatal("expected error from Get")
	}
}

// ─── Delete Tests ───

func TestS3Store_Delete(t *testing.T) {
	var captured *s3.DeleteObjectInput
	mock := &mockS3Client{
		deleteObjectFn: func(ctx context.Context, params *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
			captured = params
			return &s3.DeleteObjectOutput{}, nil
		},
	}

	store := NewS3StoreWithClient(mock, "test-bucket")
	err := store.Delete(context.Background(), "old/file.txt")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if captured == nil {
		t.Fatal("DeleteObject was not called")
	}
	if aws.ToString(captured.Bucket) != "test-bucket" {
		t.Errorf("bucket = %q, want test-bucket", aws.ToString(captured.Bucket))
	}
	if aws.ToString(captured.Key) != "old/file.txt" {
		t.Errorf("key = %q, want old/file.txt", aws.ToString(captured.Key))
	}
}

// ─── Exists Tests ───

func TestS3Store_Exists_Found(t *testing.T) {
	mock := &mockS3Client{
		headObjectFn: func(ctx context.Context, params *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			return &s3.HeadObjectOutput{}, nil
		},
	}

	store := NewS3StoreWithClient(mock, "test-bucket")
	exists, err := store.Exists(context.Background(), "found.txt")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Error("expected exists=true")
	}
}

func TestS3Store_Exists_NotFound(t *testing.T) {
	mock := &mockS3Client{
		headObjectFn: func(ctx context.Context, params *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			return nil, &types.NotFound{Message: aws.String("not found")}
		},
	}

	store := NewS3StoreWithClient(mock, "test-bucket")
	exists, err := store.Exists(context.Background(), "missing.txt")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("expected exists=false for NotFound")
	}
}

func TestS3Store_Exists_NotFoundAPIError(t *testing.T) {
	mock := &mockS3Client{
		headObjectFn: func(ctx context.Context, params *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			return nil, &mockAPIError{code: "NotFound", message: "not found"}
		},
	}

	store := NewS3StoreWithClient(mock, "test-bucket")
	exists, err := store.Exists(context.Background(), "missing.txt")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("expected exists=false for NotFound API error")
	}
}

func TestS3Store_Exists_NoSuchKeyAPIError(t *testing.T) {
	mock := &mockS3Client{
		headObjectFn: func(ctx context.Context, params *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			return nil, &mockAPIError{code: "NoSuchKey", message: "no such key"}
		},
	}

	store := NewS3StoreWithClient(mock, "test-bucket")
	exists, err := store.Exists(context.Background(), "missing.txt")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("expected exists=false for NoSuchKey API error")
	}
}

func TestS3Store_Exists_OtherError(t *testing.T) {
	mock := &mockS3Client{
		headObjectFn: func(ctx context.Context, params *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	store := NewS3StoreWithClient(mock, "test-bucket")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.Exists(ctx, "key")
	if err == nil {
		t.Fatal("expected error from Exists with connection error")
	}
}

// ─── List Tests ───

func TestS3Store_List(t *testing.T) {
	callCount := 0
	mock := &mockS3Client{
		listObjectsFn: func(ctx context.Context, params *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			callCount++
			if aws.ToString(params.Bucket) != "test-bucket" {
				t.Errorf("bucket = %q, want test-bucket", aws.ToString(params.Bucket))
			}
			if aws.ToString(params.Prefix) != "raw/" {
				t.Errorf("prefix = %q, want raw/", aws.ToString(params.Prefix))
			}
			return &s3.ListObjectsV2Output{
				Contents: []types.Object{
					{Key: aws.String("raw/file1.jsonl")},
					{Key: aws.String("raw/file2.jsonl")},
				},
			}, nil
		},
	}

	store := NewS3StoreWithClient(mock, "test-bucket")
	keys, err := store.List(context.Background(), "raw/")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if keys[0] != "raw/file1.jsonl" {
		t.Errorf("keys[0] = %q, want raw/file1.jsonl", keys[0])
	}
	if keys[1] != "raw/file2.jsonl" {
		t.Errorf("keys[1] = %q, want raw/file2.jsonl", keys[1])
	}
}

func TestS3Store_List_Empty(t *testing.T) {
	mock := &mockS3Client{
		listObjectsFn: func(ctx context.Context, params *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return &s3.ListObjectsV2Output{Contents: nil}, nil
		},
	}

	store := NewS3StoreWithClient(mock, "test-bucket")
	keys, err := store.List(context.Background(), "empty/")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

func TestS3Store_List_Error(t *testing.T) {
	mock := &mockS3Client{
		listObjectsFn: func(ctx context.Context, params *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
			return nil, fmt.Errorf("access denied")
		},
	}

	store := NewS3StoreWithClient(mock, "test-bucket")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.List(ctx, "prefix/")
	if err == nil {
		t.Fatal("expected error from List")
	}
}

// ─── NewS3StoreWithClient Tests ───

func TestNewS3StoreWithClient(t *testing.T) {
	mock := &mockS3Client{}
	store := NewS3StoreWithClient(mock, "my-bucket")
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if store.bucket != "my-bucket" {
		t.Errorf("bucket = %q, want my-bucket", store.bucket)
	}
}
