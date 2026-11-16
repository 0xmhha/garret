# Data model

garret stores relational data in SQLite and Git objects in bare
repositories. This document describes the tables, how reviews and comments
relate to Git, and the retention rules.

## Conventions

- Primary keys are ULIDs stored as text, so they sort by creation time and
  reveal nothing about row counts.
- Times are stored as Unix milliseconds (UTC) and produced by an injected
  clock.
- Column-level definitions, the ERD, the data lifecycle and the concurrency
  model are in [../specs/data.md](../specs/data.md).
- Every table that holds user content has a `project_id`, so a project purge
  can find all of its rows.
- Foreign keys are enforced (`PRAGMA foreign_keys = ON`).
- `PRAGMA secure_delete = ON` makes SQLite overwrite deleted content, which
  matters for permanent erasure and project purge.
- Schema changes are numbered goose SQL migrations under
  `internal/store/migrations`, embedded in the binary and applied at startup.
  A migration is never edited after it is merged; changes go in a new file.
- Queries live in `internal/store/queries/*.sql`. sqlc generates the Go code
  into `internal/store/sqlcgen`, which is committed and reviewed like any
  other code. CI fails if regenerating changes it.
- The ERD and per-table documentation are generated from a migrated database
  in CI. CI fails if the committed copy differs.

## Entities

```
users ─┬─ sessions
       ├─ access_tokens
       ├─ client_certificates
       └─ invitations (created_by)

projects ─┬─ reviews ─┬─ revisions ── revision_commits
          │           ├─ verdicts
          │           ├─ comments ── comment_versions
          │           └─ collab_documents (description)
          ├─ discussions ─┬─ comments
          │               └─ collab_documents (body)
          ├─ attachments
          └─ audit_events

collab_documents ─┬─ collab_updates
                  └─ collab_checkpoints

notifications (user, event)
erasures      (what was permanently erased, by whom, when)
```

## Tables

### Identity

| Table | Key columns | Notes |
|---|---|---|
| `users` | `id`, `username` (unique), `display_name`, `password_hash`, `role` (`admin`, `member`), `disabled_at` | Passwords use Argon2id with per-user salt |
| `invitations` | `id`, `token_hash`, `created_by`, `role`, `expires_at`, `used_at`, `used_by` | Single use. Only the SHA-256 of the token is stored |
| `sessions` | `id_hash`, `user_id`, `created_at`, `expires_at`, `last_seen_at` | Browser sessions. Only the hash of the cookie value is stored |
| `client_certificates` | `serial`, `user_id`, `device_name`, `public_key_sha256`, `not_before`, `not_after`, `issued_via` (`invitation`, `renewal`, `setup`), `revoked_at` | One row per issued certificate. Checked on every handshake |
| `access_tokens` | `id`, `user_id`, `name`, `token_hash`, `created_at`, `last_used_at`, `revoked_at` | Used for Git over HTTP |

### Projects and reviews

| Table | Key columns | Notes |
|---|---|---|
| `projects` | `id`, `name` (unique, `[a-z0-9][a-z0-9._-]{0,63}`), `description`, `created_by`, `source_path` (nullable) | Maps to `data/repos/<name>.git`; `source_path` is the repository on the server's machine it syncs from (D15) |
| `reviews` | `id`, `project_id`, `number` (per project), `title`, `source_branch`, `target_branch`, `state`, `author_id`, `description_doc_id` | `state` is `open`, `completed` or `abandoned` |
| `revisions` | `id`, `review_id`, `number`, `base_sha`, `head_sha`, `pushed_by`, `pushed_at` | Head pinned at `refs/garret/reviews/<review>/revisions/<number>` |
| `revision_commits` | `revision_id`, `position`, `sha`, `subject`, `author_name`, `authored_at` | Commits in `base..head`, oldest first |
| `verdicts` | `id`, `review_id`, `revision_id`, `user_id`, `value`, `created_at` | Append-only. `value` is `approve` or `request_changes`. The latest verdict per user counts |
| `review_state_changes` | `id`, `review_id`, `from_state`, `to_state`, `user_id`, `created_at` | Append-only |

### Comments

| Table | Key columns | Notes |
|---|---|---|
| `comments` | `id`, `project_id`, `parent_kind` (`review`, `discussion`), `parent_id`, `thread_root_id`, `reply_to_id`, `author_id`, `anchor`, `resolved`, `deleted_at`, `created_at` | A thread is a root comment plus replies |
| `comment_versions` | `comment_id`, `version`, `body`, `edited_at` | Every edit adds a row. The latest version is shown |

