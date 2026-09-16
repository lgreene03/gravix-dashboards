// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type stubNotifier struct{ calls int }

func (s *stubNotifier) Notify(context.Context, Alert) error { s.calls++; return nil }

type stubExporter struct{}

func (stubExporter) Export(context.Context, Batch) (ExportResult, error) {
	return ExportResult{}, nil
}

type stubAdapter struct{}

func (stubAdapter) Convert(context.Context, []byte, string) ([]RequestFact, error) { return nil, nil }

func manifestFor(name string, kind Kind) Manifest {
	return Manifest{Name: name, Version: "1.0.0", ABIVersion: ABIVersion, Kind: kind, License: "Apache-2.0"}
}

func TestRegistryRegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	impl := &stubNotifier{}

	if err := r.Register(manifestFor("slack-plus", KindNotifier), impl); err != nil {
		t.Fatalf("Register: %v", err)
	}

	e, ok := r.Lookup("slack-plus")
	if !ok {
		t.Fatal("registered plugin not found")
	}
	if e.Manifest.Kind != KindNotifier || e.Impl != impl {
		t.Errorf("entry = %+v", e)
	}

	if _, ok := r.Lookup("absent"); ok {
		t.Error("an unregistered name was found")
	}
}

// A duplicate is refused rather than overwritten: silently replacing a plugin
// would make which one runs depend on load order.
func TestRegistryRefusesDuplicateName(t *testing.T) {
	r := NewRegistry()
	first := &stubNotifier{}

	if err := r.Register(manifestFor("dup", KindNotifier), first); err != nil {
		t.Fatalf("Register: %v", err)
	}
	err := r.Register(manifestFor("dup", KindNotifier), &stubNotifier{})
	if !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("err = %v, want ErrDuplicateName", err)
	}
	if !strings.Contains(err.Error(), `"dup"`) {
		t.Errorf("error %q does not name the plugin", err)
	}

	// The original must survive, not be replaced.
	e, _ := r.Lookup("dup")
	if e.Impl != first {
		t.Error("the duplicate replaced the original")
	}
}

func TestRegistryRefusesABIMismatch(t *testing.T) {
	r := NewRegistry()
	m := manifestFor("old-plugin", KindNotifier)
	m.ABIVersion = "0"

	if err := r.Register(m, &stubNotifier{}); !errors.Is(err, ErrABIMismatch) {
		t.Fatalf("err = %v, want ErrABIMismatch", err)
	}
	if len(r.Names()) != 0 {
		t.Error("an ABI-mismatched plugin was registered anyway")
	}
}

// A manifest that claims one kind while implementing another is refused at
// registration, so a lookup by kind can assert the interface without a
// surprise at call time.
func TestRegistryRefusesKindMismatch(t *testing.T) {
	r := NewRegistry()

	cases := map[string]struct {
		kind Kind
		impl any
	}{
		"notifier that is not one": {KindNotifier, stubExporter{}},
		"exporter that is not one": {KindExporter, &stubNotifier{}},
		"adapter that is not one":  {KindAdapter, stubExporter{}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := r.Register(manifestFor("x-"+strings.ReplaceAll(name, " ", "-"), tc.kind), tc.impl)
			if !errors.Is(err, ErrManifestInvalid) {
				t.Fatalf("err = %v, want ErrManifestInvalid", err)
			}
		})
	}
}

func TestRegistryByKindIsSortedAndFiltered(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"zeta", "alpha", "mid"} {
		if err := r.Register(manifestFor(name, KindNotifier), &stubNotifier{}); err != nil {
			t.Fatalf("Register %s: %v", name, err)
		}
	}
	if err := r.Register(manifestFor("an-exporter", KindExporter), stubExporter{}); err != nil {
		t.Fatalf("Register exporter: %v", err)
	}
	if err := r.Register(manifestFor("an-adapter", KindAdapter), stubAdapter{}); err != nil {
		t.Fatalf("Register adapter: %v", err)
	}

	var got []string
	for _, e := range r.ByKind(KindNotifier) {
		got = append(got, e.Manifest.Name)
	}
	want := []string{"alpha", "mid", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ByKind(notifier) = %v, want %v — iteration must be deterministic", got, want)
	}

	if n := len(r.ByKind(KindExporter)); n != 1 {
		t.Errorf("ByKind(exporter) returned %d", n)
	}
	if n := len(r.Notifiers()); n != 3 {
		t.Errorf("Notifiers() returned %d, want 3", n)
	}
}

func TestRegistryNamesAndDeregister(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"b-plugin", "a-plugin"} {
		if err := r.Register(manifestFor(name, KindNotifier), &stubNotifier{}); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}

	if got, want := r.Names(), []string{"a-plugin", "b-plugin"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}

	r.Deregister("a-plugin")
	if _, ok := r.Lookup("a-plugin"); ok {
		t.Error("a deregistered plugin is still present")
	}
	// Deregistering something absent is not an error.
	r.Deregister("never-existed")
}

// AC-8: no plugin is invoked on the ingestion hot path.
//
// This is enforced structurally rather than by inspection: if the ingestion
// service imported pkg/plugin, a plugin call could sit between a request and
// its durable write, which is the one place a third party's latency must never
// appear.
func TestNoPluginOnIngestHotPath(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps",
		"github.com/lgreene/gravix-dashboards/services/ingestion").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}

	if strings.Contains(string(out), "gravix-dashboards/pkg/plugin") {
		t.Fatal("services/ingestion depends on pkg/plugin; a plugin must never sit between a request and its durable write")
	}
}

// AC-12: Go's plugin package is not imported anywhere.
//
// §3 forbids it because its toolchain coupling would break every plugin on
// every Gravix release — the opposite of the stable ABI this package exists to
// provide.
func TestNoGoPluginPackage(t *testing.T) {
	roots := []string{"../../pkg", "../../services", "../../cmd", "../../transforms"}

	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				// The stdlib import, not this package's own path.
				if trimmed == `"plugin"` || strings.HasPrefix(trimmed, `_ "plugin"`) {
					t.Errorf("%s imports Go's plugin package; §3 forbids it", path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
}

// AC-10: the four existing notifiers pass their original tests unchanged.
//
// Retrofitting an interface onto working code is exactly where behaviour gets
// broken quietly, so the check is the original suite itself, run for real —
// not an inspection of the source.
func TestExistingNotifiersUnchanged(t *testing.T) {
	out, err := exec.Command("go", "test", "-count=1",
		"github.com/lgreene/gravix-dashboards/pkg/notify").CombinedOutput()
	if err != nil {
		t.Fatalf("the existing notifier tests no longer pass:\n%s", out)
	}

	// The interface itself is proved by compile-time assertions in
	// pkg/notify, which the build enforces; an internal test here cannot
	// import pkg/notify, because pkg/notify imports this package.
	if !strings.Contains(string(out), "ok") {
		t.Errorf("unexpected test output:\n%s", out)
	}
}
