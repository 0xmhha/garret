-- The first-release schema defined in docs/specs/data.md section 4.
-- Times are Unix milliseconds (UTC). IDs are ULIDs. Every table is STRICT.

-- +goose Up

-- 4.1 Identity

CREATE TABLE users (
    id            TEXT    NOT NULL PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE,
    display_name  TEXT    NOT NULL CHECK (length(display_name) BETWEEN 1 AND 64),
    password_hash TEXT    NOT NULL,
    role          TEXT    NOT NULL CHECK (role IN ('admin', 'member')),
    created_at    INTEGER NOT NULL,
    disabled_at   INTEGER
) STRICT;

CREATE TABLE invitations (
    id           TEXT    NOT NULL PRIMARY KEY,
    token_sha256 BLOB    NOT NULL UNIQUE CHECK (length(token_sha256) = 32),
    role         TEXT    NOT NULL CHECK (role IN ('admin', 'member')),
    created_by   TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    revoked_at   INTEGER,
    used_at      INTEGER,
    used_by      TEXT    UNIQUE REFERENCES users (id) ON DELETE RESTRICT,
    CHECK ((used_at IS NULL) = (used_by IS NULL))
) STRICT;

CREATE INDEX invitations_open ON invitations (used_at, revoked_at, expires_at);

CREATE TABLE client_certificates (
    serial            TEXT    NOT NULL PRIMARY KEY,
    user_id           TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    device_name       TEXT    NOT NULL CHECK (length(device_name) BETWEEN 1 AND 64),
    public_key_sha256 BLOB    NOT NULL CHECK (length(public_key_sha256) = 32),
    issued_via        TEXT    NOT NULL CHECK (issued_via IN ('invitation', 'renewal', 'setup')),
    renewed_from      TEXT    REFERENCES client_certificates (serial) ON DELETE RESTRICT,
    not_before        INTEGER NOT NULL,
    not_after         INTEGER NOT NULL CHECK (not_after > not_before),
    revoked_at        INTEGER
) STRICT;

CREATE INDEX client_certificates_user ON client_certificates (user_id, revoked_at);

