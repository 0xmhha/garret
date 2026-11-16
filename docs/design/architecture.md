# Architecture

## Overview

garret is one Go process that serves three things on one HTTPS port: the Git
smart HTTP protocol, a JSON API with a WebSocket channel, and the embedded web
application. All state lives under one data directory.

```
                    +------------------------------------------------------+
 browser  --HTTPS-->|  netguard  ->  router                                |
 git      --HTTPS-->|                 |-- /git/...      gitserver          |
                    |                 |-- /api/v1/...   api  -> services   |
                    |                 |-- /api/v1/ws    realtime hub       |
                    |                 |-- /api/v1/collab/{doc}  collab     |
                    |                 '-- /*            embedded web app   |
                    |                                                      |
                    |  services: auth, project, review, comment,           |
                    |            discussion, attachment, inbox, admin      |
                    |  store:    SQLite (modernc, WAL) behind interfaces   |
                    |  gitrepo:  git CLI with context timeouts             |
                    +------------------------------------------------------+
                         data/
                           garret.db          SQLite database
                           repos/<name>.git   bare repositories
                           attachments/       content-addressed files
                           tls/               generated certificate and key
```

Every request passes through `netguard` first. A request from outside the
allowed network is closed before routing, authentication or Git handling.

## Packages

The layout follows the Go convention of hiding implementation under
`internal/`. Each package owns one concern.

| Package | Owns |
|---|---|
| `cmd/garret` | Command-line entry point: `serve`, `admin`, `export`, `purge` |
| `internal/config` | Flag and file configuration, validated at startup |
| `internal/netguard` | Allowed-network detection and the connection filter |
| `internal/pki` | Private CA, server certificate rotation, client certificate issuance, renewal and revocation |
| `internal/enroll` | Client side of `garret enroll` and `garret renew`: key generation, keychain and Git setup, renewal job |
| `internal/auth` | Users, invitations, password hashing, sessions, access tokens |
| `internal/gitserver` | Smart HTTP endpoint and push authorization |
| `internal/gitrepo` | Read operations on bare repositories (log, diff, merge-base) |
| `internal/review` | Reviews, revisions, verdicts and comment porting |
| `internal/comment` | Comment threads, edits, soft delete |
| `internal/collab` | Collaborative documents, Yjs sync and update storage |
| `internal/realtime` | WebSocket hub that pushes events to connected clients |
| `internal/inbox` | Mention detection and notifications |
| `internal/attachment` | Attachment upload, storage and download |
| `internal/audit` | Append-only event log written by every service |
| `internal/admin` | Permanent erasure, project export and project purge |
| `internal/store` | SQLite access, migrations, repository interfaces |
| `web/` | React application, built by Vite and embedded at build time |

Services receive their dependencies (store, clock, Git runner, event sink)
through constructors. There is no package-level mutable state.

## Startup

