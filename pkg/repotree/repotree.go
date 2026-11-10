// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package repotree answers one question for code that walks this repository's
// files: does a directory belong to this checkout, or to a different checkout
// nested inside it?
//
// A git worktree is commonly created inside the repository it belongs to, for
// example under .claude/worktrees/. Its files are another branch's files. A walk
// that descends into one reports that branch's code as this checkout's: in
// November 2026 a full test run in a checkout hosting five worktrees failed
// eight tests, every one of them on a file from a worktree (a testdata fixture,
// a development key, a Parquet writer on an older branch), and none on a file in
// the checkout under test.
package repotree

import (
	"os"
	"path/filepath"
)

// IsNestedCheckout reports whether dir, a directory at or below root, is the top
// of a different git checkout: a worktree, whose .git is a file, or a clone,
// whose .git is a directory. root itself never is, so a walk can call this on
// every directory it visits, including the first.
//
// A walker that gets true should return filepath.SkipDir.
func IsNestedCheckout(root, dir string) bool {
	if filepath.Clean(dir) == filepath.Clean(root) {
		return false
	}
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}
