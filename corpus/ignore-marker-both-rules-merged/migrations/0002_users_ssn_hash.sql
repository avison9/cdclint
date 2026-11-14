-- The same migration after its pull request merged: with no base there is no
-- change to judge, and the column would be in the inventory of uncaptured
-- columns on every run if the marker named only schema-before-connector.
ALTER TABLE users ADD COLUMN ssn_hash TEXT; -- cdclint:ignore schema-before-connector,source-column-not-captured: PII, never streamed
