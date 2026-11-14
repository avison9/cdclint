-- The change under review adds ssn_hash and leaves it off the include list on
-- purpose, with the marker the diff rule's own fix suggests: both rules named.
ALTER TABLE users ADD COLUMN ssn_hash TEXT; -- cdclint:ignore schema-before-connector,source-column-not-captured: PII, never streamed
