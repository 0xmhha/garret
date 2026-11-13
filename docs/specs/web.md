# Web interface specification (stage 6)

This specification turns [architecture.md](../design/architecture.md#frontend),
decision D10 in [decisions.md](../design/decisions.md) and
[PRODUCT.md](../../PRODUCT.md) into the plan for the web interface. The
server side (stages 2 to 5) stays as it is; this stage adds the browser
application and only the API it needs.

## 1. Scope

Goal: a team can enroll and then review, discuss, decide and export
entirely in the browser, on macOS laptops with the latest Chrome and
Safari, at the craft level of GitHub pull requests, Gerrit and Linear.

Out of scope for this stage: statistics dashboards, theme settings beyond
light and dark following the system, screens designed for phones,
notification integrations outside garret, Firefox.

Server boundary: the existing API, real-time events, security rules and
database schema stay as they are. An endpoint the interface needs is added
only after this specification and [api.md](../design/api.md) describe it. A
change to existing behaviour is decided separately.

## 2. Layout

```
web/                     Vite project (React, TypeScript)
  package.json
  package-lock.json      committed; CI installs with npm ci
  index.html
  src/
    api/                 fetch wrapper, error mapping, query keys
    events/              WebSocket client, events to query invalidation
    i18n/                catalogs (en.json, ko.json) and the t() module
    markdown/            renderMarkdown(): marked, then DOMPurify
    ui/                  shared components on React Aria Components
    routes/              screens
internal/webui/          Go package that serves the interface
  placeholder/index.html committed; embedded without the webui tag
  dist/                  Vite output; ignored by Git; embedded with -tags webui
```

## 3. Build and embedding (D10)

- `go build` without tags embeds `internal/webui/placeholder`. The server
  works and every non-API path shows the placeholder page.
- `make web` runs `npm ci`, the frontend tests and `vite build` into
  `internal/webui/dist`. `make build` runs `make web`, then
  `go build -tags webui`.
- `internal/webui` reports which one is embedded. `garret serve` prints
  `web interface: not built` and `garret version` adds `web: placeholder`
  when the placeholder is in the binary.
- `make release` builds with the tag and fails when the embedded files are
  the placeholder.
- CI installs Node 24 (the current long-term support line), runs
  `npm ci`, the frontend tests, `npm audit --audit-level=high` and
  `vite build`, then runs the Go tests against the real build.

## 4. Serving

`internal/webui.Handler` serves the embedded files for every path that is
not `/api/` or `/git/`:

- A path that names an embedded file returns it. Files under `assets/`
  carry a content hash in their names and are served with
  `Cache-Control: public, max-age=31536000, immutable`.
- Any other path returns `index.html` with `Cache-Control: no-store`, so the
  browser router handles it.
- Every response carries the security headers of transport.md 3.3. The
  build must not need `unsafe-inline`: no inline scripts or styles in
  `index.html`, and Vite's module preload polyfill is turned off (current
  Chrome and Safari support module preload).

## 5. Frontend modules

Built from the bottom up; each module has tests before the code that uses
it.

