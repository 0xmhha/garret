package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

var (
	// ErrParams is returned when hashing parameters are outside the
	// accepted bounds.
	ErrParams = errors.New("auth: password hash parameters out of bounds")
	// ErrHashFormat is returned when a stored hash is not a well-formed
	// Argon2id PHC string within the accepted bounds.
	ErrHashFormat = errors.New("auth: malformed password hash")
)

// Params are the Argon2id cost parameters. Memory is in KiB.
type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// Default is RFC 9106 section 4's second recommended option with a 128-bit
// salt and a 256-bit hash.
var Default = Params{Memory: 64 * 1024, Time: 3, Threads: 4, SaltLen: 16, KeyLen: 32}

// Bounds on parameters. The upper bounds stop a tampered stored hash from
// making the server allocate or compute without limit.
const (
	maxMemoryKiB     = 1024 * 1024
	maxTime          = 16
	minSaltLen       = 8
	maxSaltLen       = 64
	minKeyLen        = 16
	maxKeyLen        = 64
	minMemoryPerLane = 8 // Argon2 needs at least 8 KiB per lane.
	argon2Version    = argon2.Version
	phcAlgorithm     = "argon2id"
)

func (p Params) valid() bool {
	return p.Time >= 1 && p.Time <= maxTime &&
		p.Threads >= 1 &&
		p.Memory >= minMemoryPerLane*uint32(p.Threads) && p.Memory <= maxMemoryKiB &&
		p.SaltLen >= minSaltLen && p.SaltLen <= maxSaltLen &&
		p.KeyLen >= minKeyLen && p.KeyLen <= maxKeyLen
}

// HashPassword hashes password with Argon2id and a fresh salt read from rand,
// and returns the PHC string
// $argon2id$v=19$m=<memory>,t=<time>,p=<threads>$<salt>$<hash>.
// It returns ErrParams for parameters outside the accepted bounds.
func HashPassword(password string, p Params, rand io.Reader) (string, error) {
	if !p.valid() {
		return "", ErrParams
	}
	salt := make([]byte, p.SaltLen)
	if _, err := io.ReadFull(rand, salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		phcAlgorithm, argon2Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the PHC string encoded.
// rehash is true when it matches and the stored parameters differ from
// current, so the caller should store a new hash made with current. It
// returns ErrHashFormat when encoded is malformed or out of bounds.
func VerifyPassword(password, encoded string, current Params) (ok, rehash bool, err error) {
	stored, salt, key, err := parsePHC(encoded)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(password), salt, stored.Time, stored.Memory, stored.Threads, stored.KeyLen)
	if subtle.ConstantTimeCompare(got, key) != 1 {
		return false, false, nil
	}
	return true, stored != current, nil
}

func parsePHC(encoded string) (Params, []byte, []byte, error) {
	const fields = 6 // "", algorithm, version, parameters, salt, hash
	parts := strings.Split(encoded, "$")
	if len(parts) != fields || parts[0] != "" || parts[1] != phcAlgorithm ||
		parts[2] != "v="+strconv.Itoa(argon2Version) {
		return Params{}, nil, nil, ErrHashFormat
	}
	var p Params
	values, ok := parseCosts(parts[3])
	if !ok || values[2] > 255 {
		return Params{}, nil, nil, ErrHashFormat
	}
	// ParseUint with bitSize 32 bounds each value, and p is checked above.
	p.Memory, p.Time, p.Threads = uint32(values[0]), uint32(values[1]), uint8(values[2]) // #nosec G115
	salt, err1 := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	key, err2 := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return Params{}, nil, nil, ErrHashFormat
	}
	p.SaltLen, p.KeyLen = uint32(len(salt)), uint32(len(key)) // #nosec G115 -- bounded by valid below
	if len(salt) > maxSaltLen || len(key) > maxKeyLen || !p.valid() {
		return Params{}, nil, nil, ErrHashFormat
	}
	return p, salt, key, nil
}

// parseCosts parses "m=<n>,t=<n>,p=<n>" in that exact order. Each value is
// returned as uint32-sized; the caller narrows p.
func parseCosts(s string) ([3]uint64, bool) {
	var out [3]uint64
	names := [3]string{"m=", "t=", "p="}
	items := strings.Split(s, ",")
	if len(items) != len(names) {
		return out, false
	}
	for i, item := range items {
		digits, found := strings.CutPrefix(item, names[i])
		if !found || digits == "" || digits[0] < '0' || digits[0] > '9' {
			return out, false
		}
		v, err := strconv.ParseUint(digits, 10, 32)
		if err != nil {
			return out, false
		}
		out[i] = v
	}
	return out, true
}
