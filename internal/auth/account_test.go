package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestValidUsername(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"two characters", "ab", true},
		{"dash and underscore", "a-b_c", true},
		{"digits first", "9lives", true},
		{"32 characters", strings.Repeat("a", 32), true},
		{"one character", "a", false},
		{"33 characters", strings.Repeat("a", 33), false},
		{"uppercase", "Alice", false},
		{"leading dash", "-ab", false},
		{"leading underscore", "_ab", false},
		{"dot", "a.b", false},
		{"Hangul", "한글", false},
		{"empty", "", false},
		{"trailing newline", "alice\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidUsername(tt.in)
			if tt.ok && err != nil {
				t.Fatalf("ValidUsername(%q) = %v, want nil", tt.in, err)
			}
			if !tt.ok && !errors.Is(err, ErrUsername) {
				t.Fatalf("ValidUsername(%q) = %v, want ErrUsername", tt.in, err)
			}
		})
	}
}

func TestValidPassword(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want error
	}{
		{"14 code points", strings.Repeat("a", 14), ErrPasswordTooShort},
		{"15 code points", strings.Repeat("a", 15), nil},
		{"15 Hangul syllables", strings.Repeat("가", 15), nil},
		{"14 Hangul syllables (42 bytes)", strings.Repeat("가", 14), ErrPasswordTooShort},
		{"1024 bytes", strings.Repeat("a", 1024), nil},
		{"1025 bytes", strings.Repeat("a", 1025), ErrPasswordTooLong},
		{"invalid UTF-8", strings.Repeat("a", 15) + "\xff", ErrPasswordEncoding},
		{"empty", "", ErrPasswordTooShort},
		{"spaces count", strings.Repeat(" ", 15), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidPassword(tt.in); !errors.Is(err, tt.want) {
				t.Fatalf("ValidPassword = %v, want %v", err, tt.want)
			}
		})
	}
}
