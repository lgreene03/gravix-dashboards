// Package provider implements the Gravix Terraform provider.
// It exposes gravix_api_key, gravix_alert_rule, and gravix_tenant resources
// so engineering teams can manage Gravix configuration as infrastructure-as-code.
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/lgreene/terraform-provider-gravix/internal/resources"
)

// New returns the provider factory function.
func New() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"api_url": {
				Type:        schema.TypeString,
				Required:    true,
				DefaultFunc: schema.EnvDefaultFunc("GRAVIX_API_URL", "https://app.gravix.io"),
				Description: "Gravix gateway API base URL.",
			},
			"jwt_token": {
				Type:        schema.TypeString,
				Required:    true,
				Sensitive:   true,
				DefaultFunc: schema.EnvDefaultFunc("GRAVIX_JWT_TOKEN", nil),
				Description: "JWT bearer token for an admin user. Set via GRAVIX_JWT_TOKEN.",
			},
		},
		ResourcesMap: map[string]*schema.Resource{
			"gravix_api_key":   resources.ResourceAPIKey(),
			"gravix_alert_rule": resources.ResourceAlertRule(),
			"gravix_tenant":    resources.ResourceTenant(),
		},
		ConfigureContextFunc: configure,
	}
}

func configure(_ context.Context, d *schema.ResourceData) (any, diag.Diagnostics) {
	return map[string]string{
		"api_url":   d.Get("api_url").(string),
		"jwt_token": d.Get("jwt_token").(string),
	}, nil
}
