package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// DeployPayload represents a normalized deploy event.
type DeployPayload struct {
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Version     string `json:"version"`
	CommitSHA   string `json:"commit_sha"`
	CommitMsg   string `json:"commit_message"`
	DeployedBy  string `json:"deployed_by"`
	Status      string `json:"status"` // success, failure, in_progress
	Source      string `json:"source"` // github, gitlab, manual
}

// GitHubDeploymentPayload represents a GitHub deployment_status webhook.
type GitHubDeploymentPayload struct {
	Action     string `json:"action"`
	Deployment struct {
		SHA         string `json:"sha"`
		Ref         string `json:"ref"`
		Environment string `json:"environment"`
		Description string `json:"description"`
		Creator     struct {
			Login string `json:"login"`
		} `json:"creator"`
	} `json:"deployment"`
	DeploymentStatus struct {
		State       string `json:"state"`
		Description string `json:"description"`
	} `json:"deployment_status"`
	Repository struct {
		FullName string `json:"full_name"`
		Name     string `json:"name"`
	} `json:"repository"`
}

// handleDeployWebhook returns a handler for POST /api/v1/deploy that receives
// deployment notifications from GitHub, GitLab, or manual sources and converts
// them to ServiceEvents written to the durable sink.
func handleDeployWebhook(sink *DurableSink, tdb tenantdb.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "only POST is accepted")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeErrorJSON(w, http.StatusRequestEntityTooLarge, "request body too large (max 1MB)")
			return
		}
		defer r.Body.Close()

		tenantID := getTenantID(r)

		// Detect source from headers
		source := "manual"
		if r.Header.Get("X-GitHub-Event") != "" {
			source = "github"
		} else if r.Header.Get("X-Gitlab-Event") != "" {
			source = "gitlab"
		}

		var deploy DeployPayload

		switch source {
		case "github":
			event := r.Header.Get("X-GitHub-Event")
			if event != "deployment_status" && event != "deployment" {
				// Accept but ignore non-deployment events
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]string{"status": "ignored", "event": event})
				return
			}

			var gh GitHubDeploymentPayload
			if err := json.Unmarshal(body, &gh); err != nil {
				writeErrorJSON(w, http.StatusBadRequest, "invalid GitHub payload: "+err.Error())
				return
			}

			status := "success"
			if gh.DeploymentStatus.State != "" {
				status = gh.DeploymentStatus.State
			}

			deploy = DeployPayload{
				Service:     gh.Repository.Name,
				Environment: gh.Deployment.Environment,
				Version:     gh.Deployment.Ref,
				CommitSHA:   gh.Deployment.SHA,
				CommitMsg:   gh.Deployment.Description,
				DeployedBy:  gh.Deployment.Creator.Login,
				Status:      status,
				Source:      "github",
			}

		default:
			// Direct/manual payload
			if err := json.Unmarshal(body, &deploy); err != nil {
				writeErrorJSON(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
				return
			}
			if deploy.Source == "" {
				deploy.Source = source
			}
		}

		if deploy.Service == "" {
			writeErrorJSON(w, http.StatusBadRequest, "service name required")
			return
		}
		if deploy.Status == "" {
			deploy.Status = "success"
		}

		// Convert to ServiceEvent
		now := time.Now().UTC()
		event := map[string]interface{}{
			"event_id":    fmt.Sprintf("deploy-%s-%d", deploy.Service, now.UnixNano()),
			"event_time":  now.Format(time.RFC3339),
			"service":     deploy.Service,
			"event_type":  "deploy",
			"title":       fmt.Sprintf("Deployed %s %s", deploy.Service, deploy.Version),
			"description": fmt.Sprintf("Deploy to %s by %s (commit: %s)", deploy.Environment, deploy.DeployedBy, truncateSHA(deploy.CommitSHA, 8)),
			"severity":    deploySeverity(deploy.Status),
			"metadata": map[string]string{
				"version":     deploy.Version,
				"environment": deploy.Environment,
				"commit_sha":  deploy.CommitSHA,
				"deployed_by": deploy.DeployedBy,
				"source":      deploy.Source,
				"status":      deploy.Status,
			},
		}

		eventBytes, err := json.Marshal(event)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "failed to marshal event")
			return
		}

		// Store as a service event using the same topic pattern as handleEvents
		topic := topicForTenant(tenantID, "service_events")
		if err := sink.Write(topic, eventBytes); err != nil {
			slog.Error("sink write error for deploy event", "error", err)
			ingestionRequestsTotal.WithLabelValues("/api/v1/deploy", "500", tenantID).Inc()
			writeErrorJSON(w, http.StatusInternalServerError, "failed to persist deploy event")
			return
		}

		// Increment event counter for billing
		incrementEventCounter(tdb, tenantID, 1)

		slog.Info("deploy event recorded",
			"tenant", tenantID,
			"service", deploy.Service,
			"version", deploy.Version,
			"env", deploy.Environment,
			"source", deploy.Source)

		ingestionRequestsTotal.WithLabelValues("/api/v1/deploy", "201", tenantID).Inc()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "recorded",
			"service": deploy.Service,
		})
	}
}

func deploySeverity(status string) string {
	switch strings.ToLower(status) {
	case "failure", "error":
		return "critical"
	case "in_progress", "pending":
		return "info"
	default:
		return "low"
	}
}

func truncateSHA(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
