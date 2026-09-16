// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

package dogfood

import (
	"os"
	"path/filepath"

	"github.com/lgreene/gravix-dashboards/schemas"
)

// validateAgainstSchemas runs a probe fact through the core validator, the
// same one POST /api/v1/facts uses.
//
// This is the load-bearing check in this package. A prober whose facts are
// rejected records nothing, and a month with no data points scores as 100%
// uptime — so a schema mistake here would not show up as an error, it would
// show up as a perfect SLA report during an outage.
func validateAgainstSchemas(body []byte) error {
	fact, err := schemas.ParseRequestFact(body)
	if err != nil {
		return err
	}
	return schemas.ValidateRequestFact(fact)
}

// readRepoFile reads a path relative to the repository root.
func readRepoFile(rel string) (string, error) {
	// ee/cloud/dogfood -> repository root
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
