// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package extpoint

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fake is a minimal Extension. Name and prefix are fields so a test can build an
// invalid one without needing a second type.
type fake struct {
	name   string
	prefix string
	body   string
}

func (f fake) Name() string       { return f.name }
func (f fake) PathPrefix() string { return f.prefix }
func (f fake) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(f.body))
	})
}

// reset empties the registry and restores it afterwards, so no test can leave
// state behind for the next one. Every test that calls Register calls this first.
func reset(t *testing.T) {
	t.Helper()
	mu.Lock()
	prevExt, prevPrefix := extensions, byPrefix
	extensions = map[string]Extension{}
	byPrefix = map[string]Extension{}
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		extensions, byPrefix = prevExt, prevPrefix
		mu.Unlock()
	})
}

// mustPanic runs fn and returns the panic value, failing if fn did not panic.
func mustPanic(t *testing.T, fn func()) (v any) {
	t.Helper()
	defer func() {
		v = recover()
		if v == nil {
			t.Fatal("expected a panic, got none")
		}
	}()
	fn()
	return nil
}

// AC-1. This is the state of every OSS install, permanently: nothing registers,
// so the registry is empty and the mount loop in gatewaycore never runs its body.
// The assertion is on a non-nil slice as well as an empty one — a nil return would
// still range zero times, but it would make the status endpoint emit `null` instead
// of `[]`, which is a different response body.
func TestRegisteredEmptyByDefault(t *testing.T) {
	got := Registered()
	if got == nil {
		t.Fatal("Registered() returned nil; it must return an empty, non-nil slice")
	}
	if len(got) != 0 {
		t.Fatalf("Registered() = %d extensions before any Register call; want 0", len(got))
	}
}

func TestRegisterAndMount(t *testing.T) {
	reset(t)
	Register(fake{name: "tenancy", prefix: "/ee/tenancy/", body: "tenancy-ok"})

	got := Registered()
	if len(got) != 1 {
		t.Fatalf("Registered() = %d; want 1", len(got))
	}
	if got[0].Name() != "tenancy" {
		t.Errorf("Name() = %q; want %q", got[0].Name(), "tenancy")
	}

	ext, ok := Mounted("/ee/tenancy/")
	if !ok {
		t.Fatal("Mounted(\"/ee/tenancy/\") = not found; want found")
	}
	rec := httptest.NewRecorder()
	ext.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Body.String() != "tenancy-ok" {
		t.Errorf("handler body = %q; want %q", rec.Body.String(), "tenancy-ok")
	}

	if _, ok := Mounted("/ee/absent/"); ok {
		t.Error("Mounted(\"/ee/absent/\") = found; want not found")
	}
}

// Registered sorts by Name so the status endpoint's output is stable across
// restarts. Map iteration order alone would make it vary run to run.
func TestRegisteredIsSortedByName(t *testing.T) {
	reset(t)
	Register(fake{name: "whitelabel", prefix: "/ee/whitelabel/"})
	Register(fake{name: "billing", prefix: "/ee/billing/"})
	Register(fake{name: "identity", prefix: "/ee/identity/"})

	var names []string
	for _, ext := range Registered() {
		names = append(names, ext.Name())
	}
	if want := "billing,identity,whitelabel"; strings.Join(names, ",") != want {
		t.Errorf("Registered() names = %v; want %s", names, want)
	}
}

func TestRegisterRejectsNil(t *testing.T) {
	reset(t)
	v := mustPanic(t, func() { Register(nil) })
	if got, want := v.(string), "extpoint: Register called with a nil Extension"; got != want {
		t.Errorf("panic = %q; want %q", got, want)
	}
}

func TestRegisterRejectsEmptyName(t *testing.T) {
	reset(t)
	v := mustPanic(t, func() { Register(fake{name: "", prefix: "/ee/x/"}) })
	if got, want := v.(string), "extpoint: Extension.Name() must be non-empty"; got != want {
		t.Errorf("panic = %q; want %q", got, want)
	}
}

// AC-2. A prefix without a trailing slash would mount as an exact-match route in
// net/http's mux, so "/ee/tenancy/policies" would 404 while "/ee/tenancy" worked —
// a failure mode worth catching at init() rather than in production.
func TestRegisterRejectsBadPrefix(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
	}{
		{"no trailing slash", "/ee/tenancy"},
		{"no leading slash", "ee/tenancy/"},
		{"empty", ""},
		{"bare slash", "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reset(t)
			v := mustPanic(t, func() { Register(fake{name: "tenancy", prefix: tc.prefix}) })
			msg, _ := v.(string)
			if !strings.Contains(msg, `must start and end with "/"`) {
				t.Errorf("panic = %q; want it to mention the leading/trailing slash rule", msg)
			}
			if !strings.Contains(msg, tc.prefix) && tc.prefix != "" {
				t.Errorf("panic = %q; want it to quote the offending prefix %q", msg, tc.prefix)
			}
		})
	}
}

// AC-3.
func TestRegisterRejectsDuplicateName(t *testing.T) {
	reset(t)
	Register(fake{name: "tenancy", prefix: "/ee/tenancy/"})
	v := mustPanic(t, func() { Register(fake{name: "tenancy", prefix: "/ee/other/"}) })
	if got, want := v.(string), `extpoint: Extension name "tenancy" registered twice`; got != want {
		t.Errorf("panic = %q; want %q", got, want)
	}
}

// AC-4. Two extensions on one prefix is the more dangerous collision: the mux
// would panic at mount time with a message about the pattern, not about which
// two ee/ packages disagreed.
func TestRegisterRejectsDuplicatePrefix(t *testing.T) {
	reset(t)
	Register(fake{name: "tenancy", prefix: "/ee/shared/"})
	v := mustPanic(t, func() { Register(fake{name: "billing", prefix: "/ee/shared/"}) })
	if got, want := v.(string), `extpoint: PathPrefix "/ee/shared/" registered twice`; got != want {
		t.Errorf("panic = %q; want %q", got, want)
	}
}

// A rejected registration must not be half-applied: the first of the two maps
// must not keep the name when the prefix check is what failed.
func TestRejectedRegistrationLeavesNothingBehind(t *testing.T) {
	reset(t)
	Register(fake{name: "tenancy", prefix: "/ee/shared/"})
	func() {
		defer func() { recover() }()
		Register(fake{name: "billing", prefix: "/ee/shared/"})
	}()
	if len(Registered()) != 1 {
		t.Errorf("Registered() = %d after a rejected Register; want 1", len(Registered()))
	}
	if _, ok := Mounted("/ee/shared/"); !ok {
		t.Error("the original extension lost its mount point")
	}
}

// Register must not invoke Handler(). Registration runs from an ee/ package's
// init(), before flags are parsed or any dependency is constructed; a Handler that
// was built there would be built against nothing. gatewaycore's mount loop is what
// calls it, once, after Run has assembled the gateway.
func TestRegisterDoesNotCallHandler(t *testing.T) {
	reset(t)
	Register(exploding{})
	if len(Registered()) != 1 {
		t.Fatal("registration did not take effect")
	}
	// Reaching here at all is the assertion: exploding.Handler panics when called.
	if _, ok := Mounted("/ee/exploding/"); !ok {
		t.Error("Mounted did not find the registered extension")
	}
}

type exploding struct{}

func (exploding) Name() string       { return "exploding" }
func (exploding) PathPrefix() string { return "/ee/exploding/" }
func (exploding) Handler() http.Handler {
	panic("Handler() was called during registration or lookup")
}
