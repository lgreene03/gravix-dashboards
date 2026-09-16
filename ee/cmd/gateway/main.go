// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: BUSL-1.1
//
// This file is part of Gravix Enterprise Edition and is NOT open source.
// Licensed under the Business Source License 1.1. See ee/LICENSE.
// Change Date: two years from this version's publication. Change License: Apache-2.0.

// Command gateway is the Gravix Enterprise entrypoint. It calls the identical
// gatewaycore.Run used by the OSS binary. The only difference between this binary
// and services/gateway's is the import block below: each ee/ feature package this
// binary should include is blank-imported here so its init() registers with
// pkg/extpoint before Run mounts extensions. Today this list is empty — no ee/
// feature exists yet. GRVX-1304 through GRVX-1311 each add exactly one import line.
package main

import (
	// ee/ feature imports go here, one per shipped capability. Empty today.

	"github.com/lgreene/gravix-dashboards/pkg/gatewaycore"
)

func main() {
	gatewaycore.Run()
}
