// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/plugin"
	"github.com/lgreene/gravix-dashboards/pkg/registry"
	registryfile "github.com/lgreene/gravix-dashboards/registry"
)

// pluginTemplates holds the scaffolds. They are embedded rather than fetched so
// that `gravix plugin new` works offline and cannot be made to write somebody
// else's code.
//
//go:embed templates/plugin
var pluginTemplates embed.FS

// Exit codes for `gravix plugin`.
const (
	pluginExitOK     = 0
	pluginExitFailed = 1
	pluginExitUsage  = 2
)

// templateSuffix is stripped from a scaffold file's name when it is written.
const templateSuffix = ".tmpl"

// pluginMain runs the subcommand and returns its exit code rather than exiting,
// so tests can exercise every exit path.
func pluginMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		pluginUsage(stderr)
		return pluginExitUsage
	}

	switch args[0] {
	case "new":
		return pluginNew(args[1:], stdout, stderr)
	case "list":
		return pluginList(args[1:], stdout, stderr)
	case "validate":
		return pluginValidate(ctx, args[1:], stdout, stderr)
	case "help", "--help", "-h":
		pluginUsage(stdout)
		return pluginExitOK
	default:
		fmt.Fprintf(stderr, "unknown plugin subcommand %q\n", args[0])
		pluginUsage(stderr)
		return pluginExitUsage
	}
}

func pluginUsage(w io.Writer) {
	fmt.Fprint(w, `Usage: gravix plugin <new|list|validate> [flags]

  new       Scaffold a plugin that builds and passes its tests unedited
  list      List the plugins in the registry
  validate  Check a plugin's manifest and ABI against this Gravix

Gravix never downloads, builds, or runs a plugin from the registry. Listing is
not endorsement: see registry/README.md.
`)
}

// scaffoldData is what the templates are rendered with.
type scaffoldData struct {
	Name        string
	Kind        string
	Description string
	ABIVersion  string
	Language    string
	Maintainer  string
	Year        int
}

func pluginNew(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plugin new", flag.ContinueOnError)
	fs.SetOutput(stderr)

	name := fs.String("name", "", "plugin name; must start with gravix-")
	kind := fs.String("kind", string(plugin.KindNotifier), "notifier, exporter, or adapter")
	out := fs.String("out", "", "output directory")
	language := fs.String("language", "go", "go or python")

	if err := fs.Parse(args); err != nil {
		return pluginExitUsage
	}

	if *name == "" {
		fmt.Fprintf(stderr, "--name is required\n")
		return pluginExitUsage
	}
	if err := registry.ValidateName(*name); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return pluginExitUsage
	}
	if !plugin.Kind(*kind).Valid() {
		fmt.Fprintf(stderr, "--kind must be notifier, exporter, or adapter\n")
		return pluginExitUsage
	}
	if *language != "go" && *language != "python" {
		fmt.Fprintf(stderr, "--language must be go or python\n")
		return pluginExitUsage
	}

	dir := *out
	if dir == "" {
		dir = "./" + *name
	}
	if err := requireEmptyScaffoldDir(dir); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return pluginExitUsage
	}

	data := scaffoldData{
		Name:        *name,
		Kind:        *kind,
		Description: fmt.Sprintf("A Gravix %s plugin.", *kind),
		ABIVersion:  plugin.ABIVersion,
		Language:    *language,
		Maintainer:  "the " + *name + " authors",
		Year:        time.Now().UTC().Year(),
	}

	written, err := renderScaffold(*language, dir, data)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return pluginExitFailed
	}

	fmt.Fprintf(stdout, "Scaffolded %s (%s, %s) in %s\n", data.Name, data.Kind, data.Language, dir)
	for _, f := range written {
		fmt.Fprintf(stdout, "  %s\n", f)
	}
	fmt.Fprintf(stdout, "\nIt builds and passes its tests with no edits:\n  cd %s && make test\n", dir)
	fmt.Fprintf(stdout, "\nBefore publishing, replace LICENSE and fill in the README's Data flow\nsection — the registry will not list a plugin without one.\n")
	return pluginExitOK
}

// requireEmptyScaffoldDir refuses to write into a directory that already has
// something in it. Overwriting somebody's work in progress to save them one
// flag is not a trade worth making.
func requireEmptyScaffoldDir(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("%s: %v", dir, err)
	case len(entries) > 0:
		return fmt.Errorf("%s is not empty; refusing to overwrite", dir)
	}
	return nil
}

// renderScaffold writes the scaffold and returns the files it wrote, relative
// to dir.
func renderScaffold(language, dir string, data scaffoldData) ([]string, error) {
	root := "templates/plugin/" + language
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %v", dir, err)
	}

	var written []string
	err := fs.WalkDir(pluginTemplates, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		rel := strings.TrimPrefix(strings.TrimPrefix(path, root), "/")
		rel = strings.TrimSuffix(rel, templateSuffix)

		src, err := pluginTemplates.ReadFile(path)
		if err != nil {
			return err
		}
		tmpl, err := template.New(rel).Parse(string(src))
		if err != nil {
			return fmt.Errorf("parse template %s: %v", path, err)
		}

		dest := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, scaffoldMode(rel))
		if err != nil {
			return err
		}
		if err := tmpl.Execute(f, data); err != nil {
			f.Close()
			return fmt.Errorf("render %s: %v", rel, err)
		}
		if err := f.Close(); err != nil {
			return err
		}

		written = append(written, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return written, nil
}

