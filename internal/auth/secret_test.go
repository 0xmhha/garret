package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/rand/v2"
	"regexp"
	"testing"
)

func seeded() io.Reader { return rand.NewChaCha8([32]byte{7}) }

var base64URL = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func TestNewSecret(t *testing.T) {
	r := seeded()
	a, err := NewSecret(r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSecret(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{a, b} {
		if !base64URL.MatchString(s) {
			t.Fatalf("secret %q is not 43 base64url characters", s)
		}
	}
	if a == b {
		t.Fatal("two secrets are equal")
	}
}

var errRead = errors.New("read failed")

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errRead }

func TestNewSecretReadFailure(t *testing.T) {
	tests := []struct {
		name string
		r    io.Reader
	}{
		{"error", errReader{}},
		{"short read", bytes.NewReader(make([]byte, 31))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewSecret(tt.r)
			if err == nil || s != "" {
				t.Fatalf("NewSecret = %q, %v; want empty and an error", s, err)
			}
		})
	}
}

func TestDigest(t *testing.T) {
	// SHA-256("abc") from FIPS 180-2, appendix B.1.
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	got := Digest("abc")
	if hex.EncodeToString(got[:]) != want {
		t.Fatalf("Digest(abc) = %x, want %s", got, want)
	}
	if Digest("abc") != sha256.Sum256([]byte("abc")) {
		t.Fatal("Digest is not SHA-256")
	}
}

func TestDigestEqual(t *testing.T) {
	a := Digest("x")
	b := Digest("x")
	c := a
	c[31] ^= 1
	tests := []struct {
		name string
		x, y []byte
		want bool
	}{
		{"equal", a[:], b[:], true},
		{"one bit different", a[:], c[:], false},
		{"different lengths", a[:], a[:31], false},
		{"both empty", nil, []byte{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DigestEqual(tt.x, tt.y); got != tt.want {
				t.Fatalf("DigestEqual = %v, want %v", got, tt.want)
			}
		})
	}
}
