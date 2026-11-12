# Components specification

Status: approved. Components combine the atomic functions of
[atomic.md](atomic.md) with exactly one outside dependency each: the file
system, SQLite, the `git` binary, the network interfaces or the clock-driven
certificate rotation. Services (stage 4) coordinate them.

## 1. Scope

| Package | Component | Dependency |
|---|---|---|
| `internal/datadir` | Data directory layout and permission checks | File system |
| `internal/store` | Connections, transactions, per-key locks | SQLite |
| `internal/gitrepo` | `Runner`: every `git` invocation | `git` binary |
| `internal/netguard` | Interface listing and the connection filter | Network |
| `internal/pki` | CA files, server certificate rotation, revocation set | File system, clock |
| `internal/attachment` | Content-addressed file store | File system |

SQL queries are written with the services that need them (stage 4), so each
query has a caller and a test from the start.

Requirements carried from the stage 2 security reviews:

- the TLS server uses `tls.RequireAndVerifyClientCert` with only garret's CA
  in `ClientCAs` (stage 5);
- certificates are built only from `ServerTemplate` and `ClientTemplate`, and
  only `PublicKey` is taken from a certificate request;
- `crypto/rand.Reader` is the random source in production for IDs, secrets,
  serials, salts and signatures;
- callers of `VerifyPassword` reject on any error and on `ok == false`, and
  run `ValidPassword` before `HashPassword`.

## 2. Data directory (`internal/datadir`)

```
<data>/              0700
  garret.db          0600  (plus -wal and -shm)
  repos/<name>.git   0700
  attachments/<project-id>/<sha256-hex>  0600
  tls/ca.key         0600  PEM, PKCS #8
  tls/ca.crt         0600
  tls/server.key     0600
  tls/server.crt     0600
```

### `Open(path string) (*Dir, error)`

1. Make `path` absolute and clean.
2. Create it with `0700` if missing, then create the subdirectories with
   `0700`.
3. Refuse to continue (`ErrPermissions`) if the directory or any
   subdirectory grants any permission to group or others, or is owned by
   another user. garret never relaxes or tightens an existing directory's
   mode on its own; the error message names the path and the `chmod` to run.
4. Refuse a path that is a symlink (`ErrSymlink`), so the data cannot be
   redirected.

`Dir` returns the paths above and `WriteFile(rel string, data []byte)`,
which writes through a temporary file in the same directory, `fsync`s it,
renames it into place with mode `0600`, and `fsync`s the directory.

| Case | Expected |
|---|---|
| Missing directory | Created with the layout and modes above |
| Existing directory with mode `0755` | `ErrPermissions` naming it |
| Subdirectory `repos` with mode `0750` | `ErrPermissions` |
| Path is a symlink | `ErrSymlink` |
| `WriteFile` | File has mode `0600`; no temporary file left behind |
| `WriteFile` with a path that escapes the directory (`../x`, absolute) | Error, nothing written |
| Disk full or rename failure during `WriteFile` | Error, the previous file content unchanged |

## 3. Store (`internal/store`)

### 3.1 `Open(ctx, path string) (*DB, error)`

