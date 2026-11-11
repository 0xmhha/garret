# Atomic functions specification

Status: approved. This spec defines the lowest layer of garret: pure
functions with no I/O, built first per the
[development process](../development-process.md#4-bottom-up-construction).
Components, services and transport build on them in later stages.

## 1. Scope

| Package | Functions |
|---|---|
| `internal/clock` | The clock interface every package receives |
| `internal/id` | ULID generator |
| `internal/netguard` | Private range checks, allowed-network resolution, peer address check |
| `internal/pki` | Serial numbers, fingerprints, certificate templates, signing, renewal timing |
| `internal/auth` | Secrets and their digests, password hashing, username and password rules |
| `internal/gitrepo` | Name validation, `git diff` output parsers |
| `internal/review` | Comment position porting |

Out of scope for this stage: anything that reads the network interfaces, the
file system, the database or a `git` process. Those are components (stage 3)
and take the outputs of these functions.

Every function here:

- does no I/O and has no side effects other than the ones stated;
- takes time and randomness as parameters, so tests are deterministic;
- validates its input and returns an error instead of panicking.

Because none of these functions has an entry point before stage 3, the
"reachable from the real entry point" rule of the
[definition of done](../development-process.md#8-definition-of-done-for-a-change)
is checked when stage 3 wires them in.

## 2. Clock (`internal/clock`)

```go
type Clock interface{ Now() time.Time }
type System struct{}
func (System) Now() time.Time
```

`System.Now` returns `time.Now()`. It is the only place in garret that calls
`time.Now`. Tests use a fake clock defined in the test files that need it.
Safe for concurrent use.

## 3. Identifiers (`internal/id`)

Implements the generator defined in
[data.md section 2.1](data.md#21-id-generator). Dependency:
`github.com/oklog/ulid/v2` v2.1.2 (released 2026-07-23).

```go
func NewGenerator(c clock.Clock, entropy io.Reader) (*Generator, error)
func (g *Generator) New() (string, error)
```

| Part | Definition |
|---|---|
| Input | `NewGenerator`: non-nil clock and entropy reader, otherwise `ErrNilDependency` |
| Output | `New`: a 26-character ULID string, or an error wrapping the entropy or overflow error |
| Logic | As in data.md 2.1 |
| Side effects | Advances the generator's last timestamp and monotonic state |
| Concurrency | Safe; a mutex guards generation |

| Case | Expected |
|---|---|
| Two calls in the same millisecond | Second ID sorts after the first |
| Clock moves backwards by 1 s | ID still sorts after the previous one |
| Clock moves forward | ID carries the new timestamp |
| Clock before the Unix epoch | ID timestamp 0 (the library would otherwise wrap around) |
| Entropy reader fails | Error, and the next call with working entropy still succeeds |
| Monotonic overflow (entropy all `0xff`, two calls in one ms) | Error on the second call |
| 1,000 calls from 8 goroutines | 1,000 distinct IDs, run with `-race` |
| Nil clock or entropy | `ErrNilDependency` |

## 4. Network boundary (`internal/netguard`)

Implements [security.md, Network boundary](../design/security.md#network-boundary).

### 4.1 `IsPrivate(p netip.Prefix) bool`

True when the whole prefix lies inside RFC 1918 (`10.0.0.0/8`,
`172.16.0.0/12`, `192.168.0.0/16`), `fc00::/7` or `fe80::/10`. An
IPv4-mapped IPv6 prefix is unmapped first. An invalid prefix is false.

| Case | Expected |
|---|---|
| `192.168.1.0/24`, `10.0.0.0/8`, `172.16.0.0/12`, `fd12::/64`, `fe80::/64` | true |
| `172.32.0.0/16`, `8.8.8.0/24`, `100.64.0.0/10` (CGNAT), `2001:db8::/32` | false |
| `192.168.0.0/15` (wider than the private block) | false |
| `::ffff:192.168.1.0/120` | true |
| `127.0.0.0/8`, zero `Prefix{}` | false |

### 4.2 `ResolveAllowed(explicit []netip.Prefix, candidates []Interface) ([]netip.Prefix, error)`

`Interface` is `{Name string; Up, Loopback bool; Prefixes []netip.Prefix}`,
filled by the stage 3 component that lists interfaces.

Logic, in order:

1. If `explicit` is not empty, use it as given (masked with `Masked()`), after
   removing duplicates.
2. Otherwise keep interfaces that are up and not loopback, and their IPv4
   prefixes that are private. Exactly one such prefix: use it. None:
   `ErrNoPrivateNetwork`. More than one: `ErrAmbiguousNetwork`, whose message
   lists the candidates sorted by interface name then prefix.
3. Every resulting prefix must pass `IsPrivate`, otherwise `ErrPublicNetwork`
   naming the prefix.

Output is sorted, so the result does not depend on input order. No side
effects.

| Case | Expected |
|---|---|
| One interface `en0` with `192.168.1.23/24` | `[192.168.1.0/24]` |
| `en0` private and `utun3` with a public address | `[192.168.1.0/24]` |
| Two interfaces with private IPv4 prefixes | `ErrAmbiguousNetwork` listing both |
| Only loopback, or only interfaces that are down | `ErrNoPrivateNetwork` |
| Only IPv6 private prefixes and no explicit list | `ErrNoPrivateNetwork` (IPv6 needs `--allow`) |
| Explicit `192.168.1.7/24` | `[192.168.1.0/24]` |
| Explicit list with a duplicate | Duplicate removed |
| Explicit `0.0.0.0/0` or `8.8.8.0/24` | `ErrPublicNetwork` |
| Same input in a different order | Same output |

### 4.3 `Allowed(set []netip.Prefix, peer netip.Addr) bool`

True when `peer` (unmapped from IPv4-mapped IPv6) is a loopback address or
lies inside one of the prefixes. An invalid address is false. The caller
passes the TCP peer address only; headers are never consulted.

| Case | Expected |
|---|---|
| Peer inside the set, peer outside | true, false |
| `127.0.0.1`, `::1` with an empty set | true |
| `::ffff:192.168.1.5` with `192.168.1.0/24` | true |
| Zero `Addr{}` | false |
| Network address and broadcast address of the prefix | true (the filter decides by range only) |

## 5. Certificates (`internal/pki`)

Implements [security.md, Transport security](../design/security.md#transport-security-and-device-certificates).
All keys are ECDSA P-256. Signing uses only the standard library.

Since Go 1.26, `ecdsa.GenerateKey` and the signing functions ignore the
random reader argument and always use a secure source. Tests that need
repeatable keys use `testing/cryptotest.SetGlobalRandom`. The functions below
still take a `rand io.Reader` where the standard library reads one (serials).

### 5.1 `NewSerial(rand io.Reader) (*big.Int, error)`

Reads 16 bytes and clears the top bit, so the serial is a positive 128-bit
integer under the 20-octet limit of RFC 5280 section 4.1.2.2. A zero result
is rejected and read again, at most 3 times, then `ErrSerial`. Read errors
are wrapped.

| Case | Expected |
|---|---|
| Normal reader | Positive integer, bit length ≤ 127 |
| Reader returns all zeros three times | `ErrSerial` |
| Reader returns zeros once, then data | Valid serial |
| Short read or read error | Wrapped error |

### 5.2 `Fingerprint(cert *x509.Certificate) string`

SHA-256 of `cert.Raw` as 64 lowercase hex characters. This is the value
printed at start and embedded in invitation links. A nil certificate is a
programming error and returns an empty string.

| Case | Expected |
|---|---|
| A known certificate | Matches `openssl x509 -fingerprint -sha256` with colons removed and lowercased |
| Same certificate twice | Same string |

### 5.3 Templates

```go
func CATemplate(serial *big.Int, now time.Time, names Names) (*x509.Certificate, error)
func ServerTemplate(serial *big.Int, now time.Time, names Names) (*x509.Certificate, error)
func ClientTemplate(serial *big.Int, now time.Time, userID string) (*x509.Certificate, error)
```

`Names` is `{Host string; Networks []netip.Prefix; Addresses []netip.Addr}`.

| Field | CA | Server | Client |
|---|---|---|---|
| Validity | `now − 5 min` to `now + 10 years` (Q1) | `now − 5 min` to `now + 7 days` | Same as server |
| Key usage | Cert sign, CRL sign | Digital signature | Digital signature |
| Extended key usage | none | Server auth | Client auth |
| Basic constraints | CA, path length 0 | not CA | not CA |
| Subject | `CN=garret CA` | `CN=<host>` | `CN=<userID>` |
| SANs | none | `<host>`, `localhost`, each address, `127.0.0.1`, `::1` | none |
| Name constraints | Critical. Permitted DNS: `<host>`, `localhost`. Permitted IP ranges: each network, `127.0.0.0/8`, `::1/128` | none | none |

The CA permits whole networks rather than single addresses. A DHCP address
change inside the same network then keeps the CA valid, and only the server
certificate is reissued. Client certificates carry no SANs, so the name
constraints do not apply to them; the device is identified by its serial,
which the server looks up in `client_certificates`.

Errors: empty host, host that is not a valid DNS name, no networks, a
network that fails `IsPrivate`, an empty user ID, or a nil serial.

| Case | Expected |
|---|---|
| Valid names | Fields as in the table |
| `NotBefore` and `NotAfter` | Exactly `now − 5 min` and the stated end |
| Public network in `Names` | Error |
| Host `bad host` or empty | Error |
| A server certificate signed by the CA verifies for `<host>` and each address | Verifies |
| A certificate for `evil.example` signed by the same CA key | Verification fails on the name constraint |

### 5.4 `Sign(template, parent *x509.Certificate, pub crypto.PublicKey, signer crypto.Signer, rand io.Reader) (*x509.Certificate, error)`

Calls `x509.CreateCertificate` and parses the result. Rejects a public key
that is not ECDSA P-256 with `ErrKeyType`.

### 5.5 `CheckCSR(der []byte) (*x509.CertificateRequest, error)`

Parses the request, checks its signature, and accepts only an ECDSA P-256
key. Subject and extensions in the request are ignored; the server fills
them from the template. Input over 4 KiB is rejected before parsing.

| Case | Expected |
|---|---|
| Valid P-256 request | Parsed request |
| RSA or P-384 key | `ErrKeyType` |
| Tampered signature | Error |
| Empty, garbage, over 4 KiB | Error |

### 5.6 `RenewalDue(notBefore, notAfter, now time.Time) bool`

True when less than half of the validity remains:
`now ≥ notBefore + (notAfter − notBefore) / 2`. Used for both server
rotation and the client renewal job.

| Case | Expected |
|---|---|
| 7-day certificate, 3 days in | false |
| Exactly 3.5 days in | true |
| After `notAfter` | true |
| `now` before `notBefore` | false |
| `notAfter` not after `notBefore` | true (an invalid certificate is always due) |

## 6. Credentials (`internal/auth`)

Implements [security.md, Authentication](../design/security.md#authentication).

### 6.1 Secrets

```go
func NewSecret(rand io.Reader) (string, error)
func Digest(secret string) [32]byte
func DigestEqual(a, b []byte) bool
```

- `NewSecret` reads 32 bytes (256 bits) and encodes them as unpadded
  base64url, 43 characters. Used for invitation tokens, session IDs, CSRF
  tokens, access tokens and the setup token. Read errors are wrapped.
- `Digest` is SHA-256 of the secret's bytes. Only digests are stored.
- `DigestEqual` compares with `crypto/subtle.ConstantTimeCompare`. Different
  lengths return false.

| Case | Expected |
|---|---|
| Two calls | Different 43-character strings of the base64url alphabet |
| Failing reader | Error, no secret |
| `Digest` of a known string | Matches the SHA-256 test vector |
| Equal digests, one byte different, different lengths, both empty | true, false, false, true |

### 6.2 Password hashing

Decision D1 of this stage: Argon2id with RFC 9106's second recommended
option. Dependency: `golang.org/x/crypto` v0.58.0 (released 2026-10-09).

```go
type Params struct{ Memory, Time uint32; Threads uint8; SaltLen, KeyLen uint32 }
var Default = Params{Memory: 64 * 1024, Time: 3, Threads: 4, SaltLen: 16, KeyLen: 32}

func HashPassword(password string, p Params, rand io.Reader) (string, error)
func VerifyPassword(password, encoded string, current Params) (ok, rehash bool, err error)
```

The encoded form is the PHC string
`$argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>`, salt and hash in unpadded
standard base64.

`VerifyPassword` logic:

1. Parse the PHC string. Reject any algorithm other than `argon2id`, any
   version other than 19, and parameters outside safe bounds (memory over
   1 GiB, time over 16, threads 0, salt under 8 bytes, hash under 16 bytes)
   with `ErrHashFormat`. The bounds stop a tampered row from making the
   server allocate unbounded memory.
2. Recompute with the stored parameters and compare in constant time.
3. `rehash` is true when `ok` and the stored parameters differ from
   `current`. The caller then stores a new hash. This is how parameters are
   raised later (OWASP Password Storage Cheat Sheet, "Upgrading the Work
   Factor").

| Case | Expected |
|---|---|
| Hash then verify the same password | `ok`, no rehash |
| Wrong password | not `ok`, no error |
| Hash stored with `t=2`, current `t=3` | `ok`, `rehash` |
| Wrong password on an old hash | not `ok`, no rehash |
| `$argon2i$...`, `v=16`, `m=4194304`, missing fields, bad base64 | `ErrHashFormat` |
| Failing salt reader | Error, no hash |
| Two hashes of the same password | Different salts, both verify |
| Benchmark `HashPassword` with `Default` | Under 1 s on the reference Mac (OWASP: under one second) |

### 6.3 Account rules

- `ValidUsername(s string) error`: matches `^[a-z0-9][a-z0-9_-]{1,31}$`
  (data.md, `users.username`).
- `ValidPassword(s string) error`: at least 15 characters counted as Unicode
  code points, at most 1,024 bytes, valid UTF-8 (Q3).
  NIST SP 800-63B-4 section 3.1.1.2 requires 15 characters for a password
  used as a single factor and recommends allowing at least 64. garret can run
  with client certificates off, so the password may be the single factor.
  The upper bound limits hashing work per request.

| Case | Expected |
|---|---|
| `ab`, `a`, 33 characters, uppercase, leading `-`, `한글` | Username rejected |
| `ab`, `a-b_c`, 32 characters | Username accepted |
| 14 and 15 code points, 15 Hangul syllables (45 bytes) | Rejected, accepted, accepted |
| 1,024 and 1,025 bytes, invalid UTF-8 | Accepted, rejected, rejected |

## 7. Git input (`internal/gitrepo`)

Decision D2 of this stage: the diff parser is written in garret, not taken
from a library. Paths come from `-z` output, where Git prints them verbatim
with NUL separators (`man git-diff`, `-z`), so the parser never unquotes a
path.

### 7.1 Names

- `ValidProjectName(s string) error`: `^[a-z0-9][a-z0-9._-]{0,63}$`, and not
  ending in `.git` or `.lock`, and no `..`.
- `ValidBranchRef(s string) error`: starts with `refs/heads/`, then follows
  the rules of `git check-ref-format`: no component starting with `.` or
  ending with `.lock`, no `..`, no ASCII control characters, space, `~`, `^`,
  `:`, `?`, `*`, `[`, `\`, no `@{`, not `@` alone, no trailing `/` or `.`, no
  `//`, at most 255 bytes.

The test table for `ValidBranchRef` is built from the examples in
`man git-check-ref-format`, and every case is cross-checked against the real
`git check-ref-format` in a test that is skipped when `git` is not installed.

### 7.2 `ParseRaw(out []byte) ([]FileChange, error)`

Parses `git diff --raw -z -M <from> <to>`:
`:<old mode> <new mode> <old sha> <new sha> <status>\0<path>\0[<new path>\0]`.

`FileChange` is `{Status Status; OldPath, NewPath string; OldMode, NewMode
string; Similarity int}` with `Status` one of added, modified, deleted,
renamed, type-changed. Copy (`C`) does not occur without `-C` and is
rejected; unknown statuses return `ErrRawFormat` with the byte offset.

| Case | Expected |
|---|---|
| Empty output | Empty list |
| One modification, one addition, one deletion | Three entries with the right statuses and paths |
| Rename `R087` | `Similarity` 87, both paths |
| Path with spaces, newline, quotes, non-UTF-8 bytes | Path kept byte for byte |
| Mode change only (`100644` to `100755`) | Modified, both modes |
| Symlink replaced by a file (`T`) | Type-changed |
| Truncated record, missing NUL, bad mode, status `X` | `ErrRawFormat` |
| Fuzz | Never panics; every accepted input round-trips through a formatter |

### 7.3 `ParseNumstat(out []byte) ([]LineCount, error)`

Parses `git diff --numstat -z -M`. A binary file is reported by Git as
`-\t-`; it becomes `Binary: true`. Renames use the `\0old\0new\0` form.
Same error rules as `ParseRaw`.

### 7.4 `ParseHunks(patch []byte, limits Limits) ([]Hunk, error)`

Parses the patch of one file from `git diff --no-color --no-ext-diff
-U<n> <from> <to> -- <path>`. The caller asks for one file at a time, which
matches the diff cache key `(from_sha, to_sha, path)` in
[data.md section 7.3](data.md#73-bounded-external-work). Header lines before
the first `@@` are skipped, so path quoting in headers never matters.

```go
type Hunk struct {
    OldStart, OldLines, NewStart, NewLines int
    Section string           // text after the closing @@, display only
    Lines   []Line
}
type Line struct {
    Kind    LineKind         // context, added, deleted
    Text    []byte           // without the leading marker and newline
    OldNo, NewNo int         // 0 when the side has no such line
    NoNewlineAtEOF bool
}
```

Rules:

- A hunk header is `@@ -a[,b] +c[,d] @@[ section]`. An omitted count is 1.
- The number of context and deleted lines must equal the old count, and
  context and added lines the new count. A mismatch is `ErrPatchFormat`.
- `\ No newline at end of file` marks the previous line.
- Hunks must not overlap and must be in increasing order.
- `Limits{MaxBytes, MaxLines}` (2 MiB and 20,000 lines,
  Q5): over either limit returns `ErrTooLarge`, so the API reports the file
  as too large instead of sending it.

| Case | Expected |
|---|---|
| Empty patch (no change) | No hunks |
| One-line change with context | One hunk, line numbers on both sides correct |
| Header `@@ -0,0 +1,3 @@` (new file) | Three added lines numbered 1–3 |
| Header `@@ -1,3 +0,0 @@` (deleted file) | Three deleted lines |
| Omitted counts `@@ -5 +5 @@` | Count 1 |
| Last line without newline on one side | `NoNewlineAtEOF` on that line only |
| Line containing `@@` or starting with `\` inside text | Treated as content, by its marker |
| CRLF content | `\r` kept in `Text` |
| Count mismatch, negative number, overlapping hunks, unknown marker | `ErrPatchFormat` |
| One byte over `MaxBytes`, one line over `MaxLines` | `ErrTooLarge` |
| Fuzz | Never panics; for accepted input, line counts match the headers |

## 8. Comment porting (`internal/review`)

Implements [data-model.md, Comment porting](../design/data-model.md#comment-porting).

### 8.1 `ChangeBlocks(hunks []gitrepo.Hunk) []Block`

Reduces hunks to blocks of consecutive changed lines,
`Block{OldStart, OldLines, NewStart, NewLines}`, dropping context lines. A
pure insertion has `OldLines = 0` and `OldStart` equal to the old line after
which the lines were inserted; a pure deletion has `NewLines = 0`. Porting
uses blocks, so the result does not depend on how much context the diff was
asked for.

### 8.2 `Port(blocks []Block, start, end int) (Position, error)`

Maps the line range `start..end` (1-based, inclusive) on the old side of a
diff to the new side.

Logic: walk the blocks in order with `shift = 0`.

1. If the range ends before the block (`end < OldStart`, or for a pure
   insertion `end ≤ OldStart`), stop.
2. If the range starts after the block's last old line, add
   `NewLines − OldLines` to `shift` and continue.
3. Otherwise the range touches changed lines: return
   `Position{FileLevel: true}`.

After the walk, return `Position{Start: start + shift, End: end + shift}`.
The caller ports file-level comments (no lines) without calling `Port`, and
decides which diff to use (old head to new head for new-side comments).

Errors: `start < 1`, `end < start`, or blocks out of order.

| Case | Expected |
|---|---|
| No blocks | Same range |
| Block entirely below the range | Same range |
| 3 lines inserted above | Range moved down by 3 |
| 2 lines deleted above | Range moved up by 2 |
| Insertion right after the last commented line | Same range |
| Insertion right after the line before the range | Moved down |
| Range overlaps a modified line, its first line, its last line | File-level |
| Range inside a deleted block | File-level |
| Several blocks above with mixed effects | Sum of the effects |
| `start = 0`, `end < start` | Error |
| Property test: random edits that do not touch the range | Ported text equals original text |

## 9. Dependencies added in this stage

| Module | Version | Released | Maintainer | Runtime modules added | Why |
|---|---|---|---|---|---|
| `github.com/oklog/ulid/v2` | v2.1.2 | 2026-07-23 | Organization | 1 | data.md 2.1 |
| `golang.org/x/crypto` | v0.58.0 | 2026-10-09 | Go team | 1 (`argon2` imports `x/sys/cpu`, counted at implementation) | D1 |

Both are past the 7-day cooldown. The exact `go list -deps` count is recorded
in each implementation pull request.

## 10. Delivery and done criteria

Decision D3 of this stage. This spec is reviewed and merged first. Then one
pull request per package implements it, with commits ordered spec change (if
any), failing tests, implementation.

Stage 2 is done when:

- every case in this spec passes with `-race`, and `make check` and the
  required macOS CI job pass;
- the fuzz targets for `ParseRaw`, `ParseNumstat`, `ParseHunks` and `Port`
  are committed with seed corpora and each has run for 60 seconds locally
  without failure before its pull request;
- the `HashPassword` benchmark with `Default` runs under one second on the
  reference Mac.

## 11. Settled values

These were open questions in the draft and were accepted as proposed during
review.

| # | Question | Value |
|---|---|---|
| Q1 | CA validity | 10 years. Rotating the CA forces every device to re-enroll |
| Q2 | Certificate back-dating | 5 minutes, for clock differences between machines |
| Q3 | Password length | 15 code points minimum, 1,024 bytes maximum |
| Q4 | Accepted client key types | ECDSA P-256 only; `garret enroll` generates the key |
| Q5 | Diff size limits per file | 2 MiB and 20,000 lines |

Changing any of them changes constants, not the design.
