# Transport and CLI specification

Status: approved. Transport turns network requests into service calls
([services.md](services.md)) and never touches the store directly. The CLI
starts the server and sets up client devices.

## 1. Scope

| Package | Owns |
|---|---|
| `cmd/garret` | Commands: `serve`, `admin setup`, `enroll`, `renew`, `version` |
| `internal/config` | Flags, defaults and their validation |
| `internal/server` | Listeners, TLS, request identity, graceful shutdown |
| `internal/httpapi` | JSON API of [api.md](../design/api.md), errors, CSRF, pagination, and the WebSocket endpoints (they share the request authentication, so they live in the same package rather than a separate `internal/ws`) |
| `internal/gitserver` | Git smart HTTP through `git http-backend`, push detection |
| `internal/enroll` | Client side of `enroll` and `renew`: key, request, installation |
| `internal/webui` | The embedded web application (a placeholder page until stage 6) |

Decisions of this stage (decision record `20261112-transport`):

- **T-D1** WebSocket library: `github.com/coder/websocket` v1.8.15
  (organization-maintained, no dependencies, context on every call). Chosen
  over `gorilla/websocket` (no release since June 2024) and a hand-written
  RFC 6455 implementation. Supply-chain safety comes first.
- **T-D2** Ports: the main service on 8443 and the enrollment listener on
  8444, both bound to the allowed network's interface addresses only.
- **T-D3** Invitations: the link carries only the server address and the CA
  fingerprint; the invitation code travels by another channel and is typed
  at a hidden prompt; codes are six random words and live 30 minutes.
- **T-D4** ygo is used as released, with the panic guard of services.md 7.
  If panics keep appearing in testing, the fallback is a fork that carries
  the fix and follows upstream releases, dropped and archived once upstream
  ships its own fix.

Requirements carried from the security reviews of stages 2 to 4 are listed
where they apply and marked **(review)**.

## 2. Listeners and TLS

### 2.1 Main listener (8443)

1. Resolve the allowed networks (`netguard.ResolveAllowed`) and the listen
   addresses: the addresses of the interfaces inside those networks, plus
   loopback. `--listen` overrides the address and port.
2. Wrap every listener with `netguard.Listener`, so a peer outside the
   allowed networks is closed before TLS.
3. TLS: minimum TLS 1.3; `GetCertificate` from `pki.ServerCertificates`.
   - Client certificates on (default): `ClientAuth:
     tls.RequireAndVerifyClientCert`, `ClientCAs` holding only garret's CA
     **(review)**. `VerifyConnection` takes the leaf of the verified chain and
     rejects the handshake unless `ActiveSerials.Active(SerialString(serial))`
     **(review)**.
   - `--client-cert=off`: `tls.NoClientCert`, and a warning at every start.
   - `--insecure-http`: plain HTTP, implies `--client-cert=off`, warning at
     every start.
4. Active serials are loaded once before the first `Accept` **(review)**,
   refreshed after every issuance or revocation commit, and also every 30
   seconds (Q1), so a certificate created by `garret admin setup` in another
   process takes effect without a restart.

### 2.2 Enrollment listener (8444)

- Opened while `auth.OpenInvitationsUntil` reports an open invitation and
  closed when the last one is used, revoked or expires; checked after every
  invitation change and at the earliest expiry.
- TLS 1.3 with the same server certificate and no client certificates.
- Serves exactly two routes and nothing else **(review)**: `GET /ca.crt`
  (the CA certificate, public) and `POST /enroll`.
- Rate limited per source address with the login limiter: every failed
  enrollment counts, and a success gives the attempt back.

### 2.3 Request identity

For every request on the main listener, middleware derives, once:

- the certificate serial from `r.TLS.VerifiedChains[0][0]` only, never from
  a header; with certificates on, a request without a verified chain is
  rejected **(review)**;
- the remote address from `r.RemoteAddr`, parsed and unmapped, never from
  `X-Forwarded-For` **(review)**;
- the user: from the session cookie (browser) or Basic credentials (Git),
  through `auth.Authenticate` or `auth.AuthenticateToken`, which bind it to
  the certificate's user.

### 2.4 Shutdown

`SIGINT` or `SIGTERM` cancels the server context: listeners stop accepting,
in-flight requests get 10 seconds (data.md Q4), WebSocket connections are
closed with status 1001, the collaborative document service writes final
checkpoints, and the database closes last.

## 3. JSON API (`internal/httpapi`)

### 3.1 Conventions