Opens two `*sql.DB` handles on the same file, as defined in
[data.md section 7.1](data.md#71-database-connections):

- **writer**: `SetMaxOpenConns(1)`, pragmas `foreign_keys=ON`,
  `journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`,
  `secure_delete=ON`, `_txlock=immediate`;
- **reader**: `mode=ro`, `query_only=ON`, the same pragmas otherwise,
  `max(2, min(GOMAXPROCS, 8))` connections.

It runs `Migrate` on the writer before returning, so no caller ever sees an
unmigrated database. `Close` closes the reader, then the writer.

### 3.2 `(*DB).WriteTx(ctx, fn func(*sql.Tx) error) error`

Runs `fn` in one `BEGIN IMMEDIATE` transaction on the writer under
`context.WithTimeout(ctx, WriteTimeout)` (5 s, data.md Q3). Commits when `fn`
returns nil, rolls back otherwise, and returns `fn`'s error unchanged so
callers can match it. A panic in `fn` rolls back and re-panics.

`ReadTx` is the same on the reader without the write lock.

| Case | Expected |
|---|---|
| `fn` succeeds | Rows visible to a later `ReadTx` |
| `fn` returns an error | Nothing written; same error returned |
| `fn` panics | Rolled back, panic propagates, the writer stays usable |
| Context cancelled while waiting for the writer | Context error, `fn` not called |
| `fn` runs longer than the timeout | Rolled back with a deadline error |
| 50 concurrent `WriteTx` incrementing one counter | Final value 50, no `SQLITE_BUSY`, `-race` clean |
| A write attempted through `ReadTx` | Error (`query_only`) |

### 3.3 `KeyedMutex` and `ProjectLocks`

`KeyedMutex.Lock(ctx, key) (unlock func(), error)` serializes callers per
key (data.md 7.2). Entries are reference-counted and removed when the last
holder unlocks, so memory does not grow with the number of keys ever seen.
Waiting respects `ctx`.

`ProjectLocks` gives a reader–writer lock per project: `Shared(ctx, id)` for
ordinary writes and `Exclusive(ctx, id)` for purge.

`ProjectLocks` is built on `golang.org/x/sync/semaphore.Weighted` (Go team,
already pinned at v0.24.0 through the build tools): shared takes weight 1 and
exclusive takes the full weight. The semaphore serves waiters first in,
first out, so a waiting purge blocks later writers instead of starving. A
hand-written context-aware reader–writer lock was rejected as more code to
verify for the same behavior. Entries are kept for the life of the server,
since projects are few.

| Case | Expected |
|---|---|
| Two callers, same key | Second waits until the first unlocks |
| Two callers, different keys | Both proceed |
| Context cancelled while waiting | Error, the lock is not taken, no entry leaks |
| 1,000 keys locked and unlocked | Internal map empty afterwards |
| Exclusive waits for shared holders, and blocks new shared ones | Yes |
| Unlock called twice | Second call is a no-op |

## 4. Git runner (`internal/gitrepo`)

### 4.1 `NewRunner(gitPath string, limit int, timeout time.Duration) (*Runner, error)`

Checks that `gitPath` runs and reports version 2.38 or later, which covers
the Git shipped with current macOS (2.50 here) and Debian 12 (2.39), and
every feature garret uses (`uploadpack.hideRefs`, `-z` output, `merge-base`).
`limit` is the semaphore size,
`max(1, min(GOMAXPROCS-1, 4))` in production (data.md 7.3).

Every command:

- runs without a shell, arguments as a list, with `--` before any path;
- gets an environment of only `PATH` (the directory of `gitPath` followed by
  `/usr/bin:/bin:/usr/sbin:/sbin`, not the server's own `PATH`), `HOME` (set
  to the data directory), `LANG=C`, `LC_ALL=C`, `GIT_CONFIG_NOSYSTEM=1`,
  `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_TERMINAL_PROMPT=0`;
- acquires the semaphore with the caller's context, then runs under
  `context.WithTimeout(ctx, timeout)`;
- caps captured output (64 MiB, Q4) and returns `ErrTooLarge`
  beyond it;
- returns an error that includes the subcommand and Git's stderr (trimmed to
  4 KiB), never the full argument list, since paths can be sensitive.

### 4.2 Operations

