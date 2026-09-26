-- Each marker here is a mistake cdclint must point out, except the one
-- above note, which acknowledges the finding on the line below it.
-- cdclint:ignore topic-table-mapping: the topic was renamed long ago
CREATE TABLE kafka_orders
(
    `id` Int64,
    `total` String,
    -- cdclint:ignore sink-column-not-captured
    `currency` String,
    -- cdclint:ignore sink-column-not-captured: filled by a backfill job, not by the stream
    `note` Nullable(String) -- cdclint:ignore sink-colum-unknown: a typo in the rule name
)
ENGINE = Kafka
SETTINGS
    kafka_broker_list = '${KAFKA_BROKERS}',
    kafka_topic_list = 'shop.public.orders',
    kafka_group_name = 'clickhouse-orders',
    kafka_format = 'JSONEachRow';
