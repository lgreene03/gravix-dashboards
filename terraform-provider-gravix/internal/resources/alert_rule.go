package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

// ResourceAlertRule manages Gravix alert rules.
func ResourceAlertRule() *schema.Resource {
	return &schema.Resource{
		CreateContext: alertRuleCreate,
		ReadContext:   alertRuleRead,
		UpdateContext: alertRuleUpdate,
		DeleteContext: alertRuleDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},
		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
			},
			"metric": {
				Type:         schema.TypeString,
				Required:     true,
				ValidateFunc: validation.StringInSlice([]string{"error_rate", "p50_latency", "p95_latency", "p99_latency", "throughput"}, false),
			},
			"operator": {
				Type:         schema.TypeString,
				Required:     true,
				ValidateFunc: validation.StringInSlice([]string{"gt", "lt"}, false),
			},
			"threshold": {
				Type:     schema.TypeFloat,
				Required: true,
			},
			"window_minutes": {
				Type:         schema.TypeInt,
				Required:     true,
				ValidateFunc: validation.IntBetween(1, 1440),
			},
			"channel_id": {
				Type:     schema.TypeString,
				Required: true,
			},
			"cooldown_minutes": {
				Type:         schema.TypeInt,
				Optional:     true,
				Default:      60,
				ValidateFunc: validation.IntBetween(1, 10080),
			},
			"service": {
				Type:     schema.TypeString,
				Optional: true,
				Default:  "",
			},
			"path_template": {
				Type:     schema.TypeString,
				Optional: true,
				Default:  "",
			},
			"status": {
				Type:         schema.TypeString,
				Optional:     true,
				Computed:     true,
				ValidateFunc: validation.StringInSlice([]string{"active", "paused"}, false),
			},
		},
	}
}

type alertRulePayload struct {
	Name            string  `json:"name"`
	Metric          string  `json:"metric"`
	Operator        string  `json:"operator"`
	Threshold       float64 `json:"threshold"`
	WindowMinutes   int     `json:"window_minutes"`
	Service         string  `json:"service"`
	PathTemplate    string  `json:"path_template"`
	ChannelID       string  `json:"channel_id"`
	CooldownMinutes int     `json:"cooldown_minutes"`
}

type alertRuleResponse struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Metric          string  `json:"metric"`
	Operator        string  `json:"operator"`
	Threshold       float64 `json:"threshold"`
	WindowMinutes   int     `json:"window_minutes"`
	Service         string  `json:"service"`
	PathTemplate    string  `json:"path_template"`
	ChannelID       string  `json:"channel_id"`
	CooldownMinutes int     `json:"cooldown_minutes"`
	Status          string  `json:"status"`
}

func alertRuleCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := newClientFromMeta(meta)
	body := alertRulePayload{
		Name:            d.Get("name").(string),
		Metric:          d.Get("metric").(string),
		Operator:        d.Get("operator").(string),
		Threshold:       d.Get("threshold").(float64),
		WindowMinutes:   d.Get("window_minutes").(int),
		Service:         d.Get("service").(string),
		PathTemplate:    d.Get("path_template").(string),
		ChannelID:       d.Get("channel_id").(string),
		CooldownMinutes: d.Get("cooldown_minutes").(int),
	}
	data, status, err := c.do(ctx, http.MethodPost, "/api/gateway/alert-rules", body)
	if err != nil {
		return diag.FromErr(err)
	}
	if status != http.StatusCreated {
		return diag.Errorf("create alert rule failed (HTTP %d): %s", status, data)
	}
	var resp alertRuleResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return diag.FromErr(fmt.Errorf("parse response: %w", err))
	}
	d.SetId(resp.ID)
	setAlertRuleFields(d, &resp)
	return nil
}

func alertRuleRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := newClientFromMeta(meta)
	data, status, err := c.do(ctx, http.MethodGet, "/api/gateway/alert-rules", nil)
	if err != nil {
		return diag.FromErr(err)
	}
	if status == http.StatusNotFound {
		d.SetId("")
		return nil
	}
	if status != http.StatusOK {
		return diag.Errorf("list alert rules failed (HTTP %d): %s", status, data)
	}
	var body struct {
		Rules []alertRuleResponse `json:"rules"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return diag.FromErr(err)
	}
	for _, r := range body.Rules {
		if r.ID == d.Id() {
			setAlertRuleFields(d, &r)
			return nil
		}
	}
	d.SetId("")
	return nil
}

func alertRuleUpdate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := newClientFromMeta(meta)
	body := alertRulePayload{
		Name:            d.Get("name").(string),
		Metric:          d.Get("metric").(string),
		Operator:        d.Get("operator").(string),
		Threshold:       d.Get("threshold").(float64),
		WindowMinutes:   d.Get("window_minutes").(int),
		Service:         d.Get("service").(string),
		PathTemplate:    d.Get("path_template").(string),
		ChannelID:       d.Get("channel_id").(string),
		CooldownMinutes: d.Get("cooldown_minutes").(int),
	}
	data, status, err := c.do(ctx, http.MethodPut, "/api/gateway/alert-rules/"+d.Id(), body)
	if err != nil {
		return diag.FromErr(err)
	}
	if status == http.StatusNotFound {
		d.SetId("")
		return nil
	}
	if status != http.StatusOK {
		return diag.Errorf("update alert rule failed (HTTP %d): %s", status, data)
	}
	var resp alertRuleResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return diag.FromErr(fmt.Errorf("parse response: %w", err))
	}
	setAlertRuleFields(d, &resp)
	return nil
}

func alertRuleDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := newClientFromMeta(meta)
	_, status, err := c.do(ctx, http.MethodDelete, "/api/gateway/alert-rules/"+d.Id(), nil)
	if err != nil {
		return diag.FromErr(err)
	}
	if status != http.StatusNoContent && status != http.StatusNotFound {
		return diag.Errorf("delete alert rule failed (HTTP %d)", status)
	}
	return nil
}

func setAlertRuleFields(d *schema.ResourceData, r *alertRuleResponse) {
	d.Set("name", r.Name)
	d.Set("metric", r.Metric)
	d.Set("operator", r.Operator)
	d.Set("threshold", r.Threshold)
	d.Set("window_minutes", r.WindowMinutes)
	d.Set("service", r.Service)
	d.Set("path_template", r.PathTemplate)
	d.Set("channel_id", r.ChannelID)
	d.Set("cooldown_minutes", r.CooldownMinutes)
	d.Set("status", r.Status)
}
