// Package password provides server-side password strength validation.
package password

import (
	"bufio"
	"embed"
	"fmt"
	"strings"
	"unicode"
)

//go:embed common_passwords.txt
var commonFS embed.FS

var commonPasswords map[string]struct{}

func init() {
	commonPasswords = make(map[string]struct{}, 10000)
	f, err := commonFS.Open("common_passwords.txt")
	if err != nil {
		// Embedded file must exist; panic at startup if missing.
		panic(fmt.Sprintf("password: failed to open common_passwords.txt: %v", err))
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		pw := strings.TrimSpace(scanner.Text())
		if pw != "" && !strings.HasPrefix(pw, "#") {
			commonPasswords[strings.ToLower(pw)] = struct{}{}
		}
	}
}

// MinLength is the minimum password length.
const MinLength = 10

// Validate checks password strength requirements:
//   - At least 10 characters
//   - At least 1 uppercase letter
//   - At least 1 lowercase letter
//   - At least 1 digit
//   - At least 1 special character (!@#$%^&*... etc.)
//   - Not in top-10K common passwords list
func Validate(password string) error {
	if len(password) < MinLength {
		return fmt.Errorf("password must be at least %d characters", MinLength)
	}

	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSpecial = true
		}
	}

	if !hasUpper {
		return fmt.Errorf("password must contain at least one uppercase letter")
	}
	if !hasLower {
		return fmt.Errorf("password must contain at least one lowercase letter")
	}
	if !hasDigit {
		return fmt.Errorf("password must contain at least one digit")
	}
	if !hasSpecial {
		return fmt.Errorf("password must contain at least one special character")
	}

	if _, found := commonPasswords[strings.ToLower(password)]; found {
		return fmt.Errorf("password is too common, please choose a different one")
	}

	return nil
}