CREATE TABLE sessions (
    id_sha256          BLOB    NOT NULL PRIMARY KEY CHECK (length(id_sha256) = 32),
    user_id            TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    certificate_serial TEXT    REFERENCES client_certificates (serial) ON DELETE RESTRICT,
    csrf_token_sha256  BLOB    NOT NULL CHECK (length(csrf_token_sha256) = 32),
    created_at         INTEGER NOT NULL,
    last_seen_at       INTEGER NOT NULL,
    expires_at         INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expiry ON sessions (expires_at);

CREATE TABLE access_tokens (
    id           TEXT    NOT NULL PRIMARY KEY,
    user_id      TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name         TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    token_sha256 BLOB    NOT NULL UNIQUE CHECK (length(token_sha256) = 32),
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    revoked_at   INTEGER,
    UNIQUE (user_id, name)
) STRICT;

-- 4.4 Collaborative documents come before the tables that reference them.

CREATE TABLE projects (
    id                 TEXT    NOT NULL PRIMARY KEY,
    name               TEXT    NOT NULL UNIQUE,
    description        TEXT    NOT NULL CHECK (length(description) <= 500),
    created_by         TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at         INTEGER NOT NULL,
    next_review_number INTEGER NOT NULL DEFAULT 1 CHECK (next_review_number >= 1)
) STRICT;

CREATE TABLE collab_documents (
    id         TEXT    NOT NULL PRIMARY KEY,
    project_id TEXT    NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    kind       TEXT    NOT NULL CHECK (kind IN ('review_description', 'discussion_body')),
    next_seq   INTEGER NOT NULL DEFAULT 1 CHECK (next_seq >= 1),
    generation INTEGER NOT NULL DEFAULT 1 CHECK (generation >= 1),
    created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE collab_updates (
    document_id TEXT    NOT NULL REFERENCES collab_documents (id) ON DELETE RESTRICT,
    seq         INTEGER NOT NULL CHECK (seq >= 1),
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    received_at INTEGER NOT NULL,
    payload     BLOB    NOT NULL CHECK (length(payload) BETWEEN 1 AND 1048576),
    PRIMARY KEY (document_id, seq)
) STRICT;

CREATE TABLE collab_checkpoints (
    document_id  TEXT    NOT NULL REFERENCES collab_documents (id) ON DELETE RESTRICT,
    up_to_seq    INTEGER NOT NULL CHECK (up_to_seq >= 0),
    state        BLOB    NOT NULL,
    text         TEXT    NOT NULL,
    contributors TEXT    NOT NULL CHECK (json_valid(contributors) AND json_type(contributors) = 'array'),
    created_at   INTEGER NOT NULL,
    PRIMARY KEY (document_id, up_to_seq)
) STRICT;

-- 4.2 Projects and reviews

CREATE TABLE reviews (
    id                 TEXT    NOT NULL PRIMARY KEY,
    project_id         TEXT    NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    number             INTEGER NOT NULL CHECK (number >= 1),
    title              TEXT    NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    source_branch      TEXT    NOT NULL CHECK (source_branch LIKE 'refs/heads/_%'),
    target_branch      TEXT    NOT NULL CHECK (target_branch LIKE 'refs/heads/_%'),
    state              TEXT    NOT NULL CHECK (state IN ('open', 'completed', 'abandoned')),
    author_id          TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    description_doc_id TEXT    NOT NULL UNIQUE REFERENCES collab_documents (id) ON DELETE RESTRICT,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    UNIQUE (project_id, number)
) STRICT;

CREATE UNIQUE INDEX reviews_one_open_per_branch ON reviews (project_id, source_branch) WHERE state = 'open';
CREATE INDEX reviews_project_activity ON reviews (project_id, updated_at);

CREATE TABLE revisions (
    id              TEXT    NOT NULL PRIMARY KEY,
    review_id       TEXT    NOT NULL REFERENCES reviews (id) ON DELETE RESTRICT,
    number          INTEGER NOT NULL CHECK (number >= 1),
    base_sha        TEXT    NOT NULL CHECK (length(base_sha) IN (40, 64)),
    head_sha        TEXT    NOT NULL CHECK (length(head_sha) IN (40, 64)),
    commits_omitted INTEGER NOT NULL DEFAULT 0 CHECK (commits_omitted IN (0, 1)),
    pushed_by       TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    pushed_at       INTEGER NOT NULL,
    UNIQUE (review_id, number)
) STRICT;

CREATE TABLE revision_commits (
    revision_id TEXT    NOT NULL REFERENCES revisions (id) ON DELETE RESTRICT,
    position    INTEGER NOT NULL CHECK (position >= 0),
    sha         TEXT    NOT NULL CHECK (length(sha) IN (40, 64)),
    subject     TEXT    NOT NULL CHECK (length(subject) <= 200),
    author_name TEXT    NOT NULL CHECK (length(author_name) <= 200),
    authored_at INTEGER NOT NULL,
    PRIMARY KEY (revision_id, position)
) STRICT;

CREATE TABLE verdicts (
    id          TEXT    NOT NULL PRIMARY KEY,
    review_id   TEXT    NOT NULL REFERENCES reviews (id) ON DELETE RESTRICT,
    revision_id TEXT    NOT NULL REFERENCES revisions (id) ON DELETE RESTRICT,
    user_id     TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    value       TEXT    NOT NULL CHECK (value IN ('approve', 'request_changes')),
    created_at  INTEGER NOT NULL
) STRICT;

CREATE INDEX verdicts_review_user ON verdicts (review_id, user_id, created_at);

CREATE TABLE review_state_changes (
    id         TEXT    NOT NULL PRIMARY KEY,
    review_id  TEXT    NOT NULL REFERENCES reviews (id) ON DELETE RESTRICT,
    from_state TEXT    NOT NULL CHECK (from_state IN ('open', 'completed', 'abandoned')),
    to_state   TEXT    NOT NULL CHECK (to_state IN ('open', 'completed', 'abandoned')),
    user_id    TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL,
    CHECK (from_state <> to_state)
) STRICT;

CREATE INDEX review_state_changes_review ON review_state_changes (review_id, created_at);

-- 4.4 Discussions

CREATE TABLE discussions (
    id          TEXT    NOT NULL PRIMARY KEY,
    project_id  TEXT    NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    title       TEXT    NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    author_id   TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    body_doc_id TEXT    NOT NULL UNIQUE REFERENCES collab_documents (id) ON DELETE RESTRICT,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    deleted_at  INTEGER
) STRICT;

CREATE INDEX discussions_project_activity ON discussions (project_id, updated_at);

-- 4.3 Comments

CREATE TABLE comments (
    id                 TEXT    NOT NULL PRIMARY KEY,
    project_id         TEXT    NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    review_id          TEXT    REFERENCES reviews (id) ON DELETE RESTRICT,
    discussion_id      TEXT    REFERENCES discussions (id) ON DELETE RESTRICT,
    thread_root_id     TEXT    REFERENCES comments (id) ON DELETE RESTRICT,
    author_id          TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    anchor_revision_id TEXT    REFERENCES revisions (id) ON DELETE RESTRICT,
    anchor_view        TEXT    CHECK (anchor_view IN ('revision', 'commit', 'range')),
    anchor_from_sha    TEXT    CHECK (length(anchor_from_sha) IN (40, 64)),
    anchor_to_sha      TEXT    CHECK (length(anchor_to_sha) IN (40, 64)),
    anchor_path        TEXT    CHECK (length(CAST(anchor_path AS BLOB)) BETWEEN 1 AND 4096),
    anchor_side        TEXT    CHECK (anchor_side IN ('old', 'new')),
    anchor_start_line  INTEGER CHECK (anchor_start_line >= 1),
    anchor_end_line    INTEGER CHECK (anchor_end_line >= anchor_start_line),
    resolved           INTEGER NOT NULL DEFAULT 0 CHECK (resolved IN (0, 1)),
    current_version    INTEGER NOT NULL CHECK (current_version >= 0),
    created_at         INTEGER NOT NULL,
    deleted_at         INTEGER,
    deleted_by         TEXT    REFERENCES users (id) ON DELETE RESTRICT,
    CHECK ((review_id IS NULL) <> (discussion_id IS NULL)),
    CHECK (
        (anchor_revision_id IS NULL AND anchor_view IS NULL AND anchor_from_sha IS NULL
            AND anchor_to_sha IS NULL AND anchor_path IS NULL AND anchor_side IS NULL
            AND anchor_start_line IS NULL AND anchor_end_line IS NULL)
        OR
        (anchor_revision_id IS NOT NULL AND anchor_view IS NOT NULL AND anchor_from_sha IS NOT NULL
            AND anchor_to_sha IS NOT NULL AND anchor_path IS NOT NULL AND anchor_side IS NOT NULL
            AND (anchor_start_line IS NULL) = (anchor_end_line IS NULL))
    ),
    CHECK (anchor_revision_id IS NULL OR (review_id IS NOT NULL AND thread_root_id IS NULL)),
    CHECK ((deleted_at IS NULL) = (deleted_by IS NULL))
) STRICT;

CREATE INDEX comments_review_threads ON comments (review_id, thread_root_id, created_at);
CREATE INDEX comments_discussion_threads ON comments (discussion_id, thread_root_id, created_at);
CREATE INDEX comments_anchor ON comments (anchor_revision_id, anchor_path);

CREATE TABLE comment_versions (
    comment_id TEXT    NOT NULL REFERENCES comments (id) ON DELETE RESTRICT,
    version    INTEGER NOT NULL CHECK (version >= 1),
    body       TEXT    NOT NULL CHECK (length(CAST(body AS BLOB)) BETWEEN 1 AND 65536),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (comment_id, version)
) STRICT;

-- 4.5 Attachments, notifications, audit

CREATE TABLE attachments (
    id          TEXT    NOT NULL PRIMARY KEY,
    project_id  TEXT    NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    uploader_id TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    filename    TEXT    NOT NULL CHECK (length(CAST(filename AS BLOB)) BETWEEN 1 AND 255),
    media_type  TEXT    NOT NULL,
    size        INTEGER NOT NULL CHECK (size BETWEEN 0 AND 26214400),
    sha256      BLOB    NOT NULL CHECK (length(sha256) = 32),
    created_at  INTEGER NOT NULL,
    deleted_at  INTEGER
) STRICT;

CREATE INDEX attachments_project ON attachments (project_id, sha256);

CREATE TABLE comment_attachments (
    comment_id    TEXT NOT NULL REFERENCES comments (id) ON DELETE RESTRICT,
    attachment_id TEXT NOT NULL REFERENCES attachments (id) ON DELETE RESTRICT,
    PRIMARY KEY (comment_id, attachment_id)
) STRICT;

CREATE TABLE notifications (
    id           TEXT    NOT NULL PRIMARY KEY,
    user_id      TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    project_id   TEXT    NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    kind         TEXT    NOT NULL CHECK (kind IN ('mention', 'comment_on_my_review', 'reply_to_me',
                                                  'verdict_on_my_review', 'revision_on_reviewed')),
    subject_kind TEXT    NOT NULL CHECK (subject_kind IN ('comment', 'review', 'discussion', 'revision')),
    subject_id   TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    read_at      INTEGER
) STRICT;

CREATE INDEX notifications_inbox ON notifications (user_id, read_at, created_at);

CREATE TABLE audit_events (
    id           TEXT    NOT NULL PRIMARY KEY,
    project_id   TEXT    REFERENCES projects (id) ON DELETE RESTRICT,
    actor_id     TEXT    REFERENCES users (id) ON DELETE RESTRICT,
    action       TEXT    NOT NULL CHECK (action GLOB '[a-z]*.[a-z]*'),
    subject_kind TEXT    NOT NULL,
    subject_id   TEXT    NOT NULL,
    data         TEXT    NOT NULL CHECK (json_valid(data) AND json_type(data) = 'object'),
    created_at   INTEGER NOT NULL
) STRICT;

CREATE INDEX audit_events_project ON audit_events (project_id, created_at);
CREATE INDEX audit_events_subject ON audit_events (subject_kind, subject_id);

CREATE TABLE erasures (
    id            TEXT    NOT NULL PRIMARY KEY,
    actor_id      TEXT    NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    scope         TEXT    NOT NULL CHECK (scope IN ('comment', 'attachment', 'collab_document', 'project')),
    subject_id    TEXT    NOT NULL,
    subject_label TEXT    NOT NULL,
    reason        TEXT    NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
    created_at    INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE erasures;
DROP TABLE audit_events;
DROP TABLE notifications;
DROP TABLE comment_attachments;
DROP TABLE attachments;
DROP TABLE comment_versions;
DROP TABLE comments;
DROP TABLE discussions;
DROP TABLE review_state_changes;
DROP TABLE verdicts;
DROP TABLE revision_commits;
DROP TABLE revisions;
DROP TABLE reviews;
DROP TABLE collab_checkpoints;
DROP TABLE collab_updates;
DROP TABLE collab_documents;
DROP TABLE projects;
DROP TABLE access_tokens;
DROP TABLE sessions;
DROP TABLE client_certificates;
DROP TABLE invitations;
DROP TABLE users;
