# API

garret serves everything on one HTTPS origin. This document lists the
endpoints of the first release. Request and response bodies are JSON unless
stated otherwise.

## Conventions

- Base path: `/api/v1`.
- Browser requests authenticate with the session cookie. State-changing
  requests must also carry the `X-CSRF-Token` header returned at login, and
  their `Origin` header must match the server origin.
- Errors use one shape:

  ```json
  { "error": { "code": "review_not_found", "message": "review 12 does not exist" } }
  ```

  `code` is stable and documented; `message` is for humans.
- Lists are paginated with `?cursor=` and `?limit=` (maximum 100) and return
  `next_cursor`.
- Times are RFC 3339 in UTC.

## Authentication

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/auth/login` | Start a session |
| `POST` | `/auth/logout` | End the session |
| `GET` | `/me` | Current user |
| `GET` `POST` | `/me/tokens` | List or create personal access tokens. The token value is shown once |
| `DELETE` | `/me/tokens/{id}` | Revoke a token |

## Administration

| Method | Path | Purpose |
|---|---|---|
| `GET` `POST` | `/admin/invitations` | List or create invitations |
| `DELETE` | `/admin/invitations/{id}` | Revoke an unused invitation |
| `GET` | `/admin/users` | List users |
| `POST` | `/admin/users/{id}/disable` | Disable a user and revoke their sessions and tokens |
| `POST` | `/admin/erasures` | Permanently erase one comment, attachment or collaborative text |
| `POST` | `/admin/projects/{project}/export` | Produce an export archive and return a download link |
| `POST` | `/admin/projects/{project}/purge` | Erase the project. Requires a completed export and the project name typed again |

## Projects

| Method | Path | Purpose |
|---|---|---|
| `GET` `POST` | `/projects` | List projects, or create one (administrators) |
| `GET` | `/projects/{project}` | Project details and clone URL |
| `GET` | `/projects/{project}/branches` | Branches with their heads |

## Reviews

| Method | Path | Purpose |
|---|---|---|
| `GET` `POST` | `/projects/{project}/reviews` | List reviews (filter by `state`, `author`), or open one from a branch |
| `GET` | `/projects/{project}/reviews/{n}` | Review details, current state, latest verdict per user |
| `POST` | `/projects/{project}/reviews/{n}/state` | Complete, abandon or reopen |
| `GET` | `/projects/{project}/reviews/{n}/revisions` | Revisions with their commits |
| `GET` | `/projects/{project}/reviews/{n}/diff` | Structured diff. Query: `rev`, or `commit`, or `from`+`to` (commit range or revision pair) |
| `GET` | `/projects/{project}/reviews/{n}/file` | File content at a commit, for expanding context |
| `POST` | `/projects/{project}/reviews/{n}/verdicts` | Record `approve` or `request_changes` on the latest revision |
| `GET` | `/projects/{project}/reviews/{n}/history` | Audit trail of the review |

## Comments

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/projects/{project}/reviews/{n}/comments` | Threads. Query `rev` returns ported positions for that revision |
| `POST` | `/projects/{project}/reviews/{n}/comments` | New thread (with optional anchor) or reply |
| `GET` `POST` | `/projects/{project}/discussions/{id}/comments` | Same for discussions |
| `PATCH` | `/comments/{id}` | Edit body (author only). Adds a version |
| `POST` | `/comments/{id}/resolve` | Resolve or unresolve a thread |
| `DELETE` | `/comments/{id}` | Soft delete (author or administrator) |
| `GET` | `/comments/{id}/versions` | Edit history |

## Discussions

| Method | Path | Purpose |
|---|---|---|
| `GET` `POST` | `/projects/{project}/discussions` | List or create discussion posts |
| `GET` `PATCH` `DELETE` | `/projects/{project}/discussions/{id}` | Read, rename or soft delete |
| `GET` | `/projects/{project}/discussions/{id}/history` | Text checkpoints over time with their contributors |

## Attachments

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/projects/{project}/attachments` | Upload (`multipart/form-data`). **Proposed default** size limit: 25 MiB |
| `GET` | `/attachments/{id}` | Download. Always served as `Content-Disposition: attachment` |
| `DELETE` | `/attachments/{id}` | Soft delete |

## Inbox

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/inbox` | Notifications, newest first. Query `unread=true` |
| `POST` | `/inbox/read` | Mark notifications as read |

## WebSocket channels

### Events: `/api/v1/ws`

Server-to-client messages only. Each message names an event and the resource
to re-fetch:

```json
{ "type": "comment.created", "project": "libfoo", "review": 12, "id": "01J..." }
```

Event types: `revision.created`, `comment.created`, `comment.updated`,
`comment.resolved`, `verdict.recorded`, `review.state_changed`,
`discussion.created`, `notification.created`.

### Collaborative documents: `/api/v1/collab/{doc}`

Binary messages in the Yjs sync and awareness protocols. The connection is
accepted only for users who can see the document's project.

## Git

| Path | Purpose |
|---|---|
| `/git/{project}.git/info/refs` | Ref advertisement |
| `/git/{project}.git/git-upload-pack` | Fetch and clone |
| `/git/{project}.git/git-receive-pack` | Push |

Clients use HTTP Basic authentication with their username and a personal
access token:

```sh
git remote add review https://<host>:<port>/git/<project>.git
git -c http.sslCAInfo=garret.pem push review fix/my-branch
```

`garret enroll` configures the client certificate and the CA for the server's
URL, so the `-c` option above is only needed on a machine configured by hand.

## Enrollment and renewal

| Listener | Method | Path | Purpose |
|---|---|---|---|
| Enrollment | `POST` | `/enroll` | Invitation token, CSR, username and password in; client certificate and CA certificate out |
| Main | `POST` | `/api/v1/me/certificates/renew` | CSR in; new client certificate out. Requires the current valid client certificate |
| Main | `GET` | `/api/v1/me/certificates` | The user's device certificates |
| Main | `DELETE` | `/api/v1/me/certificates/{serial}` | Revoke one of the user's device certificates |
