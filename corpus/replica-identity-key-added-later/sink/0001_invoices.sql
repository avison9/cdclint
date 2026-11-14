-- Invented sink for the entry: a Kafka-engine table reading the captured
-- table. The entry is about the source table's replica identity.
CREATE TABLE kafka_invoices
(
    `id` String,
    `total` String
)
ENGINE = Kafka
SETTINGS
    kafka_broker_list = '${KAFKA_BROKERS}',
    kafka_topic_list = 'app.public.invoices',
    kafka_group_name = 'clickhouse-invoices',
    kafka_format = 'JSONEachRow';
