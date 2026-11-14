-- Reduced from ClickHouse/clickhouse-kafka-connect discussion 182 and issue
-- 183 ("ClickHouseSinkConnector & Debezium PostgresConnector creates rows
-- zero values or nulls", 2023-09): every insert, update and delete produced
-- a ClickHouse row of zeros and empty strings, with no error anywhere.
CREATE TABLE testradiomeas (
    "measDate"          INTEGER NOT NULL,
    "measLongitude"     DOUBLE PRECISION,
    "measLatitude"      DOUBLE PRECISION,
    "measNetNoCoverage" INTEGER,
    "measNetType"       TEXT
);

-- The discussion's sample message carries a whole "before" row (measDate,
-- measLongitude, ...) on a table with no primary key, which only REPLICA
-- IDENTITY FULL produces: with DEFAULT the connector would emit inserts only.
ALTER TABLE testradiomeas REPLICA IDENTITY FULL;
