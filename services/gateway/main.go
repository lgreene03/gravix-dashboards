// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command gateway is the Gravix OSS gateway entrypoint. It imports only
// pkg/gatewaycore — never ee/ — so this binary is, by construction, the OSS
// gateway with zero paid capability, whether or not ee/ exists in the working tree.
package main

import "github.com/lgreene/gravix-dashboards/pkg/gatewaycore"

func main() {
	gatewaycore.Run()
}
