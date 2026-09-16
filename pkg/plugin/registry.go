// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"fmt"
	"sort"
	"sync"
)

// Entry is a registered plugin: what it declared, and what it can be called
// through.
type Entry struct {
	Manifest Manifest
	// Impl is the plugin's implementation. Its dynamic type matches the
	// manifest's Kind — Notifier, Exporter or Adapter — and Lookup callers
	// assert accordingly.
	Impl any
}

// Registry holds the loaded plugins. It is safe for concurrent use.
//
// There is one registry, not one per kind: charter §7.2 has ee/ register
// against core extension points, and two registries would mean two answers to
// "what is installed".
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*Entry
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{entries: map[string]*Entry{}}
}

// Register validates a manifest and adds the plugin.
//
// It refuses an ABI mismatch and a duplicate name rather than overwriting:
// silently replacing a registered plugin would make which one runs depend on
// load order.
func (r *Registry) Register(m Manifest, impl any) error {
	if err := m.Validate(); err != nil {
		return err
	}

	if err := assertKindMatchesImpl(m, impl); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.entries[m.Name]; exists {
		return fmt.Errorf("%w: plugin %q is already registered", ErrDuplicateName, m.Name)
	}

	r.entries[m.Name] = &Entry{Manifest: m, Impl: impl}
	return nil
}

// assertKindMatchesImpl checks that a plugin does what its manifest claims, so
// a lookup by kind can assert the interface without a surprise at call time.
func assertKindMatchesImpl(m Manifest, impl any) error {
	var ok bool
	switch m.Kind {
	case KindNotifier:
		_, ok = impl.(Notifier)
	case KindExporter:
		_, ok = impl.(Exporter)
	case KindAdapter:
		_, ok = impl.(Adapter)
	}
	if !ok {
		return fmt.Errorf("%w: plugin %q declares kind %q but does not implement it", ErrManifestInvalid, m.Name, m.Kind)
	}
	return nil
}

// Lookup returns the entry registered under name.
func (r *Registry) Lookup(name string) (*Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	return e, ok
}

// ByKind returns every entry of a kind, ordered by name so callers iterate
// deterministically.
func (r *Registry) ByKind(kind Kind) []*Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []*Entry
	for _, e := range r.entries {
		if e.Manifest.Kind == kind {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.Name < out[j].Manifest.Name })
	return out
}

// Names returns every registered name, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]string, 0, len(r.entries))
	for name := range r.entries {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Deregister removes a plugin. It is used when a plugin is uninstalled, and by
// tests; a plugin disabled by the failure budget stays registered so the
// operator can still see it and why.
func (r *Registry) Deregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, name)
}

// Notifiers returns every registered notifier, ready to call.
func (r *Registry) Notifiers() []Notifier {
	var out []Notifier
	for _, e := range r.ByKind(KindNotifier) {
		if n, ok := e.Impl.(Notifier); ok {
			out = append(out, n)
		}
	}
	return out
}
