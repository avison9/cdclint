-- The change under review adds two columns and leaves both off the
-- connector's include list. One is left off on purpose, and the line that
-- adds it says so; the other was forgotten, and is raised.
ALTER TABLE users
    ADD COLUMN ssn_hash TEXT, -- cdclint:ignore schema-before-connector: PII, never streamed
    ADD COLUMN nickname TEXT;
