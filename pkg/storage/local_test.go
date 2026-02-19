package storage

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestLocalStore_PutGetDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	key := "test/data.txt"
	content := "hello world"

	// Put
	if err := store.Put(ctx, key, strings.NewReader(content)); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Get
	rc, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != content {
		t.Errorf("expected %q, got %q", content, string(data))
	}

	// List
	keys, err := store.List(ctx, "test")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(keys) != 1 || keys[0] != "test/data.txt" {
		t.Errorf("expected [test/data.txt], got %v", keys)
	}

	// Delete
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Get after delete should fail
	_, err = store.Get(ctx, key)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestLocalStore_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	traversalKeys := []string{
		"../etc/passwd",
		"foo/../../etc/shadow",
		"../../../tmp/evil",
	}

	for _, key := range traversalKeys {
		t.Run("Put_"+key, func(t *testing.T) {
			err := store.Put(ctx, key, strings.NewReader("malicious"))
			if err == nil {
				t.Errorf("Put(%q) should fail with path traversal error", key)
			}
		})

		t.Run("Get_"+key, func(t *testing.T) {
			_, err := store.Get(ctx, key)
			if err == nil {
				t.Errorf("Get(%q) should fail with path traversal error", key)
			}
		})

		t.Run("Delete_"+key, func(t *testing.T) {
			err := store.Delete(ctx, key)
			if err == nil {
				t.Errorf("Delete(%q) should fail with path traversal error", key)
			}
		})

		t.Run("List_"+key, func(t *testing.T) {
			_, err := store.List(ctx, key)
			if err == nil {
				t.Errorf("List(%q) should fail with path traversal error", key)
			}
		})
	}
}

func TestLocalStore_Exists(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	key := "test/exists.txt"

	// Should not exist yet
	exists, err := store.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("expected key to not exist before Put")
	}

	// Put the file
	if err := store.Put(ctx, key, strings.NewReader("data")); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Should exist now
	exists, err = store.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Error("expected key to exist after Put")
	}

	// Delete and check again
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	exists, err = store.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("expected key to not exist after Delete")
	}
}

func TestLocalStore_Exists_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	_, err = store.Exists(ctx, "../etc/passwd")
	if err == nil {
		t.Error("Exists with path traversal key should return error")
	}
}

func TestLocalStore_ValidKeys(t *testing.T) {
	dir := t.TempDir()
	store, err := NewLocalStore(dir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	ctx := context.Background()
	validKeys := []string{
		"simple.txt",
		"nested/path/file.jsonl",
		"raw/request_facts/2025-01-15/10/batch_001.jsonl",
	}

	for _, key := range validKeys {
		t.Run("Put_"+key, func(t *testing.T) {
			err := store.Put(ctx, key, strings.NewReader("data"))
			if err != nil {
				t.Errorf("Put(%q) should succeed: %v", key, err)
			}
		})
	}
}
