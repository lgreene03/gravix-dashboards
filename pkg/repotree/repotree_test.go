// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package repotree

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// layout builds a checkout with one nested worktree (.git as a file), one nested
// clone (.git as a directory) and an ordinary subdirectory, each holding a Go file.
func layout(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		".git/HEAD":                          "ref: refs/heads/main\n",
		"pkg/a/a.go":                         "package a\n",
		".claude/worktrees/other/.git":       "gitdir: /elsewhere/.git/worktrees/other\n",
		".claude/worktrees/other/pkg/a/a.go": "package a\n",
		"vendor/clone/.git/HEAD":             "ref: refs/heads/main\n",
		"vendor/clone/b.go":                  "package b\n",
		".claude/agents/note.md":             "not a checkout\n",
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRootIsNeverNested(t *testing.T) {
	root := layout(t)
	if IsNestedCheckout(root, root) {
		t.Error("the root has a .git of its own and must not be skipped")
	}
	if IsNestedCheckout(root, root+string(filepath.Separator)) {
		t.Error("the root with a trailing separator must not be skipped either")
	}
}

func TestWorktreeAndCloneAreNested(t *testing.T) {
	root := layout(t)
	for _, rel := range []string{".claude/worktrees/other", "vendor/clone"} {
		if !IsNestedCheckout(root, filepath.Join(root, rel)) {
			t.Errorf("%s holds its own .git and must be reported as nested", rel)
		}
	}
}

func TestOrdinaryDirectoriesAreNotNested(t *testing.T) {
	root := layout(t)
	for _, rel := range []string{"pkg", "pkg/a", ".claude", ".claude/worktrees", ".claude/agents", "vendor"} {
		if IsNestedCheckout(root, filepath.Join(root, rel)) {
			t.Errorf("%s has no .git of its own and must not be skipped", rel)
		}
	}
}

// The use it exists for: a walk that skips nested checkouts sees only this
// checkout's files.
func TestWalkSeesOnlyThisCheckout(t *testing.T) {
	root := layout(t)
	var got []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || IsNestedCheckout(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".go" {
			rel, _ := filepath.Rel(root, path)
			got = append(got, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if len(got) != 1 || got[0] != "pkg/a/a.go" {
		t.Errorf("walk found %v, want only pkg/a/a.go", got)
	}
}