- Routing with the standard library `net/http.ServeMux` patterns (Go 1.22+).
- Request bodies: JSON, at most 1 MiB (Q2), unknown fields rejected. Uploads
  are `multipart/form-data` up to 25 MiB plus form overhead.
- Session cookie `__Host-garret_session`: `HttpOnly`, `Secure`,
  `SameSite=Strict`, `Path=/`, no `Domain`; set at login, cleared at logout
  **(review)**.
- State-changing methods (`POST`, `PATCH`, `DELETE`) require the
  `X-CSRF-Token` header checked with `CheckCSRF`, and an `Origin` header equal
  to the server origin **(review)**. The CSRF token is never read from a
  cookie or the query string.
- Pagination: `?cursor=&limit=` (at most 100), `next_cursor` in the response.
- Times are RFC 3339 in UTC.

### 3.2 Errors

One body shape, `{"error": {"code": "...", "message": "..."}}`. Internal
error text is never sent; it is logged with `slog` at the request boundary.

| Service error | Status | Code |
|---|---|---|
| `ErrInvalidCredentials`, `ErrUnauthenticated` | 401 | `unauthenticated` (one generic message) **(review)** |
| `ErrForbidden` | 403 | `forbidden` |
| `ErrNotFound` | 404 | `not_found` |
| `ErrConflict`, `ErrStale` | 409 | `conflict` |
| `ErrInvalid`, `ErrHashFormat` | 400 | `invalid` with the field name |
| `ErrTooLarge` | 413 | `too_large` |
| `ErrInvalidInvitation` | 400 | `invalid_invitation`, one body for every token state **(review)** |
| `ErrExportRequired` | 409 | `export_required` |
| anything else | 500 | `internal` |

### 3.3 Security headers

On every response: `Content-Security-Policy: default-src 'self'; img-src
'self' data: blob:; style-src 'self'; script-src 'self'; connect-src 'self';
frame-ancestors 'none'; base-uri 'none'; form-action 'self'`,
`X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`,
`X-Frame-Options: DENY`. Attachments are served with `Content-Disposition:
attachment` unless `Inline`, and always with their detected media type.

### 3.4 Endpoints

Every endpoint of api.md maps to one service method; the handler parses and
validates the request, calls the service with the request's user, and maps
the result. The collaborative document endpoint moves under its project:
`/api/v1/projects/{project}/collab/{doc}` instead of `/api/v1/collab/{doc}`,
so the project always comes from the path **(review)**; api.md is updated.

## 4. Git endpoint (`internal/gitserver`)

- `/git/{project}.git/...` with HTTP Basic credentials: username and personal
  access token, checked by `AuthenticateToken` with the certificate serial
  and remote address. Failures answer 401 with `WWW-Authenticate: Basic` and
  a body that does not say whether the user exists **(review)**.
- Only the smart protocol is served: `GET info/refs?service=...` and `POST
  git-upload-pack` or `git-receive-pack`; anything else is 404. Each runs
  `git upload-pack` or `git receive-pack --stateless-rpc` through
  `Runner.Serve`, with the runner's minimal environment, the client's
  `Git-Protocol` header (validated) as `GIT_PROTOCOL`, the semaphore, and the
  long timeout; the process is killed when the request ends. `git
  http-backend` was dropped because `net/http/cgi` in Go 1.26 starts it
  without a context. The project name is validated before any path is
  built.
- Pushes to `refs/garret/*` are rejected by Git itself through
  `receive.hideRefs` (an update or delete of a hidden ref is refused).
- Push detection: a per-project repository lock serializes
  `git-receive-pack`; branch heads are read before and after, and the moved
  refs go to `review.HandlePush` with the pusher. Export and purge take the
  same lock. Once the lock is held, the push runs on a context the client
  cannot cancel (still bounded by the long timeout): otherwise a client
  could disconnect right after git moved a branch and the move would never
  become a revision. A crash of the whole server in that window is not
  covered; the next push to the branch records its head.
- Limits: a push body over 500 MiB (components.md Q6) is refused; the whole
  request runs under the 60-second long timeout of components.md Q3.

## 5. WebSocket endpoints (`internal/httpapi`)

Both endpoints authenticate before the upgrade, check `Origin` exactly
against the server origin **(review)**, and run one reader and one writer
goroutine per connection.

- `/api/v1/ws` (events): subscribes to the hub; the writer sends each event
  as JSON text; a full send buffer closes the connection (the hub already
  drops slow subscribers).
- `/api/v1/projects/{project}/collab/{doc}/info` (plain GET) returns the
  generation to join with.
