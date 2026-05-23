package main

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
	"github.com/lgreene/terraform-provider-gravix/internal/provider"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		ProviderFunc: provider.New,
	})
}
