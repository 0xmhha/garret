package auth

import (
	"errors"
	"strings"
	"testing"
)

// fast keeps tests quick; Default is exercised by the benchmark and one test.
var fast = Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

func mustHash(t *testing.T, pw string, p Params) string {
	t.Helper()
	h, err := HashPassword(pw, p, seeded())
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return h
}

func TestHashPasswordFormat(t *testing.T) {
	h := mustHash(t, "correct horse battery", Default)
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("hash %q does not carry the default parameters", h)
	}
	ok, rehash, err := VerifyPassword("correct horse battery", h, Default)
	if err != nil || !ok || rehash {
		t.Fatalf("VerifyPassword = %v, %v, %v; want true, false, nil", ok, rehash, err)
	}
}

func TestVerifyPassword(t *testing.T) {
	older := fast
	older.Time = 2
	newer := fast
	newer.Time = 3
	oldHash := mustHash(t, "old password", older)
	hash := mustHash(t, "right password", fast)

	tests := []struct {
		name       string
		pw, hash   string
		current    Params
		wantOK     bool
		wantRehash bool
	}{
		{"right password", "right password", hash, fast, true, false},
		{"wrong password", "wrong password", hash, fast, false, false},
		{"empty password", "", hash, fast, false, false},
		{"old parameters", "old password", oldHash, newer, true, true},
		{"wrong password on old hash", "nope", oldHash, newer, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, rehash, err := VerifyPassword(tt.pw, tt.hash, tt.current)
			if err != nil {
				t.Fatal(err)
			}
			if ok != tt.wantOK || rehash != tt.wantRehash {
				t.Fatalf("got ok=%v rehash=%v, want ok=%v rehash=%v", ok, rehash, tt.wantOK, tt.wantRehash)
			}
		})
	}
}

func TestHashPasswordSaltsDiffer(t *testing.T) {
	r := seeded()
	a, err := HashPassword("same", fast, r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("same", fast, r)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two hashes of the same password are equal")
	}
	for _, h := range []string{a, b} {
		if ok, _, err := VerifyPassword("same", h, fast); err != nil || !ok {
			t.Fatalf("VerifyPassword(%q) = %v, %v", h, ok, err)
		}
	}
}

func TestHashPasswordErrors(t *testing.T) {
	tests := []struct {
		name string
		p    Params
		r    interface{ Read([]byte) (int, error) }
		want error
	}{
		{"failing reader", fast, errReader{}, errRead},
		{"zero time", Params{Memory: 64, Time: 0, Threads: 1, SaltLen: 16, KeyLen: 32}, seeded(), ErrParams},
		{"zero threads", Params{Memory: 64, Time: 1, Threads: 0, SaltLen: 16, KeyLen: 32}, seeded(), ErrParams},
		{"memory below 8 KiB per thread", Params{Memory: 7, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, seeded(), ErrParams},
		{"short salt", Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 7, KeyLen: 32}, seeded(), ErrParams},
		{"short key", Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 15}, seeded(), ErrParams},
		{"memory over bound", Params{Memory: maxMemoryKiB + 1, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, seeded(), ErrParams},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := HashPassword("pw", tt.p, tt.r)
			if !errors.Is(err, tt.want) || h != "" {
				t.Fatalf("HashPassword = %q, %v; want empty and %v", h, err, tt.want)
			}
		})
	}
}

func TestVerifyPasswordRejectsMalformed(t *testing.T) {
	good := mustHash(t, "pw", fast)
	parts := strings.Split(good, "$") // "", "argon2id", "v=19", "m=..", salt, hash
	salt, sum := parts[4], parts[5]
	tests := []string{
		"",
		"plain",
		"$argon2i$v=19$m=64,t=1,p=1$" + salt + "$" + sum,
		"$argon2id$v=16$m=64,t=1,p=1$" + salt + "$" + sum,
		"$argon2id$v=19$m=4194304,t=1,p=1$" + salt + "$" + sum,
		"$argon2id$v=19$m=64,t=17,p=1$" + salt + "$" + sum,
		"$argon2id$v=19$m=64,t=1,p=0$" + salt + "$" + sum,
		"$argon2id$v=19$m=64,t=1,p=256$" + salt + "$" + sum,
		"$argon2id$v=19$m=64,t=1$" + salt + "$" + sum,
		"$argon2id$v=19$t=1,m=64,p=1$" + salt + "$" + sum,
		"$argon2id$v=19$m=64,t=1,p=1$" + salt,
		"$argon2id$v=19$m=64,t=1,p=1$" + salt + "$" + sum + "$extra",
		"$argon2id$v=19$m=64,t=1,p=1$!!!$" + sum,
		"$argon2id$v=19$m=64,t=1,p=1$" + salt + "$" + sum + "=",
		"$argon2id$v=19$m=64,t=1,p=1$c2FsdA$" + sum,
		"$argon2id$v=19$m=64,t=1,p=1$" + salt + "$" + strings.Repeat("A", 200),
		"$argon2id$v=19$m=+64,t=1,p=1$" + salt + "$" + sum,
	}
	for _, h := range tests {
		t.Run(h, func(t *testing.T) {
			ok, rehash, err := VerifyPassword("pw", h, fast)
			if !errors.Is(err, ErrHashFormat) || ok || rehash {
				t.Fatalf("VerifyPassword = %v, %v, %v; want ErrHashFormat", ok, rehash, err)
			}
		})
	}
}

func BenchmarkHashPasswordDefault(b *testing.B) {
	r := seeded()
	for b.Loop() {
		if _, err := HashPassword("correct horse battery staple", Default, r); err != nil {
			b.Fatal(err)
		}
	}
}
