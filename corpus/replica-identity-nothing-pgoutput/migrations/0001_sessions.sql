-- Invented for the entry: a keyed table later set to REPLICA IDENTITY
-- NOTHING, read through pgoutput, so through a publication.
CREATE TABLE sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
ALTER TABLE sessions REPLICA IDENTITY NOTHING;
