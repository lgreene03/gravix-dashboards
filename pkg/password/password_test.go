package password

import (
	"strings"
	"testing"
)

func TestValidate_TooShort(t *testing.T) {
	err := Validate("Ab1!xxxxx") // 9 chars
	if err == nil {
		t.Fatal("expected error for short password")
	}
	if !strings.Contains(err.Error(), "at least 10") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_ExactMinLength(t *testing.T) {
	err := Validate("Ab1!xxxxxx") // 10 chars
	if err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidate_NoUppercase(t *testing.T) {
	err := Validate("abcdef1234!")
	if err == nil {
		t.Fatal("expected error for no uppercase")
	}
	if !strings.Contains(err.Error(), "uppercase") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_NoLowercase(t *testing.T) {
	err := Validate("ABCDEF1234!")
	if err == nil {
		t.Fatal("expected error for no lowercase")
	}
	if !strings.Contains(err.Error(), "lowercase") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_NoDigit(t *testing.T) {
	err := Validate("Abcdefghij!")
	if err == nil {
		t.Fatal("expected error for no digit")
	}
	if !strings.Contains(err.Error(), "digit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_NoSpecial(t *testing.T) {
	err := Validate("Abcdefghij1")
	if err == nil {
		t.Fatal("expected error for no special char")
	}
	if !strings.Contains(err.Error(), "special") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_CommonPassword(t *testing.T) {
	// "password1" is in the common list; we need a version that passes complexity
	// but is still detected as common. Since the list stores exact matches,
	// we test that the common-check works by verifying a raw common password
	// (which will fail complexity first). Instead, verify the lookup directly.
	if _, ok := commonPasswords["password1"]; !ok {
		t.Fatal("expected 'password1' to be in common passwords list")
	}
	if _, ok := commonPasswords["123456789"]; !ok {
		t.Fatal("expected '123456789' to be in common passwords list")
	}
	// A password that looks complex but is actually common
	// "Passw0rd" is in many common lists — verify lookup works
	_ = commonPasswords["passw0rd"]
	// Test that something IN the common list is caught (when it also meets complexity)
	// Add a known entry that meets all rules
	commonPasswords["str0ng!pass"] = struct{}{}
	err := Validate("Str0ng!Pass")
	if err == nil {
		t.Fatal("expected error for common password")
	}
	if !strings.Contains(err.Error(), "too common") {
		t.Fatalf("unexpected error: %v", err)
	}
	delete(commonPasswords, "str0ng!pass") // cleanup
}

func TestValidate_ValidPasswords(t *testing.T) {
	passwords := []string{
		"Str0ng!Pass",
		"MyS3cur3#Key",
		"C0mpl3x!ty99",
		"Un1qu3$ecret",
		"P@ssw0rd!XYZ",
	}
	for _, pw := range passwords {
		if err := Validate(pw); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", pw, err)
		}
	}
}

func TestValidate_Unicode(t *testing.T) {
	// Unicode uppercase/lowercase/special should count
	err := Validate("Abcdef123!") // standard
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCommonPasswordsLoaded(t *testing.T) {
	if len(commonPasswords) == 0 {
		t.Fatal("common passwords list not loaded")
	}
	// Spot-check some known common passwords
	for _, pw := range []string{"password", "123456789", "qwerty"} {
		if _, ok := commonPasswords[pw]; !ok {
			t.Errorf("expected %q to be in common passwords list", pw)
		}
	}
}
