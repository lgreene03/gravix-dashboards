package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// ResourceAPIKey manages Gravix ingestion API keys.
//
// Example usage:
//
//	resource "gravix_api_key" "production" {
//	  name = "production-ingest"
//	}
func ResourceAPIKey() *schema.Resource {
	return &schema.Resource{
		CreateContext: apiKeyCreate,
		ReadContext:   apiKeyRead,
		DeleteContext: apiKeyDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},
		Schema: map[string]*schema.Schema{
			"name": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "Human-readable label for this API key.",
			},
			"key_prefix": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "First 8 characters of the key for identification (e.g. grvx_abc1).",
			},
			"plain_key": {
				Type:        schema.TypeString,
				Computed:    true,
				Sensitive:   true,
				Description: "The plaintext API key. Only available immediately after creation — store it in your secrets manager.",
			},
			"status": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Key status: active or revoked.",
			},
		},
	}
}

func apiKeyCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := newClientFromMeta(meta)
	body := map[string]string{"name": d.Get("name").(string)}
	data, status, err := c.do(ctx, http.MethodPost, "/api/gateway/api-keys", body)
	if err != nil {
		return diag.FromErr(err)
	}
	if status != http.StatusCreated {
		return diag.Errorf("create API key failed (HTTP %d): %s", status, data)
	}
	var resp struct {
		ID       string `json:"id"`
		PlainKey string `json:"plain_key"`
		Prefix   string `json:"key_prefix"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return diag.FromErr(fmt.Errorf("parse response: %w", err))
	}
	d.SetId(resp.ID)
	d.Set("plain_key", resp.PlainKey)
	d.Set("key_prefix", resp.Prefix)
	d.Set("status", resp.Status)
	return nil
}

func apiKeyRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := newClientFromMeta(meta)
	data, status, err := c.do(ctx, http.MethodGet, "/api/gateway/api-keys", nil)
	if err != nil {
		return diag.FromErr(err)
	}
	if status == http.StatusNotFound {
		d.SetId("")
		return nil
	}
	if status != http.StatusOK {
		return diag.Errorf("read API keys failed (HTTP %d): %s", status, data)
	}
	var keys []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Prefix string `json:"key_prefix"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &keys); err != nil {
		return diag.FromErr(err)
	}
	for _, k := range keys {
		if k.ID == d.Id() {
			d.Set("name", k.Name)
			d.Set("key_prefix", k.Prefix)
			d.Set("status", k.Status)
			return nil
		}
	}
	d.SetId("") // key no longer exists
	return nil
}

func apiKeyDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := newClientFromMeta(meta)
	_, status, err := c.do(ctx, http.MethodDelete, "/api/gateway/api-keys/"+d.Id(), nil)
	if err != nil {
		return diag.FromErr(err)
	}
	if status != http.StatusNoContent && status != http.StatusNotFound {
		return diag.Errorf("delete API key failed (HTTP %d)", status)
	}
	return nil
}

func newClientFromMeta(meta any) *Client {
	return clientFromMeta(meta)
}
