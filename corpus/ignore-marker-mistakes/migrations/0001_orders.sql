CREATE TABLE orders (
    id       BIGSERIAL PRIMARY KEY,
    total    NUMERIC(12, 2) NOT NULL,
    currency CHAR(3) NOT NULL,
    note     TEXT
);
