// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package export

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lgreene/gravix-dashboards/pkg/tenantdb"
)

// RedactedFields are configuration fields never written to an export, because
// an export is a file a user will copy, email, and store — not a vault.
var RedactedFields = []string{
	"api_key", "key_material", "secret", "password_hash", "token",
	"client_secret", "private_key", "webhook_auth_header", "stripe_key",
}

// RedactedPlaceholder replaces a redacted value. The field itself is kept, so a
// reader can see what existed without seeing what it was.
const RedactedPlaceholder = "[redacted]"

// ErrSecretLeak is returned when a value matching a redaction rule reaches output.
var ErrSecretLeak = errors.New("export: refusing to write a secret to an export file")

// configFile is one exported configuration document.
type configFile struct {
	name  string
	build func(ctx context.Context, db tenantdb.DB, tenantID string) (any, error)
}

// configFiles is the set §5.1 requires, each named by its output filename.
func configFiles() []configFile {
	return []configFile{
		{"alert_rules.json", func(ctx context.Context, db tenantdb.DB, t string) (any, error) {
			return db.AlertRules().ListByTenant(ctx, t)
		}},
		{"dashboards.json", func(ctx context.Context, db tenantdb.DB, t string) (any, error) {
			return db.CustomDashboards().ListByTenant(ctx, t)
		}},
		{"slos.json", func(ctx context.Context, db tenantdb.DB, t string) (any, error) {
			return db.SLOs().ListByTenant(ctx, t)
		}},
		{"api_keys.json", func(ctx context.Context, db tenantdb.DB, t string) (any, error) {
			return db.APIKeys().ListByTenant(ctx, t)
		}},
		{"team.json", func(ctx context.Context, db tenantdb.DB, t string) (any, error) {
			return db.Users().ListByTenant(ctx, t)
		}},
		{"scheduled_exports.json", func(ctx context.Context, db tenantdb.DB, t string) (any, error) {
			return db.ScheduledExports().ListByTenant(ctx, t)
		}},
	}
}

// ExportConfig writes tenant configuration as JSON with every RedactedFields
// value replaced by the literal string "[redacted]". The field is retained so a
// reader knows it existed; only the value is removed.
func ExportConfig(ctx context.Context, db tenantdb.DB, tenantID, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("export: create %s: %w", outDir, err)
	}

	for _, cf := range configFiles() {
		records, err := cf.build(ctx, db, tenantID)
		if err != nil {
			return fmt.Errorf("export: read %s: %w", cf.name, err)
		}

		data, err := redactedJSON(records)
		if err != nil {
			return fmt.Errorf("export: encode %s: %w", cf.name, err)
		}

		path := filepath.Join(outDir, cf.name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return fmt.Errorf("export: write %s: %w", path, err)
		}

		// The post-write scan is the real guard. Redacting by field selection
		// protects the fields we thought of; scanning what was actually
		// written protects against the field someone adds next year. A leak
		// deletes the file rather than leaving it on disk for someone to email.
		if field, err := scanForSecrets(path); err != nil {
			os.Remove(path)
			return fmt.Errorf("%w: %s in %s", ErrSecretLeak, field, cf.name)
		}
	}

	return nil
}

// redactedJSON marshals records, then walks the generic form replacing every
// value whose key matches a redaction rule.
//
// Going through a generic map rather than struct tags is deliberate: it redacts
// whatever the type actually serialises today, including a field added after
// this code was written.
func redactedJSON(records any) ([]byte, error) {
	raw, err := json.Marshal(records)
	if err != nil {
		return nil, err
	}

	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}

	return json.MarshalIndent(redactValue(generic), "", "  ")
}

// redactValue walks a decoded JSON value, replacing redacted-key values.
func redactValue(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			if IsRedactedField(key) {
				out[key] = RedactedPlaceholder
				continue
			}
			out[key] = redactValue(value)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, value := range typed {
			out[i] = redactValue(value)
		}
		return out
	case string:
		// A secret can hide inside a string that happens to hold JSON —
		// pkg/tenantdb stores notification channel settings as a blob in a
		// field called Config, and a webhook auth header lives inside it. A
		// scan that only walks decoded structure would step straight past it.
		if inner, ok := decodeJSONString(typed); ok {
			redacted, err := json.Marshal(redactValue(inner))
			if err == nil {
				return string(redacted)
			}
		}
		return typed
	default:
		return v
	}
}

// decodeJSONString reports whether s is itself a JSON object or array, and
// returns it decoded. A bare number or quoted word is not treated as nested
// JSON: only a container can hide a named field.
func decodeJSONString(s string) (any, bool) {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil, false
	}
	var inner any
	if err := json.Unmarshal([]byte(trimmed), &inner); err != nil {
		return nil, false
	}
	return inner, true
}

// IsRedactedField reports whether a field name must never carry a value into an
// export.
//
// Matching normalises case and underscores, so a Go field named
// TwoFactorSecret is caught by the rule "secret" and PasswordHash by
// "password_hash" — the structs in pkg/tenantdb carry no json tags, so the key
// in the output is the Go name. It deliberately over-matches: redacting an
// identifier that merely looks like a secret costs a reader nothing, and
// missing one turns a backup into a breach.
func IsRedactedField(name string) bool {
	normalised := normaliseFieldName(name)
	if normalised == "" {
		return false
	}

	for _, rule := range RedactedFields {
		normalisedRule := normaliseFieldName(rule)

		// The field is more specific than the rule: TwoFactorSecret matches
		// "secret", PasswordHash matches "password_hash".
		if strings.Contains(normalised, normalisedRule) {
			return true
		}

		// The rule is more specific than the field. This is not symmetry for
		// its own sake: the rule "webhook_auth_header" has to catch a field
		// literally named auth_header, which is how it appears inside the
		// notification-channel config blob. The length floor keeps a short,
		// innocuous name from matching a long rule by accident.
		if len(normalised) >= minRedactionMatch && strings.Contains(normalisedRule, normalised) {
			return true
		}
	}
	return false
}

// minRedactionMatch is the shortest field name allowed to match by being
// contained in a rule, rather than containing one.
const minRedactionMatch = 5

func normaliseFieldName(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", ""))
}

// scanForSecrets re-reads a written file and reports the first redacted-name
// field whose value is not the placeholder.
func scanForSecrets(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("export: re-read %s: %w", path, err)
	}

	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return "", fmt.Errorf("export: re-parse %s: %w", path, err)
	}

	if field, leaked := findLeak(generic); leaked {
		return field, ErrSecretLeak
	}
	return "", nil
}

// findLeak walks a decoded document for a redacted-name field carrying
// anything other than the placeholder.
func findLeak(v any) (string, bool) {
	switch typed := v.(type) {
	case map[string]any:
		for key, value := range typed {
			if IsRedactedField(key) {
				// An empty value is not a leak: the field exists and holds
				// nothing, which is the truth and not a secret.
				if s, ok := value.(string); ok && (s == RedactedPlaceholder || s == "") {
					continue
				}
				if value == nil {
					continue
				}
				return key, true
			}
			if field, leaked := findLeak(value); leaked {
				return field, true
			}
		}
	case []any:
		for _, value := range typed {
			if field, leaked := findLeak(value); leaked {
				return field, true
			}
		}
	case string:
		if inner, ok := decodeJSONString(typed); ok {
			return findLeak(inner)
		}
	}
	return "", false
}
