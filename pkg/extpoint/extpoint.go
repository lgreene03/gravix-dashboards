// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package extpoint is the one registry ee/ code is allowed to reach core through.
//
// Core never imports ee/. Instead, ee/ packages register an Extension here from their
// own init() function, and core's gatewaycore.Run mounts whatever is registered onto
// its existing HTTP listener. In the OSS binary, ee/ is never imported by anything in
// the build graph, so Registered() always returns an empty slice, the mount loop in
// gatewaycore never executes its body, and /api/gateway/ee/status always reports
// {"extensions":[]}. This is not a special case in the code — it is what an empty
// registry does by construction. No path in this package logs, warns, or errors when
// the registry is empty; emptiness is the permanent, correct state of every OSS install
// (charter §7.4, §7.5).
package extpoint

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
)

// Extension is implemented by an ee/ package that wants to mount its own HTTP surface
// onto the shared gateway process. Each ee/ feature package (ee/tenancy, ee/billing,
// ee/identity, ...) registers exactly one Extension from its own init().
type Extension interface {
	// Name is a short, stable, lowercase identifier, e.g. "tenancy", "identity". Used
	// only for the /api/gateway/ee/status listing and registry bookkeeping — it has no
	// effect on routing.
	Name() string

	// PathPrefix is the exact mount point, e.g. "/ee/tenancy/". Must start and end with
	// "/". The registered Handler receives requests with this prefix already stripped
	// (net/http.StripPrefix semantics): a request to "/ee/tenancy/policies" reaches the
	// handler as "/policies".
	PathPrefix() string

	// Handler is the http.Handler mounted at PathPrefix. It is called once, by
	// gatewaycore's mount loop, and the returned Handler is reused for the life of
	// the process — Handler must not depend on per-request state to construct
	// itself. Register does not call it: registration happens in an ee/ package's
	// init(), before any flag is parsed or any dependency exists.
	Handler() http.Handler
}

var (
	mu         sync.RWMutex
	extensions = map[string]Extension{} // keyed by Name()
	byPrefix   = map[string]Extension{} // keyed by PathPrefix()
)

// Register adds ext to the registry. It panics if ext, ext.Name(), or ext.PathPrefix()
// is invalid, if PathPrefix does not both start and end with "/", or if the Name or
// PathPrefix collides with an already-registered Extension. Register is called only
// from package init() functions in ee/ packages, so a panic here is a build-time
// programming error, never a runtime condition an operator can trigger — it must never
// be reached in the OSS binary, where this function is never called.
func Register(ext Extension) {
	if ext == nil {
		panic("extpoint: Register called with a nil Extension")
	}
	name := ext.Name()
	prefix := ext.PathPrefix()
	if name == "" {
		panic("extpoint: Extension.Name() must be non-empty")
	}
	if len(prefix) < 2 || prefix[0] != '/' || prefix[len(prefix)-1] != '/' {
		panic(fmt.Sprintf("extpoint: Extension %q PathPrefix() = %q must start and end with \"/\"", name, prefix))
	}
	mu.Lock()
	defer mu.Unlock()
	if _, exists := extensions[name]; exists {
		panic(fmt.Sprintf("extpoint: Extension name %q registered twice", name))
	}
	if _, exists := byPrefix[prefix]; exists {
		panic(fmt.Sprintf("extpoint: PathPrefix %q registered twice", prefix))
	}
	extensions[name] = ext
	byPrefix[prefix] = ext
}

// Registered returns every registered Extension, sorted by Name for deterministic
// iteration. In the OSS binary this is always an empty, non-nil slice.
func Registered() []Extension {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Extension, 0, len(extensions))
	for _, ext := range extensions {
		out = append(out, ext)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Mounted returns the Extension registered at the exact PathPrefix, and whether one was
// found. Used by gatewaycore's mount loop.
func Mounted(prefix string) (Extension, bool) {
	mu.RLock()
	defer mu.RUnlock()
	ext, ok := byPrefix[prefix]
	return ext, ok
}
