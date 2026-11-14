-- Invented for the entry: a keyed table whose replica identity the
-- connector overrides with replica.identity.autoset.values.
CREATE TABLE orders (
    id UUID PRIMARY KEY,
    status TEXT NOT NULL
);
