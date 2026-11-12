-- A certificate is renewed at most once, so each device has one line of
-- certificates (docs/design/decisions.md D11). NULLs, the first
-- certificate of every line, do not collide in a unique index.

-- +goose Up
CREATE UNIQUE INDEX client_certificates_renewed_from ON client_certificates (renewed_from);

-- +goose Down
DROP INDEX client_certificates_renewed_from;
