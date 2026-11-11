package auth

import (
	"errors"
	"regexp"
	"unicode/utf8"
)

var (
	// ErrUsername is returned for a username outside the allowed form.
	ErrUsername = errors.New("auth: username must be 2-32 characters of a-z, 0-9, _ and -, starting with a letter or digit")
	// ErrPasswordTooShort is returned for a password under 15 characters.
	ErrPasswordTooShort = errors.New("auth: password must be at least 15 characters")
	// ErrPasswordTooLong is returned for a password over 1,024 bytes.
	ErrPasswordTooLong = errors.New("auth: password must be at most 1024 bytes")
	// ErrPasswordEncoding is returned for a password that is not valid UTF-8.
	ErrPasswordEncoding = errors.New("auth: password must be valid UTF-8")
)

// Password length limits. NIST SP 800-63B-4 section 3.1.1.2 requires 15
// characters for a password used as a single factor; garret may run with
// client certificates off. The upper bound limits hashing work per request.
const (
	minPasswordChars = 15
	maxPasswordBytes = 1024
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,31}$`)

// ValidUsername returns ErrUsername unless s matches
// ^[a-z0-9][a-z0-9_-]{1,31}$.
func ValidUsername(s string) error {
	if !usernamePattern.MatchString(s) {
		return ErrUsername
	}
	return nil
}

// ValidPassword checks that s is valid UTF-8, at most 1,024 bytes and at
// least 15 Unicode code points long.
func ValidPassword(s string) error {
	switch {
	case !utf8.ValidString(s):
		return ErrPasswordEncoding
	case len(s) > maxPasswordBytes:
		return ErrPasswordTooLong
	case utf8.RuneCountInString(s) < minPasswordChars:
		return ErrPasswordTooShort
	}
	return nil
}
