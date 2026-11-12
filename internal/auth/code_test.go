package auth

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestWordlist(t *testing.T) {
	if len(words) != 7772 {
		t.Fatalf("%d words, want 7772", len(words))
	}
	seen := map[string]bool{}
	for _, w := range words {
		if w == "" || strings.ContainsAny(w, "- ") || seen[w] {
			t.Fatalf("bad or duplicate word %q", w)
		}
		seen[w] = true
	}
}

var codePattern = regexp.MustCompile(`^[a-z]+(-[a-z]+){5}$`)

func TestNewInvitationCode(t *testing.T) {
	r := seeded()
	a, err := NewInvitationCode(r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewInvitationCode(r)
	if !codePattern.MatchString(a) || a == b {
		t.Fatalf("codes %q and %q", a, b)
	}
	if _, err := NewInvitationCode(errReader{}); err == nil {
		t.Fatal("failing reader accepted")
	}
	if _, err := NewInvitationCode(bytes.NewReader(nil)); err == nil {
		t.Fatal("empty reader accepted")
	}
}

func TestNormalizeCode(t *testing.T) {
	for in, want := range map[string]string{
		"marble-orbit-candle":       "marble-orbit-candle",
		"Marble Orbit  Candle":      "marble-orbit-candle",
		"  marble - orbit--candle ": "marble-orbit-candle",
		"MARBLE\torbit\ncandle":     "marble-orbit-candle",
	} {
		if got := NormalizeCode(in); got != want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", in, got, want)
		}
	}
}
