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
// pkg/extpoint before Run mounts extensions. GRVX-1304 through GRVX-1311 each
// add exactly one import line; ee/fleet (GRVX-1307) is the first.
package main

import (
	// ee/ feature imports go here, one per shipped capability. Each is a blank
	// import: the package's init() registers an Extension with pkg/extpoint, and
	// gatewaycore.Run mounts whatever is registered. Nothing else changes.
	_ "github.com/lgreene/gravix-dashboards/ee/fleet"        // GRVX-1307, mounted at /ee/fleet/
	_ "github.com/lgreene/gravix-dashboards/ee/intelligence" // GRVX-1309, mounted at /ee/intelligence/
	_ "github.com/lgreene/gravix-dashboards/ee/warehouse"    // GRVX-1311, mounted at /ee/warehouse/

	"github.com/lgreene/gravix-dashboards/pkg/gatewaycore"
)

func main() {
	gatewaycore.Run()
}
