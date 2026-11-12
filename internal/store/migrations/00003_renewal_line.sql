-- A certificate is renewed at most once, so each device has one line of
-- certificates (docs/design/decisions.md D11). NULLs, the first
-- certificate of every line, do not collide in a unique index.

-- +goose Up

-- Before renewals were limited, a certificate could be renewed twice.
-- Keep the newest renewal as the successor; revoke the others and detach
-- them from the line so the index below can be created.
UPDATE client_certificates
SET revoked_at = COALESCE(revoked_at, CAST(strftime('%s', 'now') AS INTEGER) * 1000),
    renewed_from = NULL
WHERE renewed_from IS NOT NULL
  AND serial <> (
      SELECT newest.serial FROM client_certificates newest
      WHERE newest.renewed_from = client_certificates.renewed_from
      ORDER BY newest.not_before DESC, newest.serial DESC
      LIMIT 1
  );

CREATE UNIQUE INDEX client_certificates_renewed_from ON client_certificates (renewed_from);

-- +goose Down
DROP INDEX client_certificates_renewed_from;