- `/api/v1/projects/{project}/collab/{doc}?generation=N`: a stale generation
  answers 409 before the upgrade, so the client reloads;
  - binary messages only; a text frame closes the connection **(review)**;
  - read limit 1 MiB plus 64 bytes (`SetReadLimit`) **(review)**;
  - the writer has a 64-message buffer and a 10-second write timeout;
    `Conn.Send` never blocks the owner goroutine **(review)**;
  - every exit path calls `Session.Leave` and closes the connection once
    **(review)**;
  - awareness payloads are relayed as received, so the browser must never show
    an awareness name as identity; identity comes from the API **(review)**.
- Pings every 30 seconds; a connection that misses two pongs is closed.
- Limits: 16 connections per user and 64 per document (Q3).
- Disabling a user closes their WebSocket connections (`auth.Config.OnDisable`
  calls `Hub.CloseUser` and closes collaborative sessions) **(review)**.

## 6. Enrollment and renewal

### 6.1 Invitation code and link

- An invitation code is six words drawn uniformly from the EFF large wordlist
  without its four hyphenated words (7,772 words, about 77.5 bits), joined
  by `-`, for example
  `marble-orbit-candle-velvet-thimble-anchor`. The code is shown once to the
  administrator; only its SHA-256 is stored.
- Lifetime 30 minutes, single use (T-D3). security.md's 24 hours changes to
  30 minutes.
- The link is `https://<host>:8444/enroll#ca=<64 hex characters>`. It holds no
  secret and may be sent through any channel. The administrator gives the
  code through a different one, for example in person.
- Input normalization before hashing: lowercase, and any run of spaces or
  hyphens becomes one `-`, so `Marble Orbit …` typed with spaces matches.

### 6.2 `garret enroll <link>`

1. Parse the link; refuse anything but `https`, a port, the path `/enroll`
   and a 64-hex `ca`. A link with user information or a query is refused.
2. Fetch `https://<host>:8444/ca.crt` without verification, compute its
   SHA-256, and refuse unless it equals the link's `ca`. From here on the
   fetched CA is the only trusted root, and the server certificate must
   verify against it for `<host>`.
3. Prompt for the invitation code (hidden), username, display name, password
   (hidden, twice) and device name (defaults to the computer name). Nothing
   secret is taken as a command-line argument, so it never reaches shell
   history or `ps`.
4. Generate an ECDSA P-256 key, build a CSR, and `POST /enroll` with the code,
   account fields and CSR. The response holds the client certificate, the
   CA and `server_url`, the main service's URL that Git and the browser use.
   The client checks that the certificate is for its key and is signed by
   the CA pinned in step 2, and ignores the CA in the response. It accepts
   `server_url` only as `https://<link host>:<port>` with no path, query or
   user information, because the URL scopes the Git settings. Redirects are
   never followed, so the code and password go only to the link's host.
5. Install (6.4).

### 6.3 `garret renew`

Connects to the main port with the current certificate and calls
`POST /api/v1/me/certificates/renew` with a CSR for a new key; the renewal
serial is the handshake's, never a request field **(review)**. Installs the
new certificate. Run by the background job when less than half of the
validity remains (`pki.RenewalDue`), checked every 6 hours (Q4).

### 6.4 Installation on the client

- Files under `~/Library/Application Support/garret/client/<host>-<port>/`
  (macOS) or `$XDG_CONFIG_HOME/garret/client/<host>-<port>/` (Linux):
  `key.pem`, `cert.pem`, `ca.pem`, mode `0600`, directory `0700`.
- Git: `git config --global http.https://<host>:8443/.sslCert`, `.sslKey` and
  `.sslCAInfo` pointing at those files, scoped to the server's URL only.
- macOS: the key and certificate are imported into the login keychain with
  `security import`, and an identity preference is set for
  `https://<host>:8443/`, so Chrome and Safari present the certificate
  without asking. The CA is added to the login keychain as trusted for SSL
  for this user only. A launchd agent
  `~/Library/LaunchAgents/io.garret.renew.<host>.plist` runs `garret renew`
  every 6 hours.
- Linux: a systemd user timer runs `garret renew`; browser installation is
  printed as instructions (Linux is not release-blocking).
- Every step reports what it changed, and `garret enroll --dry-run` prints
  the steps without changing anything.

### 6.5 Installation steps

`internal/enroll` builds the installation as a list of steps before running
any of them. Each step has a one-line summary and is either a command run
without a shell or an action in the process (writing a file). `--dry-run`
prints the summaries; a real run prints each summary as it runs it and stops
at the first failure, except for steps marked optional. Every command has a
60-second timeout, or 5 minutes for the steps where macOS may wait for the
user's password (importing into the keychain, trusting the CA).

