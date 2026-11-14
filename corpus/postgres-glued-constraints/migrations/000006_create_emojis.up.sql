-- Mattermost v10.11.0, server/channels/db/migrations/postgres/
-- 000006_create_emojis.up.sql, reduced to the table. The table constraint
-- is written UNIQUE(name, deleteat), with no space before the parenthesis.
CREATE TABLE IF NOT EXISTS emoji (
    id VARCHAR(26) PRIMARY KEY,
    createat bigint,
    updateat bigint,
    deleteat bigint,
    creatorid VARCHAR(26),
    name VARCHAR(64),
    UNIQUE(name, deleteat)
);
