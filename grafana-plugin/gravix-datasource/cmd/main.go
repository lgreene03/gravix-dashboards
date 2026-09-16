// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Command gpx_gravix_datasource is the Grafana backend plugin binary.
//
// Grafana launches it as a subprocess and speaks to it over gRPC;
// datasource.Manage wires that up and handles instance lifecycle, so this file
// stays as short as it looks.
package main

import (
	"os"

	"github.com/grafana/grafana-plugin-sdk-go/backend/datasource"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"

	plugin "github.com/lgreene/gravix-datasource/pkg"
)

func main() {
	if err := datasource.Manage(plugin.PluginID, plugin.NewDatasource, datasource.ManageOpts{}); err != nil {
		log.DefaultLogger.Error("gravix datasource exited", "error", err)
		os.Exit(1)
	}
}