On enrollment, after the files of 6.4 are saved:

1. `git config --global` for `sslCert`, `sslKey` and `sslCAInfo` (6.4).
2. macOS only, with `<kc>` the login keychain
   (`~/Library/Keychains/login.keychain-db`):
   1. `security import key.pem -k <kc> -t priv`
   2. `security import cert.pem -k <kc> -t cert`
   3. `security add-trusted-cert -r trustRoot -p ssl -s <host> -k <kc> ca.pem`;
      `-s` limits the trust to the server's host on top of the CA's name
      constraints. macOS asks for the user's password here.
   4. `security set-identity-preference -Z <SHA-1 of cert> -s <server URL>/ <kc>`
   5. Write `~/Library/LaunchAgents/io.garret.renew.<host>-<port>.plist`
      (mode 0600): `garret renew --server <server URL>`, at load and every
      6 hours, output to `renew.log` in the client directory.
   6. `launchctl bootout gui/<uid>/io.garret.renew.<host>-<port>`
      (optional: the job may not exist yet), then
      `launchctl bootstrap gui/<uid> <plist>`.
3. Linux only, with `<units>` `$XDG_CONFIG_HOME/systemd/user` (default
   `~/.config/systemd/user`):
   1. Write `garret-renew-<host>-<port>.service` (one-shot,
      `garret renew --server <server URL>`) and `.timer` (5 minutes after
      boot, then every 6 hours).
   2. `systemctl --user daemon-reload`, then
      `systemctl --user enable --now garret-renew-<host>-<port>.timer`.
4. Other systems: Git only.

On renewal, after the new files are saved, macOS imports the new key and
certificate (steps 2.1 and 2.2), points the identity preference at the new
certificate (2.4), and removes the old identity with
`security delete-identity -Z <SHA-1 of the old cert> <kc>` (optional: the
user may have removed it). Linux and other systems need no step, because
Git reads the files.

Host names in file names and labels come from the parsed link, which
`net/url` has already restricted; a value outside `[A-Za-z0-9.:-]` is
refused before any step is built. Values written into the plist are XML
escaped and values in unit files are quoted with systemd's rules.

Whether a browser asks permission to use the new key after each renewal
(the keychain's access list) is checked on real machines in stage 7
before the access list is changed.

## 7. CLI

| Command | Purpose |
|---|---|
| `garret serve` | Start the server. Flags: `--data`, `--host`, `--allow` (repeatable CIDR), `--listen`, `--enroll-listen`, `--client-cert=on\|off`, `--tls-cert`, `--tls-key`, `--insecure-http` |
| `garret admin setup` | On the host: open the data directory directly, create the first administrator (prompting for the account like `enroll`), issue the administrator's certificate and install it on this machine. Refuses once an administrator exists |
| `garret enroll <link>` | 6.2 |
| `garret renew [--server <url>]` | 6.3; without `--server`, renews every installed server that is due |
| `garret version` | Version and Git commit of the build |

`serve` prints the listen addresses, the allowed networks and the CA
fingerprint at start. It no longer prints a setup link: `garret admin setup`
proves authority by direct access to the data directory, which is private
to the server's user (security.md, Data at rest).

## 8. Settled values

These were open questions in the draft and were accepted as proposed during
review.

| # | Question | Value |
|---|---|---|
| Q1 | Periodic refresh of active serials | Every 30 seconds |
| Q2 | JSON request body limit | 1 MiB |
| Q3 | WebSocket connection limits | 16 per user, 64 per document |
| Q4 | Renewal check interval on clients | Every 6 hours |
| Q5 | Wordlist for invitation codes | EFF large wordlist, embedded with its attribution. Implementation found the license is CC BY 4.0 (EFF's copyright page), not 3.0, and that four words contain a hyphen; they are left out, leaving 7,772 words |
| Q6 | Default host name in certificates | The computer's name with `.local` when `--host` is not given |

## 9. Done criteria for stage 5

Accepted during review:

- every package's tests pass with `-race`, and `make check` and the required
  macOS CI job pass;
- Go integration tests run steps 1, 2, 3, 7 and 8 of the end-to-end scenario
  in [testing.md](../design/testing.md) automatically: start the server,
  enroll two members, push and open a review, reject what must be rejected,
  and keep every record across a restart;
- every pull request gets its own security review.

The web interface (stage 6) and the three-machine run (stage 7) are outside
this stage.
