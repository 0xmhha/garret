# Design decisions

Each entry records what was decided, why, and which alternatives were
rejected. Changing a decision means updating this file first.

## D1. The server is a Git remote

Members add garret as a Git remote and `git push` their branches over HTTP
(smart HTTP). Fixes after review are pushed the same way.

- **Why**: several people must be able to upload changes, and every upload
  must be recorded. A remote that accepts pushes does both.
- **Rejected**: reading a local repository path on the host (only the host
  owner could update code); uploading patch files or bundles (manual and hard
  to track per branch).

## D2. Invitation-based accounts and personal tokens

The first start creates an administrator. The administrator issues single-use
invitation links. An invited person chooses a username and password. Git
pushes authenticate with a personal access token.

- **Why**: garret holds undisclosed vulnerability fixes, so being on the
  subnet is not enough to prove identity. Invitations avoid the administrator
  handling other people's passwords.
- **Rejected**: name-only identity (anyone on the network can impersonate
  anyone); administrator-created accounts (works, but the administrator must
  hand out passwords).

## D3. Subnet detection with explicit override

By default garret derives the allowed network from the host's interface
address (for example `192.168.0.12/24` allows `192.168.0.0/24`). An explicit
`--allow <CIDR>` takes precedence. If more than one candidate interface
exists, garret refuses to start and asks for `--allow`. Only the TCP peer
address is trusted; forwarding headers are ignored. Public address ranges are
refused at startup.

- **Why**: zero configuration in the common case, but a wrong boundary leaks
  undisclosed fixes, so ambiguity stops the server instead of guessing.
- **Rejected**: detection only (silently picks the wrong interface);
  explicit configuration only (setup friction).

## D4. Branch-based reviews with revisions and per-commit reading

One branch is one review. Every push that moves the branch head creates a new
revision. Earlier revisions are preserved under hidden refs even after a force
push. Reviewers can read and comment on each commit, a range of commits, or
the whole revision. A verdict (approve or request changes) applies to the
review as a whole.

- **Why**: the team splits a fix into reviewable commits and reviews them one
  by one, while keeping the familiar branch workflow. Preserving revisions
  keeps the history of the fix and the context of old comments.
- **Rejected**: Gerrit-style one-commit-per-change (requires `Change-Id` hooks
  and an amend-based workflow); branch reviews without revisions (force pushes
  erase history); per-commit verdicts (commit identity across rebases needs
  `git patch-id` matching; can be added later).

## D5. First-release collaboration features

Included: line comments with threaded replies and resolution, review-level
comments, verdicts and review states, revision-to-revision diffs, comment
porting to new revisions, live updates, an in-app inbox with mentions, file
attachments, standalone discussion posts, and simultaneous multi-author
editing.

Deferred: suggested code changes, server-side merge, search.
Excluded: real-time chat (threads plus live updates cover it, and chat
scatters the record).

## D6. Collaborative editing scope

Discussion posts and review descriptions are edited collaboratively in real
time. Comments have a single author.

- **Why**: a comment is an opinion that must stay attributable. Plans and
  analysis notes are where several people write at once.
- **Rejected**: collaborative editing everywhere (blurs who said what);
  no collaborative editing (last-writer conflicts only).

## D7. Yjs in the browser, ygo on the server

