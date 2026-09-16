// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Command verify-provenance checks that a Gravix container image was built by
// the project's own public workflow from a commit anybody can read.
//
// Gravix Cloud runs it against its own images. Nothing about it is privileged:
// the attestation and the commit history are both public, so a customer can run
// `gh attestation verify oci://<image> --owner lgreene` and reach the same
// answer by hand. What this adds is the second half — checking that the attested
// commit is actually an ancestor of the public branch, which is the difference
// between "somebody's workflow built this" and "this came from a tree you can
// read".
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/lgreene/gravix-dashboards/ee/cloud/provenance"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// Exit codes: 0 verified, 1 verification failed, 2 invalid flags.
//
// 1 and 2 are deliberately different. A script that cannot tell "this image is
// not what it claims" from "you typed the flag wrong" will eventually treat the
// first as the second.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify-provenance", flag.ContinueOnError)
	fs.SetOutput(stderr)

	image := fs.String("image", "",
		"Fully qualified image reference to verify, e.g. ghcr.io/lgreene/gravix-dashboards-ingestion:sha-abc1234 (required)")
	owner := fs.String("owner", "lgreene", "GitHub account or organisation that must own the attestation")
	repo := fs.String("repo", "lgreene/gravix-dashboards", "Repository the attested commit must belong to")
	branch := fs.String("branch", "main", "Branch the attested commit must be an ancestor of")
	repoDir := fs.String("repo-dir", ".", "Path to a local clone of --repo used to check commit ancestry")
	ghBinary := fs.String("gh-binary", "gh", "Path to the gh CLI executable")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *image == "" {
		fmt.Fprintln(stderr, "usage: verify-provenance --image <ref> [flags]")
		return 2
	}

	res, err := provenance.VerifyImage(context.Background(),
		*ghBinary, *image, *owner, *repo, *branch, *repoDir)
	if err != nil || !res.Verified {
		reason := res.Reason
		if reason == "" && err != nil {
			reason = err.Error()
		}
		fmt.Fprintln(stderr, reason)
		return 1
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
