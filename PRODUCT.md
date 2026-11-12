# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

A small team, typically two to six people, fixing an undisclosed
vulnerability in a public open-source project. They sit in the same room on
the same Wi-Fi access point or router, each on their own laptop, and review
the fix together for a few hours to a few days. One of them runs the server
on their machine and is the administrator; the others are invited members
who write, review and discuss the fix.

## Product Purpose

garret is a short-lived, private room for code that is not ready to be seen.
It lets the team push branches, review them revision by revision, comment
on lines and commits, edit descriptions together and discuss the fix,
without pushing anything to a public host before a patched release exists.

Success means the fix is reviewed thoroughly, every revision, comment and
decision is traceable, and once the fix is public the team exports the
project to one archive and purges it from the server.

## Positioning

A review server that is reachable from one local network and nowhere else,
shipped as one binary with an embedded database. Every device proves itself
with a client certificate before it can even see the login page, and the
whole review history is designed to be exported and then permanently erased.
It is not a replacement for a team's main code review platform.

## Operating Context

- The server runs on one member's laptop (macOS is the supported server
  platform; Linux is allowed but not release-blocking).
- Members join with `garret enroll <invitation link>`, typing an invitation
  code that the administrator gives them through another channel. The
  command installs a client certificate into the system keychain, so the
  browser presents it automatically.
- Git pushes go to the same server over HTTPS with a personal access token.
- Work happens in long, focused sessions: reading diffs, porting comments
  across force-pushed revisions, approving or requesting changes, and
  writing the description and discussions together in real time.
- At the end the administrator exports and purges the project.

## Capabilities and Constraints

- Projects hold Git repositories; reviews track a branch against a target
  branch, and every push creates a new revision.
- Diff views: a whole revision, one commit, a range of commits inside a
  revision, and a pair of revisions. Unresolved code comments are ported to
  their new positions on later revisions.
- Comments: threads anchored to lines or files, replies, edits with version
  history, resolution, soft delete; Markdown rendered on the client through
  an allow-list sanitizer, with no raw HTML.
- Verdicts: approve or request changes on the latest revision; authors do not
  approve their own reviews. Review states: open, completed, abandoned.
- Collaborative documents: the review description and discussion bodies are
  edited by several people at once (Yjs), with checkpoint history.
- Real-time events over WebSocket; an inbox of notifications for mentions,
  replies, verdicts and new revisions.
- Attachments (logs, proofs of concept) up to 25 MiB; images inline, every
  other type downloads.
- Administration: invitations, users, disabling users, permanent erasure of
  comments, attachments and documents, project export and purge.
- Two roles: members read and write every project; administrators also
  manage projects, invitations, users, erasure, export and purge.
- Browsers: the latest Chrome and Safari. Firefox is not supported while
  client certificates are on.
- The JSON API is at `/api/v1` (docs/design/api.md). The interface is served
  from the same origin under a strict Content-Security-Policy: scripts,
  styles and connections only from the server itself, no inline scripts or
  styles, no third-party fonts or assets.
- Interface language: English and Korean. All interface strings live in
  translation catalogs from the start.
- Documentation, code and repository history are in English.

## Stack

Settled in docs/design/decisions.md (D10): React, TypeScript and Vite; the
build output is embedded in the Go binary with `go:embed`. CodeMirror 6 for
the editor, Yjs for collaborative editing. New dependencies follow the
supply-chain rules in docs/design/security.md: a lockfile, `npm ci`, a
7-day cooldown on new releases, and `npm audit` in CI.

## Brand Commitments

- The name is garret, written in lowercase. A garret is a small room tucked
  under the roof: a private space inside the house. The name nods to Gerrit,
  the review tool that inspired it.
- The project is open source under the Apache License 2.0.
- No logo, palette or typeface has been chosen yet.
- Standing preference (chosen by the user, 2026-11-12): the category
  standard, played straight. garret looks and behaves like a familiar code
  review tool, with no themed world laid over it. The craft bar is GitHub
  pull requests (file diffs and inline threads), Gerrit (revision-based
  review and diffs between revisions) and Linear (keyboard-first operation,
  density, finish). Interfaces that look generated by AI or toy-like are
  explicitly unwanted.

## Evidence on Hand

- README.md, docs/design/ and docs/specs/ describe the product and its
  security model.
- There are no users, testimonials, case studies or metrics yet; future work
  must not invent them.

## Product Principles

1. Privacy first: nothing leaves the local network, and nothing on screen
   suggests otherwise.
2. The trail is the product: every revision, comment and decision stays
   traceable until the team chooses to erase it.
3. Built for long, focused review sessions: dense information, fast
   navigation, keyboard first.
4. Short-lived by design: setup, export and purge are first-class tasks, not
   afterthoughts.
5. Trust is visible: certificate, session and connection state are always
   clear to the person using it.

## Accessibility & Inclusion

WCAG 2.2 AA on every screen: contrast, full keyboard operation with visible
focus, and labels for screen readers. Review tools are used for hours with
the keyboard, so keyboard paths are designed, not retrofitted.