The browser uses [Yjs](https://github.com/yjs/yjs). The server holds the same
documents with [ygo](https://github.com/reearth/ygo), a pure-Go,
Yjs-compatible implementation. The server relays updates, stores every
received update together with its author, and extracts the current text for
history, export and mention detection.

- **Why**: a proven CRDT on the client, and a server that can read documents
  itself instead of trusting client-provided text snapshots.
- **Rejected**: a custom RGA implementation on the server (high verification
  cost for a secondary feature); client-sent text snapshots (the server could
  not verify them); running `ywasm` under a Go WebAssembly runtime (no
  evidence it works, and slower).

## D8. Edit history, soft delete and immutable verdicts

Edits keep previous versions. Deletion hides content but keeps it. Verdicts
and state changes are append-only; changing a verdict means recording a new
one. Administrators can permanently erase a single item (the fact of the
erasure is kept) and can export and erase a whole project.

- **Why**: the record must be traceable but correctable, and sensitive data
  must be removable once the fix is public.
- **Rejected**: fully immutable records (no way to remove a leaked secret);
  free editing and hard deletes (no trail).

## D9. Embedded SQLite

Data lives in SQLite through the pure-Go driver `modernc.org/sqlite` in WAL
mode. Storage access sits behind repository interfaces.

- **Why**: nothing to install, enough for a handful of concurrent writers,
  and cross-compilation without cgo. The interfaces keep a path to PostgreSQL.
- **Rejected**: PostgreSQL (separate server to run); embedded key-value stores
  (history queries would be hand-written).

## D10. React frontend embedded in the binary

The web UI uses React, TypeScript and Vite. The build output is embedded with
`go:embed`, so garret ships as one executable.

- **Why**: the widest choice of diff, editor (CodeMirror 6) and Yjs
  integrations, and the most familiar stack for contributors.
- **Rejected**: Svelte (smaller, but fewer ready components); server-rendered
  templates with htmx (collaborative editing and rich diffs need a client
  application anyway).

Libraries and build (decided at the start of stage 6):

| Concern | Choice | Rejected |
|---|---|---|
| Routing | react-router | TanStack Router (ten packages), wouter (single maintainer), a hand-written router |
| Server data and cache | TanStack Query | SWR, hand-written hooks |
| Interface strings (English and Korean) | A small module of our own: one JSON catalog per language and the browser's `Intl` APIs; keys are checked by TypeScript and a test fails on any missing translation | i18next, react-intl, Lingui |
| Markdown | marked with raw HTML off, then DOMPurify with an allow-list; one function does both and is the only place that writes HTML | react-markdown (unified ecosystem), markdown-it, a hand-written renderer |
| Interactive components (menus, tabs, combo boxes, tooltips) | React Aria Components; native `<dialog>` where it is enough | Base UI, Radix, hand-written ARIA patterns |
| Embedding | A placeholder page is committed and embedded by default; `make build` and release builds run Vite and embed the real build with the `webui` build tag | committing the build output (unreviewable, grows the repository), requiring Node for every Go build |

- **Why**: preventing incidents comes before dependency count where a
  mistake is an incident: script injection through comments, and screens
  that cannot be used with a keyboard or a screen reader. There garret
  uses proven libraries. Everywhere else it keeps dependencies minimal.
- A binary built without the web interface says so at startup and in
  `garret version`, release builds refuse the placeholder, and CI tests the
  real build.

## D11. HTTPS by default, device certificates on by default

garret runs a private certificate authority. The server certificate is valid
for 7 days and rotates automatically; members trust the CA once, through the
fingerprint embedded in their invitation link. Every device also needs a
client certificate (mutual TLS), valid for 7 days:

- a device gets its first certificate only through an invitation link;
- a device renews only by presenting its current, valid certificate;
- a background job on the device renews automatically;
- renewal follows one line per device: the server renews only once less
  than half of the validity remains, only the newest certificate of the
  line, and revoking any certificate revokes the whole line.

`--client-cert=off` disables mutual TLS for access points the team fully
controls, with a warning at every start. Supported browsers are the latest
Chrome and Safari. Plain HTTP requires an explicit `--insecure-http`. Details
are in [security.md](security.md#transport-security-and-device-certificates).

- **Why**: the main deployment is a shared office access point where people
  outside the fix are connected. With login alone they could still reach the
  login page and learn that a private review server exists. With mutual TLS
  their handshake fails. Short-lived certificates limit the damage of a
  leaked key, and the CA keeps rotation invisible to members.
- **Rejected**: server certificate and login only (exposes the login surface
  on shared access points); mutual TLS that cannot be turned off (forces the
  enrollment cost on teams with a private access point); a self-signed server
  certificate re-trusted every 7 days (every member reconfigures weekly);
  mutual TLS for Git only (the web UI shows the same code and comments).
- **Why one line per device**: renewal needs no password, because the
  background job has none. Without these limits a stolen device key could
  renew without end, and revoking one certificate would leave the
  certificates renewed from it valid. With them, the thief and the owner
  cannot both keep renewing (the second renewal from the same certificate
  fails, which the owner sees), and one revocation ends the device.
- **Rejected**: revoking the previous certificate at renewal (a renewal job
  that crashes after the response and before saving locks the device out);
  no limits, relying on disabling the user (a single device could not be
  revoked).

Data at rest is not encrypted by garret in the first release. Files are
created with owner-only permissions, garret warns at startup when disk
encryption appears to be off, and finished projects are erased with the purge
feature.

- **Why**: anyone who knows a shared Wi-Fi password can observe other
  members' traffic. Repository files must stay readable by `git`, so
  application-level encryption of only the database would protect half the
  data.
  HTTP only was rejected for the same reason.

## D12. sqlc for data access, goose for migrations

Schema and queries are written in SQL. [sqlc](https://github.com/sqlc-dev/sqlc)
generates typed Go code from them at build time; the generated code uses only
`database/sql` and is committed. Schema changes are numbered SQL files applied
by [goose](https://github.com/pressly/goose), embedded in the binary and run
at startup. The ERD and table documentation are generated from the real
schema in CI, so they cannot drift from it.

- **Why**: the record must be exact. Hard deletes, soft deletes, append-only
  tables and project purge all depend on precise SQL, and the concurrency
  model needs explicit transaction boundaries. sqlc keeps every statement
  visible and checks it against the schema before the code compiles. It adds
  no runtime module to the binary.
- **Supply chain**: measured by the modules compiled into the binary. The
  SQLite driver alone pulls 10 modules; goose adds 5. Every link is maintained
  by an organization with active releases.
- **Rejected**: GORM (implicit soft-delete scoping makes an incomplete purge
  easy to miss, zero-value updates are silently skipped, hooks hide side
  effects, and the pure-Go SQLite driver goes through single-maintainer forks
  that pin an old SQLite engine); ent (heavier generated code, less control
  over transactions, extra work to use the pure-Go driver); squirrel for
  dynamic queries (no release since 2023); a hand-written migration runner
  (fewer modules, but new code to verify).

## D13. Definition of done

The first release is done when an end-to-end scenario with three users passes
as an automated test, together with the quality gates. See
[testing.md](testing.md).

Supported server platforms: macOS (required) and Linux (verified in CI, not
release-blocking). Windows is not a target.

## D14. How members join is chosen when the server first starts

`garret serve` asks once, on its first start with a new data directory, how
members will join, and keeps the answer in the data directory:

1. **garret enroll on each laptop** (client certificates on, the default and
   the recommendation): D11 unchanged.
2. **A browser and a password only** (client certificates off): the
   administrator creates accounts under Administration and passes on a
   temporary password; members change it under Account. No program runs on
   members' machines.

Later starts use the kept answer. `--client-cert=on|off` still wins for one
run without changing it; a start without a terminal and without a kept
answer uses client certificates on and keeps that.

- **Why**: some teams cannot ask members to install and run a program, and
  the server already supports both modes. Asking at the first start makes
  the choice deliberate and visible instead of a flag a team has to know
  about, while the product keeps device certificates as its default promise.
- **Rejected**: client certificates off by default (gives up the promise of
  D11 for every team); keeping enrollment as the only documented way (shuts
  out teams that cannot run a program); certificate files downloaded in the
  browser (members would re-import a new file every 7 days, and the server
  would generate their private keys).