1. Load and validate configuration. Invalid values stop the process.
2. Resolve the allowed network (see [security.md](security.md#network-boundary)).
   Stop if it is ambiguous or public.
3. Open the data directory, check permissions, open SQLite and run migrations.
4. Load or create the CA, then load or issue the server certificate and start
   its rotation timer. Print the listen address, the allowed network and the
   CA fingerprint. Open the enrollment listener if unused invitations exist.
5. If no administrator exists, print a one-time setup link for the first
   administrator.
6. Warn if disk encryption appears to be disabled.
7. Start the HTTP server. Shut down gracefully on `SIGINT` or `SIGTERM`.

## Git hosting

### Serving the protocol

garret delegates pack negotiation to Git: each smart HTTP request runs
`git upload-pack` or `git receive-pack` in stateless RPC mode with an
explicit environment, under a context and a timeout. This reuses Git's own
implementation instead of re-implementing it. `git http-backend` through
Go's `net/http/cgi` was the first plan; it was dropped because `net/http/cgi`
starts the process without a context, so a stuck request could not be
stopped. Git must be installed on the host.

Each repository is a bare repository at `data/repos/<project>.git`, created
with:

- `uploadpack.hideRefs = refs/garret` so internal refs are never advertised.
- `receive.hideRefs = refs/garret` and a server-side check that rejects any
  push to `refs/garret/*`.
- `receive.denyDeleteCurrent = true`.

### Authentication and authorization

Git clients authenticate with HTTP Basic credentials: the username and a
personal access token. Passwords are not accepted on the Git endpoint. In the
first release every member can fetch from and push to every project; only
administrators create projects.

### Detecting pushes

garret records the branch heads before handing a `git-receive-pack` request to
`git http-backend`, and reads them again after the request completes. Each
changed ref becomes a push event carrying the pusher's identity, the old and
the new commit. This avoids installing hook scripts in the repositories.

A push event updates any open review on that branch:

1. If the head moved, create revision *n+1*.
2. Pin the new head under `refs/garret/reviews/<review>/revisions/<n+1>`, so a
   later force push cannot garbage-collect it.
3. Compute and store the base commit, `merge-base(target, head)`.
4. Port unresolved comments from the previous revision (see
   [data-model.md](data-model.md#comment-porting)).
5. Publish a `revision.created` event.

### Importing from the server's machine

A project created with a source path (decision D15) copies branches from a
repository on the server's machine, and the "Sync from source" action does
it again:

1. Check that the source is an absolute path to an existing Git repository.
2. With the project's repository locked, `git fetch --no-tags` the
   source's branches into `refs/garret/import/*`: every `refs/heads/<name>`
   as `<name>`, and every `refs/remotes/<remote>/<name>` (except `HEAD`) as
   `<remote>/<name>` unless a local branch already has that name.
3. For each source branch: create it if it is new, move it if the move is a
   fast-forward, and otherwise list it as rewritten; a rewritten branch moves
   only when the request asks to overwrite. Branches missing from the
   source are kept. The import refs are deleted.
4. Branches that moved become a push event by the person who asked, exactly
   as in "Detecting pushes".

### Reading repositories

`internal/gitrepo` runs `git` subcommands (`rev-list`, `diff`, `merge-base`,
`cat-file`, `diff-tree`) without a shell, with arguments passed as a list and
every call bound to a context with a timeout. Diff output is parsed into a
structured form for the API. Large files and binary files are reported as
such instead of being diffed.

## Reviews and revisions

A review links a source branch to a target branch in one project. Its
revisions form an ordered list of `(base, head)` pairs. The UI offers four
views over the same data:

- the whole revision: `base..head`
- one commit: `commit^..commit`
- a range of commits inside a revision
- a revision compared with another revision: `head(a)..head(b)`

When two revisions have different bases (the branch was rebased), the
revision-to-revision diff also contains upstream changes. The first release
shows a notice in that case instead of trying to separate them.

## Real-time updates

The browser keeps one WebSocket connection to `/api/v1/ws`. Services publish
events (`comment.created`, `revision.created`, `verdict.recorded`,
`notification.created` and so on) to the hub. The hub delivers each event to
connections whose user can see the related project. Clients treat events as
hints and re-fetch the affected resource through the JSON API, so a missed
event never leaves stale state after the next fetch.

## Collaborative documents

Discussion posts and review descriptions are collaborative documents.

- The browser binds a `Y.Text` to a CodeMirror 6 editor and syncs it over
  `/api/v1/collab/{doc}` using the Yjs sync protocol.
- The server keeps the document in memory with ygo while at least one client
  is connected, and unloads it afterwards.
- Every update received from a client is appended to the database together
  with the authenticated user and the time. Updates are never rewritten, so
  the authorship history survives CRDT garbage collection.
- After a quiet period the server writes a checkpoint: the full document
  state encoded by ygo, plus the plain text extracted from it. The text feeds
  the history view, export and mention detection.
- On load the server rebuilds the document from the latest checkpoint state
  plus the updates received after it. The raw updates stay in the database
  either way.

garret serves the Yjs sync protocol from its own authenticated WebSocket
handler, using ygo's transport-agnostic `sync` package rather than ygo's
bundled server. A spike confirmed this before stage 4 (see
[specs/services.md](../specs/services.md#7-collaborative-documents-internalcollab)).

## Frontend

The React application lives in `web/` and is built with Vite into
`internal/webui/dist`, which is embedded with `go:embed` when the binary is
built with the `webui` tag; without it a committed placeholder page is
embedded (decision D10). Routing happens in the browser; the server returns
`index.html` for unknown non-API paths. The plan for this stage is
[specs/web.md](../specs/web.md).
