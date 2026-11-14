-- Invented for the entry: the primary key is added after the table, by
-- ALTER TABLE, as migrations that split constraints out often do.
CREATE TABLE invoices (
    id UUID NOT NULL,
    total NUMERIC NOT NULL
);
ALTER TABLE invoices ADD CONSTRAINT invoices_pkey PRIMARY KEY (id);