| Module | Contract |
|---|---|
| `api` | `request(method, path, body?)`: JSON in and out, sends the CSRF token header on state-changing requests, maps the error body of transport.md 3.2 to a typed error. Query keys are built in one place. The CSRF token from login is kept in `localStorage` so every tab and a reload keep working, and is removed at logout and on any 401. Other sites cannot read it (same-origin policy); script injection on garret's own pages could use it wherever it was kept, which is why sanitizing and the CSP are the defence there. |
| `events` | One WebSocket to `/api/v1/ws` per tab, reconnects with backoff, and turns each event into query invalidations. Never trusts event content beyond its type and IDs. |
| `i18n` | `t(key, values?)` with catalogs `en.json` and `ko.json`; plurals, dates, relative times and numbers through `Intl`. The language follows the browser (Korean when it prefers Korean, otherwise English) and can be switched in account settings. A test fails when a key exists in one catalog and not the other. |
| `markdown` | `renderMarkdown(text)` returns sanitized HTML: marked with raw HTML disabled, then DOMPurify with an allow-list (paragraphs, emphasis, code, pre, lists, block quotes, tables, links with `rel="noopener noreferrer"`, images only from the same origin). It is the only code allowed to set `innerHTML`; a test fails when any other source file writes HTML, and the CSP's Trusted Types enforce it in the browser: the output is a `TrustedHTML` from the `garret-markdown` policy, which DOMPurify also uses internally. |
| `collab` | `connectDoc` speaks the server's y-websocket framing (sync, awareness, reload) for one document. The editor is CodeMirror 6 with Yjs, mounted in a shadow root so CodeMirror's styles use constructed style sheets instead of a `<style>` element the CSP refuses. Remote cursors are not drawn: their colours need inline style attributes, and awareness is relayed unchecked so it never stands for identity; the editor shows only how many others are editing. A reload frame or a closed connection rejoins with a fresh generation. |
| `ui` | Shared components: buttons, menus, tabs, combo box, dialog (native `<dialog>`), tooltip, toasts, keyboard shortcuts. Built on React Aria Components where a native element is not enough. |

## 6. Screens

In build order; the review screen sets the visual system (see the surface
brief in `.impeccable/surfaces/`).

1. Review: header, tabs Conversation, Revisions and Files; diff views of
   architecture.md (revision, commit, range, revision pair); file tree with
   reviewed state; inline threads with porting; verdicts and state;
   collaborative description. The Files tab picks the revision and what to
   show in it (all changes, one commit, a run of commits, or the changes
   since an earlier revision), kept in the URL. A view shows only the
   threads written on exactly its commits; the whole-revision view also
   shows unresolved threads ported from earlier revisions. Comments are
   written on the first three views, not on a comparison of revisions,
   whose line numbers no anchor can hold.
2. Sign-in, and the shell: projects list, navigation, inbox.
3. Project: branches, reviews list with filters, discussions.
4. Discussion: collaborative body, comments, history.
5. Account: tokens, certificates and devices, language. A new token's
   secret is shown once, with a copy button. Revoking the certificate this
   browser presents warns that the device is locked out until it enrolls
   again. The language choice is kept in `localStorage` (`garret.locale`)
   and wins over the browser's preference.
6. Administration: projects (create, export, purge), invitations (code
   shown once, the link without a secret), users, disabling, permanent
   erasure. Every destructive action asks once more before it runs, with
   focus on Cancel; purge also needs the project name typed. Export reads
   the archive into memory, checks that it ends with a zip
   end-of-central-directory record, and only then saves it and shows its
   SHA-256: browsers do not expose the `X-Garret-Export-SHA256` trailer, and
   an archive cut short by a server error lacks that record. Comments and
   collaborative documents carry an "Erase" link for administrators that
   opens the erasure form filled in.

## 7. Done criteria (stage 6)

- Steps 4, 5 and 6 of the end-to-end scenario in
  [testing.md](../design/testing.md) pass automatically in Playwright
  against Chrome and Safari (WebKit).
- An axe check finds no WCAG 2.2 AA violation on every screen of section 6.
- Both catalogs have the same keys and every key is used.
- The impeccable finish review returns `ship` for the review screen and the
  shell, and DESIGN.md records the built design system.
- `make check` and the required macOS CI job pass, including the frontend
  steps.

## 8. Tooling

- Unit tests: Vitest with happy-dom and Testing Library, sharing the Vite
  configuration; browser differences happy-dom misses are caught by the
  Playwright scenarios in real Chrome and WebKit. The sanitizer tests run
  in jsdom instead: under happy-dom 21 DOMPurify reports support but keeps
  `<script>` and `javascript:` links, so they would pass without
  sanitizing. The Playwright suite runs the same cases in Chrome and WebKit.
  Chosen over Vitest with
  jsdom (about twenty more packages) and over the Node test runner with
  Playwright only (every component test in a browser, too slow for
  test-first work).
- Every version is exact (`save-exact`) and at least 7 days old when
  added. Install scripts are disabled (`ignore-scripts`): no dependency
  runs code at install time; Playwright browsers are installed by an
  explicit step.
