package main

import (
	"testing"
)

// ─── resolveStr tests ───

func TestResolveStrFlagValue(t *testing.T) {
	got := resolveStr("flag-value", "fallback-value")
	if got != "flag-value" {
		t.Errorf("resolveStr() = %q, want flag-value", got)
	}
}

func TestResolveStrFallback(t *testing.T) {
	got := resolveStr("", "fallback-value")
	if got != "fallback-value" {
		t.Errorf("resolveStr() = %q, want fallback-value", got)
	}
}

func TestResolveStrBothEmpty(t *testing.T) {
	got := resolveStr("", "")
	if got != "" {
		t.Errorf("resolveStr() = %q, want empty", got)
	}
}

// ─── propertyFlags tests ───

func TestPropertyFlagsSetValid(t *testing.T) {
	var pf propertyFlags
	if err := pf.Set("region=us-east-1"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if pf.pairs["region"] != "us-east-1" {
		t.Errorf("pairs[region] = %q, want us-east-1", pf.pairs["region"])
	}
}

func TestPropertyFlagsSetMultiple(t *testing.T) {
	var pf propertyFlags
	pf.Set("key1=val1")
	pf.Set("key2=val2")
	if len(pf.pairs) != 2 {
		t.Errorf("len(pairs) = %d, want 2", len(pf.pairs))
	}
	if pf.pairs["key1"] != "val1" || pf.pairs["key2"] != "val2" {
		t.Errorf("unexpected pairs: %v", pf.pairs)
	}
}

func TestPropertyFlagsSetValueWithEquals(t *testing.T) {
	var pf propertyFlags
	if err := pf.Set("url=http://host?a=b"); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	// SplitN(val, "=", 2) should keep the rest in the value
	if pf.pairs["url"] != "http://host?a=b" {
		t.Errorf("pairs[url] = %q, want http://host?a=b", pf.pairs["url"])
	}
}

func TestPropertyFlagsSetInvalid(t *testing.T) {
	var pf propertyFlags
	err := pf.Set("no-equals-sign")
	if err == nil {
		t.Error("Set() should return error for value without =")
	}
}

func TestPropertyFlagsString(t *testing.T) {
	var pf propertyFlags
	// Just ensure it doesn't panic on nil map
	_ = pf.String()
}