| Method | Command |
|---|---|
| `InitBare(ctx, dir)` | `git init --bare`, then the config in [architecture.md](../design/architecture.md#serving-the-protocol) |
| `Refs(ctx, dir)` | `git for-each-ref --format=%(objectname) %(refname) refs/heads` |
| `UpdateRef(ctx, dir, ref, new, old)` | `git update-ref ref new old` (compare and swap) |
| `MergeBase(ctx, dir, a, b)` | `git merge-base a b` |
| `RevList(ctx, dir, base, head, max)` | `git log --format=...%x00... base..head`, at most `max+1` to detect overflow |
| `DiffSummary(ctx, dir, from, to)` | `--raw -z -M --no-abbrev` and `--numstat -z -M`, parsed and joined by path |
| `DiffFile(ctx, dir, from, to, oldPath, newPath)` | `--no-color --no-ext-diff -U3 -M from to -- oldPath newPath`, parsed with `ParseHunks` |
| `FileAt(ctx, dir, commit, path, max)` | `git cat-file blob commit:path`, refused over `max` |
| `Bundle(ctx, dir, out)` | `git bundle create out --all` (export) |

Commit names passed in are checked to be 40 or 64 lowercase hex characters
before use; refs with `ValidBranchRef` or the `refs/garret/` prefix.
`UpdateRef` writes only under `refs/garret/`. The version check at
construction always uses the default timeout and output cap, so options meant
for commands cannot make it fail.

Tests run against a real `git` in a temporary directory and are skipped
when `git` is missing. Cases: each operation on a fresh repository; a
renamed file through `DiffFile`; a path with a newline; a timeout on a
blocked command (a `git` wrapper script that sleeps); a cancelled context
while waiting on the semaphore; output over the cap; a bad commit name
rejected without running `git`.

## 5. Network (`internal/netguard`)

- `Interfaces() ([]Interface, error)` lists `net.Interfaces()` with their
  addresses, converted to the atomic `Interface` type.
- `Listener(inner net.Listener, allowed []netip.Prefix, log *slog.Logger)
  net.Listener` wraps `Accept`: a connection whose TCP peer is not
  `Allowed` is closed immediately and logged with its address, and `Accept`
  continues with the next one, so a rejected peer never reaches TLS.

| Case | Expected |
|---|---|
| Peer inside the set | Connection returned |
| Peer outside | Closed, logged, next connection returned |
| Inner listener closed | `Accept` returns the inner error |
| Non-TCP address | Closed as not allowed |

## 6. Certificates (`internal/pki`)

### 6.1 `LoadOrCreateCA(dir *datadir.Dir, clock, names) (*CA, error)`

Reads `tls/ca.key` and `tls/ca.crt` if both exist; creates both if neither
exists; refuses if only one exists (`ErrCAIncomplete`). A loaded CA whose
name constraints no longer cover the current `names` (for example the server
moved to another network) is refused with `ErrCAMismatch` and a message that
says re-enrollment is required. The key is ECDSA P-256 in PKCS #8 PEM. A
key that does not match the certificate, or a file that does not parse, is
an error; garret never replaces a broken CA on its own.

### 6.2 `ServerCertificates`

Holds the current server certificate behind an `atomic.Pointer` and serves
it through `GetCertificate`. At start it reuses the stored certificate only
if it verifies against the CA for the host and every listen address and is
not yet due; otherwise it issues a new one. The key is written before the
certificate, so a crash between the two leaves a pair that fails to load
and is reissued. `Run(ctx)` checks every hour (Q5)
with `RenewalDue` and issues a new one when due, writes it with
`WriteFile`, then swaps the pointer. It returns when `ctx` is done. Issuing
fails safe: on error the old certificate stays in place and the error is
logged; the next check retries.

### 6.3 `ActiveSerials`

The set of active client certificate serials (data.md 7.4) as an immutable
map behind an `atomic.Pointer`. `Replace(serials)` publishes a new map;
`Active(serial) bool` reads without locking. Serials are written by
`SerialString`: lowercase hex without leading zeros, the form stored in
`client_certificates.serial`. Services call `Replace` after
the commit that issued or revoked a certificate.

## 7. Attachments (`internal/attachment`)

`Store.Put(projectID, r io.Reader, max int64) (sha256 [32]byte, size int64,
mediaType string, err error)` streams into a temporary file while hashing,
refuses input over `max` (25 MiB, api.md) with `ErrTooLarge`, detects the
media type from the first 512 bytes with `http.DetectContentType`, and
renames the file to `attachments/<project-id>/<sha256-hex>`. Storing the same
content twice keeps one file. `Open(projectID, sha256)` opens it for reading.
`RemoveProject(projectID)` removes the project's directory (purge).

The project ID must be a ULID in canonical uppercase form before it becomes
a path segment. A read error or an oversized upload removes the temporary
file, so nothing partial is ever visible. `Inline(mediaType)` reports whether
a type may be shown in the page (Q8).

## 8. Settled values

These were open questions in the draft and were accepted as proposed during
review.

| # | Question | Value |
|---|---|---|
| Q1 | Default data directory | `~/Library/Application Support/garret` on macOS, `$XDG_DATA_HOME/garret` (or `~/.local/share/garret`) on Linux; `--data` overrides |
| Q2 | Minimum Git version | 2.38 |
| Q3 | Timeout for one `git` command | 60 s for fetch, push and bundle; 10 s for every other command |
| Q4 | Output cap for one `git` command | 64 MiB |
| Q5 | Server certificate check interval | 1 hour |
| Q6 | Push size limit | 500 MiB per push |
| Q7 | Repository size limit | None in the first release; the size is shown to administrators |
| Q8 | Attachment types shown inline | PNG, JPEG, GIF and WebP images; everything else downloads |

The proposed defaults already in [data.md section 9](data.md#9-open-questions)
(500 commits per revision, 90-day notification retention, 5 s write
transactions, 10 s shutdown, 1 MiB per collaborative update) are used as
written.
