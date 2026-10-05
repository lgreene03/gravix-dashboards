// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestEveryValuesFileRunsOneIngestionReplica is DD-035. Ingestion learns path
// templates per process, so a second replica multiplies the template budget
// and can give one raw path two templates (SD-016). And its buffer is one
// ReadWriteOnce volume, which a second pod cannot attach. The production
// values ran two replicas, scaling to ten, on that one volume.
//
// The chart refuses both at render time; this catches a values file that
// would be refused before anyone renders it.
func TestEveryValuesFileRunsOneIngestionReplica(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "deploy", "gravix", "values*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 4 {
		t.Fatalf("found %d values files; the chart moved and this test is checking too little", len(files))
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Ingestion struct {
				ReplicaCount          *int  `yaml:"replicaCount"`
				AllowMultipleReplicas *bool `yaml:"allowMultipleReplicas"`
			} `yaml:"ingestion"`
		}
		if err := yaml.Unmarshal(raw, &v); err != nil {
			t.Fatalf("%s: %v", filepath.Base(f), err)
		}
		optedIn := v.Ingestion.AllowMultipleReplicas != nil && *v.Ingestion.AllowMultipleReplicas
		if v.Ingestion.ReplicaCount != nil && *v.Ingestion.ReplicaCount > 1 && !optedIn {
			t.Errorf("%s runs ingestion at %d replicas without allowMultipleReplicas; templates would "+
				"differ between replicas (SD-016) and the chart refuses it", filepath.Base(f), *v.Ingestion.ReplicaCount)
		}
	}

	tpl, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "gravix", "templates", "ingestion.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"allowMultipleReplicas", "ReadWriteOnce", "type: Recreate"} {
		if !strings.Contains(string(tpl), want) {
			t.Errorf("templates/ingestion.yaml no longer mentions %q; its replica guard or its rollout "+
				"strategy for a single-attach volume is gone (DD-035)", want)
		}
	}
}
