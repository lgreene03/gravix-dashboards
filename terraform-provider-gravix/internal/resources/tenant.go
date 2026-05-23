package resources

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// ResourceTenant surfaces the current authenticated tenant as a managed resource.
// Since the tenant is pre-existing (you authenticate with a JWT), create simply
// reads the current tenant and imports it into state. Delete removes it from state
// only — it does not destroy the tenant account.
func ResourceTenant() *schema.Resource {
	return &schema.Resource{
		CreateContext: tenantCreate,
		ReadContext:   tenantRead,
		DeleteContext: tenantDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},
		Schema: map[string]*schema.Schema{
			"tenant_id": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The unique identifier of this tenant.",
			},
			"name": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The tenant's display name.",
			},
			"plan": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The tenant's current billing plan (free, team, business, scale, enterprise).",
			},
			"email": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The admin user's email address for this tenant.",
			},
		},
	}
}

func tenantCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	// The tenant is pre-existing; read current state and import into Terraform.
	return tenantRead(ctx, d, meta)
}

func tenantRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := newClientFromMeta(meta)
	data, status, err := c.do(ctx, http.MethodGet, "/api/gateway/me", nil)
	if err != nil {
		return diag.FromErr(err)
	}
	if status == http.StatusNotFound || status == http.StatusUnauthorized {
		d.SetId("")
		return nil
	}
	if status != http.StatusOK {
		return diag.Errorf("read tenant failed (HTTP %d): %s", status, data)
	}
	var resp struct {
		TenantID   string `json:"tenant_id"`
		TenantName string `json:"tenant_name"`
		Plan       string `json:"plan"`
		Email      string `json:"email"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(resp.TenantID)
	d.Set("tenant_id", resp.TenantID)
	d.Set("name", resp.TenantName)
	d.Set("plan", resp.Plan)
	d.Set("email", resp.Email)
	return nil
}

func tenantDelete(_ context.Context, d *schema.ResourceData, _ any) diag.Diagnostics {
	// Removing from Terraform state only; does not delete the tenant account.
	d.SetId("")
	return nil
}

// tenantMe calls GET /api/gateway/me and returns the raw response bytes.
// Used by other resources that need the authenticated tenant's ID.
func tenantMe(ctx context.Context, c *Client) (string, error) {
	data, status, err := c.do(ctx, http.MethodGet, "/api/gateway/me", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", nil
	}
	var resp struct {
		TenantID string `json:"tenant_id"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", err
	}
	return resp.TenantID, nil
}