// scaffoldMode makes a Python plugin executable, because Gravix runs it.
func scaffoldMode(rel string) os.FileMode {
	if rel == "plugin.py" {
		return 0o755
	}
	return 0o644
}

func pluginList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plugin list", flag.ContinueOnError)
	fs.SetOutput(stderr)

	kind := fs.String("kind", "", "only plugins of this kind")
	asJSON := fs.Bool("json", false, "print the entries as JSON")

	if err := fs.Parse(args); err != nil {
		return pluginExitUsage
	}
	if *kind != "" && !plugin.Kind(*kind).Valid() {
		fmt.Fprintf(stderr, "--kind must be notifier, exporter, or adapter\n")
		return pluginExitUsage
	}

	file, err := registry.ParseBytes(registryfile.PluginsJSON)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return pluginExitFailed
	}

	entries := file.Plugins
	if *kind != "" {
		entries = file.ByKind(*kind)
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(entries); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return pluginExitFailed
		}
		return pluginExitOK
	}

	if len(entries) == 0 {
		fmt.Fprintf(stdout, "No plugins listed%s.\n", kindSuffix(*kind))
		return pluginExitOK
	}

	width := 0
	for _, e := range entries {
		if len(e.Name) > width {
			width = len(e.Name)
		}
	}
	for _, e := range entries {
		origin := "third-party"
		if e.FirstParty {
			origin = "first-party"
		}
		fmt.Fprintf(stdout, "%-*s  %-8s  %-11s  %s\n", width, e.Name, e.Kind, origin, e.Repository)
		fmt.Fprintf(stdout, "%-*s  %s\n", width, "", e.Description)
	}
	// Said on every listing, not buried in a document nobody opens: a third
	// party plugin is third-party code with network access, and Gravix has not
	// reviewed it.
	fmt.Fprintf(stdout, "\nListing is not endorsement. Gravix does not review, audit, or vouch for\nthird-party plugins, and no Gravix command installs one. See registry/README.md.\n")
	return pluginExitOK
}

func kindSuffix(kind string) string {
	if kind == "" {
		return ""
	}
	return " of kind " + kind
}

func pluginValidate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("plugin validate", flag.ContinueOnError)
	fs.SetOutput(stderr)

	path := fs.String("path", "", "path to the plugin executable")

	if err := fs.Parse(args); err != nil {
		return pluginExitUsage
	}
	if *path == "" {
		fmt.Fprintf(stderr, "--path is required\n")
		return pluginExitUsage
	}
	abs, err := filepath.Abs(*path)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return pluginExitUsage
	}
	if _, err := os.Stat(abs); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return pluginExitUsage
	}

	// The manifest is asked for the same way Gravix asks for it at load time:
	// start the process and call gravix.describe. Reading a manifest from a
	// file beside the binary would validate a claim rather than the plugin.
	host := plugin.NewHost(plugin.HostOptions{
		Command:     abs,
		CallTimeout: 10 * time.Second,
	})
	defer host.Close()

	manifest, err := host.Start(ctx)
	if err != nil {
		if abi, ok := declaredABI(err); ok {
			// §6.1's message, printed here rather than reused from the error,
			// so the operator is told which build to fetch in one line.
			fmt.Fprintf(stderr, "plugin declares ABI %s, this Gravix supports %s\n", abi, plugin.ABIVersion)
			return pluginExitFailed
		}
		fmt.Fprintf(stderr, "%v\n", err)
		return pluginExitFailed
	}

	fmt.Fprintf(stdout, "%s %s (%s) is valid against ABI %s\n", manifest.Name, manifest.Version, manifest.Kind, plugin.ABIVersion)
	for field, spec := range manifest.ConfigSchema {
		required := "optional"
		if spec.Required {
			required = "required"
		}
		fmt.Fprintf(stdout, "  config %s: %s, %s\n", field, spec.Type, required)
	}
	return pluginExitOK
}

// declaredABI extracts the version a plugin declared from an ABI mismatch.
//
// The number is recovered from the error rather than by a second handshake,
// because the process has already been killed: pkg/plugin refuses an
// incompatible plugin before it can be called at all.
func declaredABI(err error) (string, bool) {
	if !errors.Is(err, plugin.ErrABIMismatch) {
		return "", false
	}
	const marker = "declares ABI "
	msg := err.Error()
	i := strings.Index(msg, marker)
	if i < 0 {
		return "", false
	}
	rest := msg[i+len(marker):]
	if j := strings.IndexByte(rest, ','); j >= 0 {
		return rest[:j], true
	}
	return rest, true
}
