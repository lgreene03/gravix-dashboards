// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package recipes

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// ─── AC-12 ───

// TestRailsRecipeStaticContract checks the one recipe CI does not execute.
//
// There is no Ruby SDK and no Ruby toolchain in CI (GRVX-909 §3), so this
// recipe is held to its contract statically. That is a weaker guarantee than
// the other five get, and it is the reason the checks below are specific about
// what would actually be wrong rather than just grepping for "looks Ruby-ish".
func TestRailsRecipeStaticContract(t *testing.T) {
	body, err := os.ReadFile("rails/client.rb")
	if err != nil {
		t.Fatalf("read the rails recipe: %v", err)
	}
	src := string(body)

	for _, want := range []string{"Net::HTTP", "/api/v1/facts", "X-API-Key"} {
		if !strings.Contains(src, want) {
			t.Errorf("AC-12 FAILED: the recipe does not use %s", want)
		}
	}

	// Every path_template value must be a route, never a URL. A raw id here is
	// the single mistake this whole recipe set exists to prevent, and with no
	// SDK there is nothing to catch it at runtime.
	templateRe := regexp.MustCompile(`path_template:\s*"([^"]*)"`)
	matches := templateRe.FindAllStringSubmatch(src, -1)
	if len(matches) == 0 {
		t.Fatal("AC-12 FAILED: the recipe sends no path_template at all")
	}
	rawID := regexp.MustCompile(`/[0-9]{4,}(/|$)`)
	uuid := regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	for _, m := range matches {
		tpl := m[1]
		if rawID.MatchString(tpl) {
			t.Errorf("AC-12 FAILED: path_template %q contains a raw numeric id", tpl)
		}
		if uuid.MatchString(tpl) {
			t.Errorf("AC-12 FAILED: path_template %q contains a raw UUID", tpl)
		}
		if strings.Contains(tpl, "?") {
			t.Errorf("AC-12 FAILED: path_template %q contains a query string", tpl)
		}
	}

	// Ruby's SecureRandom.uuid is a v4, which every endpoint rejects with
	// "event_id must be UUIDv7 (got v4)". With no SDK to get this right, the
	// recipe has to generate a v7 itself — this is SD-017 in a language with
	// no library to hide it. Checked here because CI cannot run the file.
	//
	// Comments are stripped first: the recipe *names* SecureRandom.uuid in
	// order to warn against it, and a check that cannot tell mention from use
	// forbids the explanation along with the mistake. That was F-009 exactly,
	// where a guard banned the byte sequence that documented the ban.
	if strings.Contains(stripRubyComments(src), "SecureRandom.uuid") {
		t.Error("AC-12 FAILED: the recipe uses SecureRandom.uuid, which returns a version 4 " +
			"UUID. Gravix rejects it with \"event_id must be UUIDv7 (got v4)\", so this recipe " +
			"would fail on first use")
	}
	if !strings.Contains(src, "0x70") {
		t.Error("AC-12 FAILED: no UUIDv7 version nibble is set; the generated id would not be a v7")
	}
}

// stripRubyComments removes whole-line and trailing comments, so a check can
// distinguish what the code does from what the prose explains.
func stripRubyComments(src string) string {
	var out strings.Builder
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// A trailing comment, being careful not to cut inside a string literal.
		if i := indexUnquoted(line, '#'); i >= 0 {
			line = line[:i]
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

// indexUnquoted finds c outside any single- or double-quoted run.
func indexUnquoted(s string, c byte) int {
	var inSingle, inDouble bool
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\'' && !inDouble:
			inSingle = !inSingle
		case s[i] == '"' && !inSingle:
			inDouble = !inDouble
		case s[i] == c && !inSingle && !inDouble:
			return i
		}
	}
	return -1
}
