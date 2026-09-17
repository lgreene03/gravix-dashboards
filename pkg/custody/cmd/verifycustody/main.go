// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command verifycustody checks that every piece of Gravix's identity has at
// least two people who could recover it, and that somebody has demonstrated
// as much in the last twelve months.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/custody"
)

func main() {
	var (
		repo     = flag.String("repo", ".", "path to the repository")
		asJSON   = flag.Bool("json", false, "emit JSON instead of the text report")
		register = flag.String("register", "", "path to the succession register (default <repo>/"+custody.RegisterPath+")")
	)
	flag.Parse()

	root, err := repoRoot(*repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify_custody: %v\n", err)
		os.Exit(2)
	}
	if *register == "" {
		*register = filepath.Join(root, filepath.FromSlash(custody.RegisterPath))
	}

	assets, err := custody.LoadRegister(*register)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify_custody: %v\n", err)
		os.Exit(2)
	}

	rep := custody.Audit(assets, time.Now().UTC())

	// The scan is not advisory. A register that leaked a credential is worse
	// than no register, and it is worse the moment it is pushed rather than
	// when somebody eventually reads it.
	for _, rel := range []string{custody.RegisterPath, custody.DrillPath} {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "verify_custody: reading %s: %v\n", rel, err)
			os.Exit(2)
		}
		rep.Findings = append(rep.Findings, custody.ScanSecrets(rel, string(body))...)
		rep.Findings = append(rep.Findings, custody.ScanPersonalDetails(rel, string(body))...)
	}

	if *asJSON {
		raw, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "verify_custody: %v\n", err)
			os.Exit(2)
		}
		fmt.Println(string(raw))
	} else {
		rep.Render(os.Stdout)
	}

	if !rep.OK() {
		os.Exit(1)
	}
}

// repoRoot walks up from dir to the directory holding go.mod.
func repoRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no go.mod above %s", dir)
		}
		abs = parent
	}
}