`anchor` is empty for review-level and discussion comments. For code comments
it holds:

| Field | Meaning |
|---|---|
| `revision_id` | The revision the comment was written on |
| `view` | `revision`, `commit` or `range` |
| `from_sha`, `to_sha` | The two sides of the diff the reviewer was looking at |
| `path` | File path on the side being commented |
| `side` | `old` or `new` |
| `start_line`, `end_line` | Inclusive, 1-based. Both empty for a file-level comment |

### Collaborative documents

| Table | Key columns | Notes |
|---|---|---|
| `collab_documents` | `id`, `project_id`, `kind` (`review_description`, `discussion_body`) | One Yjs document each |
| `collab_updates` | `id`, `document_id`, `seq`, `user_id`, `received_at`, `update` (blob) | Append-only. Raw Yjs update as received |
| `collab_checkpoints` | `document_id`, `up_to_seq`, `state` (blob), `text`, `created_at` | Encoded full state and extracted text |
| `discussions` | `id`, `project_id`, `title`, `author_id`, `body_doc_id`, `deleted_at` | Standalone posts with their own comment threads |

### Other

| Table | Key columns | Notes |
|---|---|---|
| `attachments` | `id`, `project_id`, `uploader_id`, `filename`, `media_type`, `size`, `sha256`, `deleted_at` | File stored at `data/attachments/<sha256>` |
| `notifications` | `id`, `user_id`, `kind`, `subject_kind`, `subject_id`, `created_at`, `read_at` | Mentions and activity on the user's reviews |
| `audit_events` | `id`, `project_id`, `actor_id`, `action`, `subject_kind`, `subject_id`, `data` (JSON), `created_at` | Append-only trail of every state change |
| `erasures` | `id`, `actor_id`, `subject_kind`, `subject_id`, `reason`, `created_at` | Records permanent erasures. Holds no erased content |

## Revisions

A push that moves the source branch of an open review creates a revision.
garret stores the pushed head, the merge base with the target branch, and the
list of commits between them. The head is pinned under a hidden ref so the
commits stay reachable after force pushes and branch deletion.

A push that does not move the head (for example, pushing the same commit
again) creates nothing. Deleting the source branch leaves the review and its
revisions intact.

## Comment porting

A comment is written on one revision. When a new revision arrives, garret
shows unresolved comments at their corresponding position in the new
revision. The comment is not copied; it is displayed in another place, and
resolving it there resolves the original. This follows the approach used by
Gerrit's ported comments.

The position is computed from the diff between the old and the new head:

1. Map the file path. A renamed file keeps the comment; a deleted file drops
   it from the new revision (it stays visible on its original revision).
2. Walk the file's diff hunks from top to bottom, tracking how far lines have
   shifted. A comment entirely outside changed hunks moves by the current
   shift.
3. A comment whose lines overlap a changed hunk cannot be placed exactly. It
   is shown as a file-level comment on the new revision with a label pointing
   to its original revision and line.

Resolved comments are not ported. They remain on the revision where they were
written.

## Retention

| Action | Effect |
|---|---|
| Edit a comment | Adds a row to `comment_versions`. Earlier versions stay viewable |
| Delete a comment, discussion or attachment | Sets `deleted_at`. The UI shows a placeholder; content stays in the database |
| Change a verdict or review state | Adds a new row. Earlier rows are never updated |
| Permanent erasure (administrator) | Removes the content and all its versions, writes an `erasures` row and an audit event |
| Permanent erasure of collaborative text (administrator) | Yjs updates keep deleted text, so the only way to remove it is to rebuild the document. garret deletes all updates and checkpoints of the document and starts a new one from the cleaned current text. The per-character authorship of that document is lost, and the `erasures` row records this |
| Project export (administrator) | Writes one archive: a Git bundle per repository with all refs, including hidden ones, plus JSON for every table row in the project and the attachment files |
| Project purge (administrator) | After an export, deletes every row with the project's `project_id`, the repository directory and unreferenced attachment files, then runs `VACUUM` and a WAL checkpoint |

Purge keeps one `erasures` row naming the project and the actor, so the fact
that a project existed and was removed is still traceable.
