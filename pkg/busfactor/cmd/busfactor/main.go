// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command busfactor audits how many people can actually review each subsystem.
//
// It reports declared and effective separately, because a declared owner who
// has not worked in a subsystem in six months is a name in a file rather than
// a bus factor of one more.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lgreene/gravix-dashboards/pkg/busfactor"
)

func main() {
	var (
		repo     = flag.String("repo", ".", "path to the repository")
		asJSON   = flag.Bool("json", false, "emit JSON instead of the text report")
		register = flag.String("register", "", "path to the subsystem register (default <repo>/"+busfactor.RegisterPath+")")
	)
	flag.Parse()

	root, err := busfactor.RepoRoot(*repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus_factor: %v\n", err)
		os.Exit(2)
	}
	if *register == "" {
		*register = filepath.Join(root, filepath.FromSlash(busfactor.RegisterPath))
	}

	subsystems, err := busfactor.LoadRegister(*register)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus_factor: %v\n", err)
		os.Exit(2)
	}

	maintainers, err := os.ReadFile(filepath.Join(root, "MAINTAINERS.md"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus_factor: reading MAINTAINERS.md: %v\n", err)
		os.Exit(2)
	}

	rep, err := busfactor.Audit(context.Background(), root, subsystems, string(maintainers))
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus_factor: %v\n", err)
		os.Exit(2)
	}

	if *asJSON {
		raw, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "bus_factor: %v\n", err)
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
