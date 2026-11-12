package auth

import (
	_ "embed"
	"fmt"
	"io"
	"math/big"
	"strings"
	"unicode"

	cryptorand "crypto/rand"
)

// codeWords is the number of words in an invitation code: six words from
// 7,772 give about 77.5 bits (transport.md, T-D3).
const codeWords = 6

//go:embed wordlist/words.txt
var wordlistFile string

// words is the invitation code wordlist (wordlist/README.md). It is read
// once at package initialization and never changed.
var words = strings.Fields(wordlistFile)

// NewInvitationCode returns six words drawn uniformly from the wordlist with
// rand, joined by "-". Production code passes crypto/rand.Reader.
func NewInvitationCode(rand io.Reader) (string, error) {
	n := big.NewInt(int64(len(words)))
	picked := make([]string, codeWords)
	for i := range picked {
		v, err := cryptorand.Int(rand, n)
		if err != nil {
			return "", fmt.Errorf("auth: read invitation code: %w", err)
		}
		picked[i] = words[v.Int64()]
	}
	return strings.Join(picked, "-"), nil
}

// NormalizeCode lowercases a typed invitation code and turns every run of
// spaces and hyphens into one "-", so "Marble Orbit" and "marble-orbit"
// match.
func NormalizeCode(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == '-' || unicode.IsSpace(r)
	}), "-")
}
