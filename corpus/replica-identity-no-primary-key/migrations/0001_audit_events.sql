-- Invented for the entry: a table created without a primary key, as tables
-- meant only to be appended to often are, then captured like the rest.
CREATE TABLE audit_events (
    actor_id UUID NOT NULL,
    action TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
